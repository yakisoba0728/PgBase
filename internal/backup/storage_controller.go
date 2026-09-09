package backup

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
)

var ErrStorageConflict = errors.New("backup storage settings conflict")
var ErrInvalidStorage = errors.New("invalid backup storage settings")

const backupStorageLock int64 = 0x7067626173650037

type StorageConfig struct {
	Driver, Bucket, Prefix, Region, Endpoint, AccessKey, SecretKey string
	UsePathStyle                                                   bool
	Retention                                                      int
}
type StorageSettings struct {
	Driver           string `json:"driver"`
	Bucket           string `json:"bucket"`
	Prefix           string `json:"prefix"`
	Region           string `json:"region"`
	Endpoint         string `json:"endpoint"`
	AccessKey        string `json:"accessKey"`
	UsePathStyle     bool   `json:"usePathStyle"`
	Retention        int    `json:"retention"`
	SecretConfigured bool   `json:"secretConfigured"`
	Revision         int64  `json:"revision"`
}
type StorageUpdate struct {
	StorageConfig
	ClearSecret bool
	Revision    int64
}

type StorageController struct {
	pool       *database.Pool
	local      Repository
	key        [32]byte
	mu         sync.RWMutex
	revision   int64
	repository Repository
	retention  int
	sessions   chan struct{}
}

func NewStorageController(ctx context.Context, pool *database.Pool, tokenSecret []byte, local Repository, startup StorageConfig) (*StorageController, error) {
	if pool == nil || local == nil || len(tokenSecret) < 32 {
		return nil, ErrInvalidStorage
	}
	c := &StorageController{pool: pool, local: local, sessions: make(chan struct{}, 64)}
	c.key = sha256.Sum256(append(append([]byte(nil), tokenSecret...), []byte("\x00backup-s3-secret-v1")...))
	if err := c.seed(ctx, startup); err != nil {
		return nil, err
	}
	if _, err := c.current(ctx); err != nil {
		return nil, err
	}
	if err := c.recoverPending(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *StorageController) recoverPending(ctx context.Context) error {
	rows, err := c.pool.Query(ctx, `select backup_id::text,settings_revision from pgbase_meta.backup_storage_operations where status='pending' order by backup_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type pending struct {
		id       string
		revision int64
	}
	items := []pending{}
	for rows.Next() {
		var item pending
		if err := rows.Scan(&item.id, &item.revision); err != nil {
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	for _, item := range items {
		leaseCtx, repository, conn, release, err := c.lease(ctx, item.id)
		if err != nil {
			return err
		}
		var revision int64
		var status string
		err = conn.QueryRow(leaseCtx, `select settings_revision,status from pgbase_meta.backup_storage_operations where backup_id=$1`, item.id).Scan(&revision, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			release()
			continue
		}
		if err != nil {
			release()
			return err
		}
		if status != "pending" {
			release()
			continue
		}
		settings, _, err := c.loadFrom(leaseCtx, conn)
		if err != nil {
			release()
			return err
		}
		if revision != item.revision || revision != settings.Revision {
			release()
			return ErrStorageConflict
		}
		_, getErr := repository.Get(leaseCtx, item.id)
		if getErr == nil {
			reader, openErr := repository.Open(leaseCtx, item.id)
			if openErr == nil {
				openErr = reader.Close()
			}
			getErr = openErr
		}
		if getErr == nil {
			if _, err := conn.Exec(leaseCtx, `update pgbase_meta.backup_storage_operations set status='committed',updated_at=now() where backup_id=$1 and settings_revision=$2 and status='pending'`, item.id, revision); err != nil {
				release()
				return err
			}
			release()
			continue
		} else if !errors.Is(getErr, ErrNotFound) {
			release()
			return getErr
		}
		// Absence is not proof that a publisher whose database session was lost
		// has stopped writing to the immutable archive keys. Preserve the durable
		// fence until an administrator explicitly deletes this backup ID.
		release()
	}
	return nil
}
func (c *StorageController) seed(ctx context.Context, in StorageConfig) error {
	driver := in.Driver
	if driver == "" {
		driver = "local"
	}
	in.Driver = driver
	if err := validateStorageConfig(in); err != nil {
		return err
	}
	secret, err := c.encrypt([]byte(in.SecretKey))
	if err != nil {
		return err
	}
	_, err = c.pool.Exec(ctx, `insert into pgbase_meta.backup_storage_settings(singleton,driver,s3_bucket,s3_prefix,s3_region,s3_endpoint,s3_access_key,s3_secret_ciphertext,s3_use_path_style,retention) values(true,$1,$2,$3,$4,$5,$6,$7,$8,$9) on conflict(singleton) do nothing`, driver, in.Bucket, in.Prefix, in.Region, in.Endpoint, in.AccessKey, secret, in.UsePathStyle, in.Retention)
	return err
}
func (c *StorageController) loadFrom(ctx context.Context, db database.DBTX) (StorageSettings, []byte, error) {
	var s StorageSettings
	var encrypted []byte
	err := db.QueryRow(ctx, `select driver,s3_bucket,s3_prefix,s3_region,s3_endpoint,s3_access_key,s3_secret_ciphertext,s3_use_path_style,retention,revision from pgbase_meta.backup_storage_settings where singleton`).Scan(&s.Driver, &s.Bucket, &s.Prefix, &s.Region, &s.Endpoint, &s.AccessKey, &encrypted, &s.UsePathStyle, &s.Retention, &s.Revision)
	s.SecretConfigured = len(encrypted) > 0
	return s, encrypted, err
}
func (c *StorageController) load(ctx context.Context) (StorageSettings, []byte, error) {
	c.mu.RLock()
	pool := c.pool
	c.mu.RUnlock()
	return c.loadFrom(ctx, pool)
}
func (c *StorageController) Settings(ctx context.Context) (StorageSettings, error) {
	s, _, err := c.load(ctx)
	return s, err
}
func (c *StorageController) current(ctx context.Context) (Repository, error) {
	c.mu.RLock()
	pool := c.pool
	c.mu.RUnlock()
	return c.currentFrom(ctx, pool)
}
func (c *StorageController) currentFrom(ctx context.Context, db database.DBTX) (Repository, error) {
	s, encrypted, err := c.loadFrom(ctx, db)
	if err != nil {
		return nil, err
	}
	c.mu.RLock()
	if c.repository != nil && c.revision == s.Revision {
		r := c.repository
		c.mu.RUnlock()
		return r, nil
	}
	c.mu.RUnlock()
	r, err := c.build(ctx, s, encrypted)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.repository = r
	c.revision = s.Revision
	c.retention = s.Retention
	c.mu.Unlock()
	return r, nil
}
func (c *StorageController) build(ctx context.Context, s StorageSettings, encrypted []byte) (Repository, error) {
	if s.Driver == "local" {
		return c.local, nil
	}
	if s.Driver != "s3" || s.Bucket == "" || s.Region == "" {
		return nil, ErrInvalidStorage
	}
	secret, err := c.decrypt(encrypted)
	if err != nil {
		return nil, err
	}
	return NewS3Repository(ctx, S3RepositoryOptions{Bucket: s.Bucket, Prefix: s.Prefix, Region: s.Region, Endpoint: s.Endpoint, AccessKey: s.AccessKey, SecretKey: string(secret), UsePathStyle: s.UsePathStyle})
}
func (c *StorageController) lease(ctx context.Context, id ...string) (context.Context, Repository, *pgx.Conn, func(), error) {
	c.mu.RLock()
	pool := c.pool
	c.mu.RUnlock()
	select {
	case c.sessions <- struct{}{}:
	case <-ctx.Done():
		return ctx, nil, nil, nil, ctx.Err()
	}
	conn, err := pool.OpenCoordinationSession(ctx)
	if err != nil {
		<-c.sessions
		return ctx, nil, nil, nil, err
	}
	if _, err = conn.Exec(ctx, `select pg_advisory_lock_shared($1)`, backupStorageLock); err != nil {
		_ = conn.Close(context.Background())
		<-c.sessions
		return ctx, nil, nil, nil, err
	}
	var idKey int64
	if len(id) > 0 {
		value := sha256.Sum256([]byte(id[0]))
		idKey = int64(uint64(value[0])<<56 | uint64(value[1])<<48 | uint64(value[2])<<40 | uint64(value[3])<<32 | uint64(value[4])<<24 | uint64(value[5])<<16 | uint64(value[6])<<8 | uint64(value[7]))
		if _, err = conn.Exec(ctx, `select pg_advisory_lock($1)`, idKey); err != nil {
			_ = conn.Close(context.Background())
			<-c.sessions
			return ctx, nil, nil, nil, err
		}
	}
	r, err := c.currentFrom(ctx, conn)
	if err != nil {
		_ = conn.Close(context.Background())
		<-c.sessions
		return ctx, nil, nil, nil, err
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			_ = conn.Close(context.Background())
			<-c.sessions
		})
	}
	return ctx, r, conn, release, nil
}
func (c *StorageController) Publish(ctx context.Context, e Entry, b io.Reader) error {
	ctx, r, conn, release, err := c.lease(ctx, e.ID)
	if err != nil {
		return err
	}
	defer release()
	settings, _, err := c.loadFrom(ctx, conn)
	if err != nil {
		return err
	}
	if _, err = conn.Exec(ctx, `insert into pgbase_meta.backup_storage_operations(backup_id,settings_revision,status) values($1,$2,'pending') on conflict(backup_id) do nothing`, e.ID, settings.Revision); err != nil {
		return err
	}
	if err = r.Publish(ctx, e, b); err != nil {
		return err
	}
	_, err = conn.Exec(ctx, `update pgbase_meta.backup_storage_operations set status='committed',updated_at=now() where backup_id=$1 and settings_revision=$2 and status='pending'`, e.ID, settings.Revision)
	return err
}
func (c *StorageController) Open(ctx context.Context, id string) (io.ReadCloser, error) {
	ctx, r, _, release, err := c.lease(ctx)
	if err != nil {
		return nil, err
	}
	reader, err := r.Open(ctx, id)
	if err != nil {
		release()
		return nil, err
	}
	return &repositoryLeasedReader{ReadCloser: reader, release: release}, nil
}

type repositoryLeasedReader struct {
	io.ReadCloser
	release func()
}

func (r *repositoryLeasedReader) Close() error {
	err := r.ReadCloser.Close()
	r.release()
	return err
}

func (c *StorageController) List(ctx context.Context) ([]Entry, error) {
	ctx, r, _, release, err := c.lease(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return r.List(ctx)
}
func (c *StorageController) Get(ctx context.Context, id string) (Entry, error) {
	ctx, r, _, release, err := c.lease(ctx)
	if err != nil {
		return Entry{}, err
	}
	defer release()
	return r.Get(ctx, id)
}
func (c *StorageController) Delete(ctx context.Context, id string) error {
	ctx, r, conn, release, err := c.lease(ctx, id)
	if err != nil {
		return err
	}
	defer release()
	var pending bool
	if err := conn.QueryRow(ctx, `select exists(select 1 from pgbase_meta.backup_storage_operations where backup_id=$1 and status='pending')`, id).Scan(&pending); err != nil {
		return err
	}
	if err := r.Delete(ctx, id); err != nil {
		if !pending || !errors.Is(err, ErrNotFound) {
			return err
		}
		discard, ok := r.(interface {
			Discard(context.Context, string) error
		})
		if !ok {
			return ErrStorageConflict
		}
		if err := discard.Discard(ctx, id); err != nil {
			return err
		}
	}
	_, err = conn.Exec(ctx, `delete from pgbase_meta.backup_storage_operations where backup_id=$1`, id)
	return err
}
func (c *StorageController) Retention() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.retention
}

func (c *StorageController) ValidatePool(ctx context.Context, pool *database.Pool) error {
	s, encrypted, err := c.loadFrom(ctx, pool)
	if err != nil {
		return err
	}
	_, err = c.build(ctx, s, encrypted)
	return err
}
func (c *StorageController) RebindPool(pool *database.Pool) {
	c.mu.Lock()
	c.pool = pool
	c.repository = nil
	c.revision = 0
	c.mu.Unlock()
}
func (c *StorageController) Update(ctx context.Context, in StorageUpdate) (StorageSettings, error) {
	var result StorageSettings
	c.mu.RLock()
	pool := c.pool
	c.mu.RUnlock()
	err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, backupStorageLock); err != nil {
			return err
		}
		old, encrypted, err := c.loadFrom(ctx, tx)
		if err != nil {
			return err
		}
		if old.Revision != in.Revision {
			return ErrStorageConflict
		}
		var pending int
		if err := tx.QueryRow(ctx, `select count(*) from pgbase_meta.backup_storage_operations where status='pending'`).Scan(&pending); err != nil {
			return err
		}
		if pending != 0 {
			return ErrStorageConflict
		}
		oldEncrypted := append([]byte(nil), encrypted...)
		if in.ClearSecret {
			encrypted = nil
		} else if in.SecretKey != "" {
			encrypted, err = c.encrypt([]byte(in.SecretKey))
			if err != nil {
				return err
			}
		}
		candidate := StorageSettings{Driver: in.Driver, Bucket: in.Bucket, Prefix: in.Prefix, Region: in.Region, Endpoint: in.Endpoint, AccessKey: in.AccessKey, UsePathStyle: in.UsePathStyle, Retention: in.Retention, SecretConfigured: len(encrypted) > 0, Revision: old.Revision + 1}
		if err := validateStorageConfig(StorageConfig{Driver: candidate.Driver, Bucket: candidate.Bucket, Prefix: candidate.Prefix, Region: candidate.Region, Endpoint: candidate.Endpoint, AccessKey: candidate.AccessKey, SecretKey: func() string {
			if candidate.SecretConfigured {
				return "configured"
			}
			return ""
		}(), UsePathStyle: candidate.UsePathStyle, Retention: candidate.Retention}); err != nil {
			return err
		}
		repository, err := c.build(ctx, candidate, encrypted)
		if err != nil {
			return ErrInvalidStorage
		}
		if locationChanged(old, candidate) {
			oldRepository, buildErr := c.build(ctx, old, oldEncrypted)
			if buildErr != nil {
				return buildErr
			}
			entries, err := oldRepository.List(ctx)
			if err != nil {
				return err
			}
			if len(entries) != 0 {
				return ErrStorageConflict
			}
		}
		if candidate.Driver == "s3" {
			if err := probeRepository(ctx, repository); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `update pgbase_meta.backup_storage_settings set driver=$1,s3_bucket=$2,s3_prefix=$3,s3_region=$4,s3_endpoint=$5,s3_access_key=$6,s3_secret_ciphertext=$7,s3_use_path_style=$8,retention=$9,revision=revision+1,updated_at=now() where singleton`, candidate.Driver, candidate.Bucket, candidate.Prefix, candidate.Region, candidate.Endpoint, candidate.AccessKey, encrypted, candidate.UsePathStyle, candidate.Retention)
		result = candidate
		return err
	})
	if err != nil {
		return StorageSettings{}, err
	}
	c.mu.Lock()
	c.revision = 0
	c.mu.Unlock()
	_, err = c.current(ctx)
	return result, err
}
func validateStorageConfig(in StorageConfig) error {
	if in.Retention < 0 || int64(in.Retention) > 2147483647 {
		return ErrInvalidStorage
	}
	if in.Driver != "local" && in.Driver != "s3" {
		return ErrInvalidStorage
	}
	if in.Driver == "s3" && (in.Bucket == "" || in.Region == "" || (in.AccessKey == "") != (in.SecretKey == "")) {
		return ErrInvalidStorage
	}
	return nil
}
func locationChanged(a, b StorageSettings) bool {
	return a.Driver != b.Driver || a.Bucket != b.Bucket || a.Prefix != b.Prefix || a.Region != b.Region || a.Endpoint != b.Endpoint || a.UsePathStyle != b.UsePathStyle
}
func (c *StorageController) Test(ctx context.Context) error {
	ctx, r, _, release, err := c.lease(ctx)
	if err != nil {
		return err
	}
	defer release()
	return probeRepository(ctx, r)
}
func probeRepository(ctx context.Context, r Repository) (resultErr error) {
	id := uuid.NewString()
	body := []byte("pgbase-backup-storage-probe")
	digest := sha256.Sum256(body)
	entry := Entry{ID: id, CreatedAt: time.Now().UTC(), Size: int64(len(body)), PostgreSQLMajor: 18, SHA256: fmt.Sprintf("%x", digest[:]), StorageFormat: s3StorageFormat}
	if err := r.Publish(ctx, entry, bytes.NewReader(body)); err != nil {
		return err
	}
	defer func() {
		if resultErr == nil {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if discard, ok := r.(interface {
			Discard(context.Context, string) error
		}); ok {
			_ = discard.Discard(cleanup, id)
		} else {
			_ = r.Delete(cleanup, id)
		}
	}()
	reader, err := r.Open(ctx, id)
	if err != nil {
		return err
	}
	got, readErr := io.ReadAll(io.LimitReader(reader, 1024))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(got, body) {
		return errors.New("backup storage probe mismatch")
	}
	return r.Delete(ctx, id)
}
func (c *StorageController) encrypt(value []byte) ([]byte, error) {
	if len(value) == 0 {
		return nil, nil
	}
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, value, nil), nil
}
func (c *StorageController) decrypt(value []byte) ([]byte, error) {
	if len(value) == 0 {
		return nil, nil
	}
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(value) < gcm.NonceSize() {
		return nil, ErrInvalidStorage
	}
	return gcm.Open(nil, value[:gcm.NonceSize()], value[gcm.NonceSize():], nil)
}
