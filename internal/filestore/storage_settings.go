package filestore

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
)

var ErrStorageConflict = errors.New("storage settings conflict")

const storageTransitionLock int64 = 0x7067626173650034
const maximumStorageOperations = 64

type StorageConfig struct {
	Driver, LocalDirectory, Bucket, Region, Endpoint, AccessKey, SecretKey string
	UsePathStyle, AllowInsecureLoopback                                    bool
}
type StorageSettings struct {
	Driver           string `json:"driver"`
	Bucket           string `json:"bucket"`
	Region           string `json:"region"`
	Endpoint         string `json:"endpoint"`
	AccessKey        string `json:"accessKey"`
	UsePathStyle     bool   `json:"usePathStyle"`
	SecretConfigured bool   `json:"secretConfigured"`
	Revision         int64  `json:"revision"`
}
type StorageUpdate struct {
	Driver, Bucket, Region, Endpoint, AccessKey, SecretKey string
	UsePathStyle, ClearSecret                              bool
	Revision                                               int64
}

type storageLeaseContextKey struct{}

type storageLease struct {
	store    ObjectStore
	revision int64
	backend  uint32
}

type StorageController struct {
	pool                  *database.Pool
	key                   [32]byte
	localDirectory        string
	allowInsecureLoopback bool
	mu                    sync.RWMutex
	revision              int64
	store                 ObjectStore
	operations            chan struct{}
}

// BeginStorageOperation prevents a destination transition until a compound
// operation has either attached or queued every object it created.
func (c *StorageController) BeginStorageOperation(ctx context.Context) (context.Context, func(), error) {
	select {
	case c.operations <- struct{}{}:
	case <-ctx.Done():
		return ctx, nil, ctx.Err()
	}
	conn, err := c.pool.OpenCoordinationSession(ctx)
	if err != nil {
		<-c.operations
		return ctx, nil, err
	}
	if _, err = conn.Exec(ctx, `select pg_advisory_lock_shared($1)`, storageTransitionLock); err != nil {
		_ = conn.Close(context.Background())
		<-c.operations
		return ctx, nil, err
	}
	settings, encrypted, err := loadStorageSettings(ctx, conn)
	if err != nil {
		_ = conn.Close(context.Background())
		<-c.operations
		return ctx, nil, err
	}
	store, err := c.storeForSettings(ctx, settings, encrypted)
	if err != nil {
		_ = conn.Close(context.Background())
		<-c.operations
		return ctx, nil, err
	}
	backend := conn.PgConn().PID()
	var once sync.Once
	release := func() {
		once.Do(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = conn.Close(cleanup)
			<-c.operations
		})
	}
	return context.WithValue(ctx, storageLeaseContextKey{}, storageLease{store: store, revision: settings.Revision, backend: backend}), release, nil
}

func NewStorageController(ctx context.Context, pool *database.Pool, tokenSecret []byte, startup StorageConfig) (*StorageController, error) {
	if pool == nil || len(tokenSecret) < 32 {
		return nil, ErrInvalid
	}
	c := &StorageController{pool: pool, localDirectory: startup.LocalDirectory, allowInsecureLoopback: startup.AllowInsecureLoopback, operations: make(chan struct{}, maximumStorageOperations)}
	c.key = sha256.Sum256(append(append([]byte(nil), tokenSecret...), []byte("\x00file-s3-secret-v1")...))
	if err := c.seed(ctx, startup); err != nil {
		return nil, err
	}
	if err := c.refresh(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *StorageController) seed(ctx context.Context, startup StorageConfig) error {
	driver := startup.Driver
	if driver == "" {
		driver = "local"
	}
	var encrypted []byte
	var err error
	if startup.SecretKey != "" {
		encrypted, err = c.encrypt([]byte(startup.SecretKey))
		if err != nil {
			return err
		}
	}
	_, err = c.pool.Exec(ctx, `insert into pgbase_meta.file_storage_settings(singleton,driver,s3_bucket,s3_region,s3_endpoint,s3_access_key,s3_secret_ciphertext,s3_use_path_style) values(true,$1,$2,$3,$4,$5,$6,$7) on conflict(singleton) do nothing`, driver, startup.Bucket, startup.Region, startup.Endpoint, startup.AccessKey, encrypted, startup.UsePathStyle)
	return err
}

func (c *StorageController) Settings(ctx context.Context) (StorageSettings, error) {
	s, _, err := c.load(ctx)
	return s, err
}
func (c *StorageController) load(ctx context.Context) (StorageSettings, []byte, error) {
	return loadStorageSettings(ctx, c.pool)
}

type storageSettingsQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadStorageSettings(ctx context.Context, query storageSettingsQuerier) (StorageSettings, []byte, error) {
	var s StorageSettings
	var encrypted []byte
	err := query.QueryRow(ctx, `select driver,s3_bucket,s3_region,s3_endpoint,s3_access_key,s3_secret_ciphertext,s3_use_path_style,revision from pgbase_meta.file_storage_settings where singleton`).Scan(&s.Driver, &s.Bucket, &s.Region, &s.Endpoint, &s.AccessKey, &encrypted, &s.UsePathStyle, &s.Revision)
	s.SecretConfigured = len(encrypted) > 0
	return s, encrypted, err
}
func (c *StorageController) refresh(ctx context.Context) error {
	s, encrypted, err := c.load(ctx)
	if err != nil {
		return err
	}
	store, err := c.storeForSettings(ctx, s, encrypted)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.store = store
	c.revision = s.Revision
	c.mu.Unlock()
	return nil
}
func (c *StorageController) storeForSettings(ctx context.Context, s StorageSettings, encrypted []byte) (ObjectStore, error) {
	c.mu.RLock()
	if c.store != nil && c.revision == s.Revision {
		store := c.store
		c.mu.RUnlock()
		return store, nil
	}
	c.mu.RUnlock()
	return c.build(ctx, s, encrypted)
}
func (c *StorageController) build(ctx context.Context, s StorageSettings, encrypted []byte) (ObjectStore, error) {
	if err := c.validate(s); err != nil {
		return nil, err
	}
	if s.Driver == "local" {
		if c.localDirectory == "" {
			return nil, errors.New("local file storage is not configured")
		}
		return NewLocalStore(c.localDirectory)
	}
	secret, err := c.decrypt(encrypted)
	if err != nil {
		return nil, err
	}
	return NewS3Store(ctx, S3Config{Bucket: s.Bucket, Region: s.Region, Endpoint: s.Endpoint, AccessKey: s.AccessKey, SecretKey: string(secret), UsePathStyle: s.UsePathStyle})
}
func (c *StorageController) Put(ctx context.Context, key string, body io.Reader) error {
	if lease, ok := ctx.Value(storageLeaseContextKey{}).(storageLease); ok {
		return lease.store.Put(ctx, key, body)
	}
	leased, release, err := c.BeginStorageOperation(ctx)
	if err != nil {
		return err
	}
	defer release()
	return c.Put(leased, key, body)
}
func (c *StorageController) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if lease, ok := ctx.Value(storageLeaseContextKey{}).(storageLease); ok {
		return lease.store.Open(ctx, key)
	}
	leased, release, err := c.BeginStorageOperation(ctx)
	if err != nil {
		return nil, err
	}
	reader, err := c.Open(leased, key)
	if err != nil {
		release()
		return nil, err
	}
	return &leasedReader{ReadCloser: reader, release: release}, nil
}
func (c *StorageController) Delete(ctx context.Context, key string) error {
	if lease, ok := ctx.Value(storageLeaseContextKey{}).(storageLease); ok {
		return lease.store.Delete(ctx, key)
	}
	leased, release, err := c.BeginStorageOperation(ctx)
	if err != nil {
		return err
	}
	defer release()
	return c.Delete(leased, key)
}

// GuardStoragePublication serializes a metadata publication with destination
// transitions and rejects bytes written through a lease whose session was
// lost before the publication transaction began.
func (c *StorageController) GuardStoragePublication(ctx context.Context, tx pgx.Tx) error {
	lease, ok := ctx.Value(storageLeaseContextKey{}).(storageLease)
	if !ok {
		return ErrStorageConflict
	}
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock_shared($1)`, storageTransitionLock); err != nil {
		return err
	}
	var revision int64
	if err := tx.QueryRow(ctx, `select revision from pgbase_meta.file_storage_settings where singleton`).Scan(&revision); err != nil {
		return err
	}
	if revision != lease.revision {
		return ErrStorageConflict
	}
	return nil
}

type leasedReader struct {
	io.ReadCloser
	release func()
}

func (r *leasedReader) Close() error {
	err := r.ReadCloser.Close()
	r.release()
	return err
}

func (c *StorageController) Update(ctx context.Context, in StorageUpdate) (StorageSettings, error) {
	if in.Revision < 1 {
		return StorageSettings{}, ErrInvalid
	}
	var result StorageSettings
	err := c.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, storageTransitionLock); err != nil {
			return err
		}
		var old StorageSettings
		var oldSecret []byte
		if err := tx.QueryRow(ctx, `select driver,s3_bucket,s3_region,s3_endpoint,s3_access_key,s3_secret_ciphertext,s3_use_path_style,revision from pgbase_meta.file_storage_settings where singleton for update`).Scan(&old.Driver, &old.Bucket, &old.Region, &old.Endpoint, &old.AccessKey, &oldSecret, &old.UsePathStyle, &old.Revision); err != nil {
			return err
		}
		if old.Revision != in.Revision {
			return ErrStorageConflict
		}
		secret := oldSecret
		if in.ClearSecret {
			secret = nil
		} else if in.SecretKey != "" {
			var err error
			secret, err = c.encrypt([]byte(in.SecretKey))
			if err != nil {
				return err
			}
		}
		candidate := StorageSettings{Driver: in.Driver, Bucket: in.Bucket, Region: in.Region, Endpoint: in.Endpoint, AccessKey: in.AccessKey, UsePathStyle: in.UsePathStyle, SecretConfigured: len(secret) > 0, Revision: old.Revision + 1}
		if err := c.validate(candidate); err != nil {
			return err
		}
		if location(old) != location(candidate) {
			var count int
			if err := tx.QueryRow(ctx, `select (select count(*) from pgbase_meta.record_files)+(select count(*) from pgbase_meta.record_file_variants)+(select count(*) from pgbase_meta.file_object_deletions)`).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return ErrStorageConflict
			}
		}
		candidateStore, err := c.build(ctx, candidate, secret)
		if err != nil {
			return ErrInvalid
		}
		if candidate.Driver == "s3" {
			if err := probeStore(ctx, candidateStore); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `update pgbase_meta.file_storage_settings set driver=$1,s3_bucket=$2,s3_region=$3,s3_endpoint=$4,s3_access_key=$5,s3_secret_ciphertext=$6,s3_use_path_style=$7,revision=revision+1,updated_at=CURRENT_TIMESTAMP where singleton`, candidate.Driver, candidate.Bucket, candidate.Region, candidate.Endpoint, candidate.AccessKey, secret, candidate.UsePathStyle)
		result = candidate
		return err
	})
	if err != nil {
		return StorageSettings{}, err
	}
	c.mu.Lock()
	c.revision = 0
	c.mu.Unlock()
	err = c.refresh(ctx)
	return result, err
}
func location(s StorageSettings) string {
	return strings.Join([]string{s.Driver, s.Bucket, s.Region, s.Endpoint, fmt.Sprint(s.UsePathStyle)}, "\x00")
}
func (c *StorageController) validate(s StorageSettings) error {
	if s.Driver == "local" {
		if c.localDirectory == "" {
			return ErrInvalid
		}
		return nil
	}
	if s.Driver != "s3" || s.Bucket == "" || s.Region == "" || (s.AccessKey == "") != !s.SecretConfigured {
		return ErrInvalid
	}
	if s.Endpoint == "" {
		return nil
	}
	u, err := url.Parse(s.Endpoint)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || u.Hostname() == "" {
		return ErrInvalid
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || !c.allowInsecureLoopback || ip == nil || !ip.IsLoopback() {
			return ErrInvalid
		}
	}
	if len(s.Bucket) > 255 || len(s.Region) > 128 || len(s.Endpoint) > 2048 || len(s.AccessKey) > 512 {
		return ErrInvalid
	}
	return nil
}
func (c *StorageController) Test(ctx context.Context) error {
	s, encrypted, err := c.load(ctx)
	if err != nil {
		return err
	}
	if s.Driver != "s3" {
		return ErrInvalid
	}
	store, err := c.build(ctx, s, encrypted)
	if err != nil {
		return ErrInvalid
	}
	return probeStore(ctx, store)
}

func probeStore(ctx context.Context, store ObjectStore) error {
	key := "objects/" + uuid.NewString()
	marker := make([]byte, 32)
	if _, err := rand.Read(marker); err != nil {
		return err
	}
	put := false
	defer func() {
		if put {
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = store.Delete(cleanup, key)
		}
	}()
	if err := store.Put(ctx, key, bytes.NewReader(marker)); err != nil {
		return err
	}
	put = true
	r, err := store.Open(ctx, key)
	if err != nil {
		return err
	}
	got, readErr := io.ReadAll(io.LimitReader(r, 33))
	closeErr := r.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if subtle.ConstantTimeCompare(got, marker) != 1 {
		return errors.New("S3 test content mismatch")
	}
	if err = store.Delete(ctx, key); err != nil {
		return err
	}
	put = false
	return nil
}

func (c *StorageController) encrypt(value []byte) ([]byte, error) {
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, value, []byte("file-s3-secret-v1")), nil
}
func (c *StorageController) decrypt(value []byte) ([]byte, error) {
	if len(value) == 0 {
		return nil, nil
	}
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(value) < aead.NonceSize() {
		return nil, ErrInvalid
	}
	return aead.Open(nil, value[:aead.NonceSize()], value[aead.NonceSize():], []byte("file-s3-secret-v1"))
}
