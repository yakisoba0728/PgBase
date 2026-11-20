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
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/validation"
)

var ErrStorageConflict = errors.New("backup storage settings conflict")
var ErrInvalidStorage = errors.New("invalid backup storage settings")
var errBackupOperationActive = errors.New("backup publication is active")
var errRetentionChanged = errors.New("backup retention settings changed")

const backupStorageLock int64 = 0x7067626173650037
const backupPruneLock int64 = 0x7067626173650072

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
	repository, err := c.current(ctx)
	if err != nil {
		return nil, err
	}
	// Remote object storage must not gate application startup. Its pending
	// publications are reconciled by the application's recovery worker.
	if _, remote := repository.(*S3Repository); !remote {
		if err := c.recoverPending(ctx, false); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// RecoverPending retries interrupted publications without waiting for healthy
// publishers that still hold their operation lease.
func (c *StorageController) RecoverPending(ctx context.Context) error {
	return c.recoverPending(ctx, true)
}

func (c *StorageController) recoverPending(ctx context.Context, try bool) error {
	c.mu.RLock()
	pool := c.pool
	c.mu.RUnlock()
	rows, err := pool.Query(ctx, `select backup_id::text from pgbase_meta.backup_storage_operations where status='pending' order by backup_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	var firstErr error
	for _, id := range ids {
		if err := c.recoverPublication(ctx, pool, id, try); err != nil {
			if !try {
				return err
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("reconcile backup publication %s: %w", id, err)
			}
			if ctx.Err() != nil {
				break
			}
		}
	}
	return firstErr
}

func (c *StorageController) recoverPublication(ctx context.Context, pool *database.Pool, id string, try bool) error {
	ctx, repository, conn, release, err := c.leaseFrom(ctx, pool, try, id)
	if errors.Is(err, errBackupOperationActive) {
		return nil
	}
	if err != nil {
		return err
	}
	defer release()
	var revision int64
	err = conn.QueryRow(ctx, `select settings_revision from pgbase_meta.backup_storage_operations where backup_id=$1 and status='pending'`, id).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	settings, _, err := c.loadFrom(ctx, conn)
	if err != nil {
		return err
	}
	if revision != settings.Revision {
		return ErrStorageConflict
	}
	_, err = repository.Get(ctx, id)
	if err == nil {
		var reader io.ReadCloser
		reader, err = repository.Open(ctx, id)
		if err == nil {
			err = reader.Close()
		}
	}
	// Absence is not proof that a publisher whose database session was lost
	// has stopped writing to the immutable archive keys. Preserve its fence.
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = conn.Exec(ctx, `update pgbase_meta.backup_storage_operations set status='committed',updated_at=now() where backup_id=$1 and settings_revision=$2 and status='pending'`, id, revision)
	return err
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
	return c.currentFrom(ctx, pool, pool)
}
func (c *StorageController) currentFrom(ctx context.Context, pool *database.Pool, db database.DBTX) (Repository, error) {
	s, encrypted, err := c.loadFrom(ctx, db)
	if err != nil {
		return nil, err
	}
	c.mu.RLock()
	if c.pool == pool && c.repository != nil && c.revision == s.Revision {
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
	if c.pool == pool {
		c.repository = r
		c.revision = s.Revision
	}
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
	return c.leaseFrom(ctx, pool, false, id...)
}

func (c *StorageController) leaseFrom(ctx context.Context, pool *database.Pool, try bool, id ...string) (context.Context, Repository, *pgx.Conn, func(), error) {
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
	release := func() {
		_ = conn.Close(context.Background())
		<-c.sessions
	}
	if _, err = conn.Exec(ctx, `select pg_advisory_lock_shared($1)`, backupStorageLock); err != nil {
		release()
		return ctx, nil, nil, nil, err
	}
	if len(id) > 0 {
		value := sha256.Sum256([]byte(id[0]))
		idKey := int64(uint64(value[0])<<56 | uint64(value[1])<<48 | uint64(value[2])<<40 | uint64(value[3])<<32 | uint64(value[4])<<24 | uint64(value[5])<<16 | uint64(value[6])<<8 | uint64(value[7]))
		if try {
			var active bool
			if err = conn.QueryRow(ctx, `select not pg_try_advisory_lock($1)`, idKey).Scan(&active); err == nil && active {
				err = errBackupOperationActive
			}
		} else {
			_, err = conn.Exec(ctx, `select pg_advisory_lock($1)`, idKey)
		}
		if err != nil {
			release()
			return ctx, nil, nil, nil, err
		}
	}
	r, err := c.currentFrom(ctx, pool, conn)
	if err != nil {
		release()
		return ctx, nil, nil, nil, err
	}
	return ctx, r, conn, sync.OnceFunc(release), nil
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
	return deleteBackup(ctx, r, conn, id)
}

func (c *StorageController) tryBeginPrune(ctx context.Context) (func(), error) {
	c.mu.RLock()
	pool := c.pool
	c.mu.RUnlock()
	conn, err := pool.OpenCoordinationSession(ctx)
	if err != nil {
		return nil, err
	}
	release := func() {
		cleanup, cancel := database.BoundedCleanupContext(ctx)
		defer cancel()
		_ = conn.Close(cleanup)
	}
	var admitted bool
	if err := conn.QueryRow(ctx, `select pg_try_advisory_lock($1)`, backupPruneLock).Scan(&admitted); err != nil || !admitted {
		release()
		return nil, err
	}
	return release, nil
}

func (c *StorageController) deleteForRetention(ctx context.Context, id string, storageRevision int64, pinRuntimeSettings func(context.Context, pgx.Tx) (bool, error)) error {
	ctx, r, conn, release, err := c.lease(ctx, id)
	if err != nil {
		return err
	}
	defer release()
	// The storage lease blocks settings writes. Hold the runtime settings row
	// too, so neither policy can change between admission and object removal.
	settings, _, err := c.loadFrom(ctx, conn)
	if err != nil {
		return err
	}
	if settings.Revision != storageRevision {
		return errRetentionChanged
	}
	if pinRuntimeSettings == nil {
		return deleteBackup(ctx, r, conn, id)
	}
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		current, err := pinRuntimeSettings(ctx, tx)
		if err != nil {
			return err
		}
		if !current {
			return errRetentionChanged
		}
		return deleteBackup(ctx, r, tx, id)
	})
}

func deleteBackup(ctx context.Context, r Repository, conn database.DBTX, id string) error {
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
	_, err := conn.Exec(ctx, `delete from pgbase_meta.backup_storage_operations where backup_id=$1`, id)
	return err
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
		var pending, revisionMismatch bool
		if err := tx.QueryRow(ctx, `select
			exists(select 1 from pgbase_meta.backup_storage_operations where status='pending'),
			exists(select 1 from pgbase_meta.backup_storage_operations where status='pending' and settings_revision<>$1)`,
			old.Revision).Scan(&pending, &revisionMismatch); err != nil {
			return err
		}
		if revisionMismatch {
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
			if pending {
				return ErrStorageConflict
			}
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
		if err != nil {
			return err
		}
		if pending {
			// A credential repair keeps the existing namespace and its fences.
			if _, err = tx.Exec(ctx, `update pgbase_meta.backup_storage_operations set settings_revision=$1 where status='pending' and settings_revision=$2`, candidate.Revision, old.Revision); err != nil {
				return err
			}
		}
		result = candidate
		return nil
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
	field := ""
	if in.Driver == "s3" && in.Endpoint != "" {
		endpoint, err := url.Parse(in.Endpoint)
		if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
			return errors.Join(ErrInvalidStorage, &validation.Error{Details: []validation.Detail{{Field: "endpoint", Code: "invalid"}}})
		}
	}
	switch {
	case in.Retention < 0 || int64(in.Retention) > 2147483647:
		field = "retention"
	case in.Driver != "local" && in.Driver != "s3":
		field = "driver"
	case in.Driver == "s3" && in.Bucket == "":
		field = "bucket"
	case in.Driver == "s3" && in.Region == "":
		field = "region"
	case in.Driver == "s3" && (in.AccessKey == "") != (in.SecretKey == ""):
		field = "secretKey"
	}
	if field != "" {
		return errors.Join(ErrInvalidStorage, &validation.Error{Details: []validation.Detail{{Field: field, Code: "invalid"}}})
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
	if err := probeRepository(ctx, r); err != nil {
		return err
	}
	release()
	return c.RecoverPending(ctx)
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
