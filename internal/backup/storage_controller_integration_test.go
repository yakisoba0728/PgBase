//go:build integration

package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

type blockingRepository struct{ entered, release chan struct{} }

func (r *blockingRepository) Publish(ctx context.Context, _ Entry, _ io.Reader) error {
	close(r.entered)
	select {
	case <-r.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (*blockingRepository) Open(context.Context, string) (io.ReadCloser, error) {
	return nil, ErrNotFound
}
func (*blockingRepository) List(context.Context) ([]Entry, error)      { return nil, nil }
func (*blockingRepository) Get(context.Context, string) (Entry, error) { return Entry{}, ErrNotFound }
func (*blockingRepository) Delete(context.Context, string) error       { return ErrNotFound }
func (*blockingRepository) Discard(context.Context, string) error      { return nil }

func TestBackupStorageControllerS3SettingsProbeAndTransitionFence(t *testing.T) {
	endpoint := os.Getenv("PGBASE_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("PGBASE_TEST_S3_ENDPOINT is not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	controller, err := NewStorageController(ctx, pool, bytes.Repeat([]byte("k"), 32), NewLocalRepository(t.TempDir()), StorageConfig{Driver: "local"})
	if err != nil {
		t.Fatal(err)
	}
	prefix := "backup-controller-tests/" + uuid.NewString()
	settings, err := controller.Update(ctx, StorageUpdate{StorageConfig: StorageConfig{Driver: "s3", Bucket: "pgbase-test", Prefix: prefix, Region: "us-east-1", Endpoint: endpoint, AccessKey: "pgbase-test-access", SecretKey: "pgbase-test-secret-0123456789", UsePathStyle: true, Retention: 2}, Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	if settings.SecretConfigured != true || settings.Retention != 2 {
		t.Fatalf("settings=%+v", settings)
	}
	loaded, err := controller.Settings(ctx)
	if err != nil || !loaded.SecretConfigured {
		t.Fatalf("Settings()=%+v,%v", loaded, err)
	}
	if err := controller.Test(ctx); err != nil {
		t.Fatal(err)
	}
	if entries, err := controller.List(ctx); err != nil || len(entries) != 0 {
		t.Fatalf("storage probe left visible archive entries=%#v err=%v", entries, err)
	}
	if _, err := controller.Update(ctx, StorageUpdate{StorageConfig: StorageConfig{Driver: "s3", Bucket: settings.Bucket, Prefix: settings.Prefix, Region: settings.Region, Endpoint: settings.Endpoint, AccessKey: settings.AccessKey, UsePathStyle: true, Retention: int(int64(2147483647) + 1)}, Revision: settings.Revision}); !errors.Is(err, ErrInvalidStorage) {
		t.Fatalf("oversized retention=%v", err)
	}
	if _, err := controller.Update(ctx, StorageUpdate{StorageConfig: StorageConfig{Driver: "s3", Bucket: settings.Bucket, Prefix: settings.Prefix, Region: settings.Region, Endpoint: settings.Endpoint, AccessKey: settings.AccessKey, UsePathStyle: true}, ClearSecret: true, Revision: settings.Revision}); !errors.Is(err, ErrInvalidStorage) {
		t.Fatalf("unpaired cleared secret=%v", err)
	}
	id := uuid.NewString()
	body := []byte("archive")
	digest := sha256.Sum256(body)
	entry := Entry{ID: id, CreatedAt: time.Now().UTC(), Size: int64(len(body)), PostgreSQLMajor: 18, SHA256: fmt.Sprintf("%x", digest[:])}
	if err := controller.Publish(ctx, entry, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	defer controller.Delete(context.Background(), id)
	reader, err := controller.Open(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err = controller.Update(blocked, StorageUpdate{StorageConfig: StorageConfig{Driver: "local"}, Revision: settings.Revision})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("update during open=%v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Update(ctx, StorageUpdate{StorageConfig: StorageConfig{Driver: "local"}, Revision: settings.Revision}); !errors.Is(err, ErrStorageConflict) {
		t.Fatalf("destination change with archive=%v", err)
	}
}

func TestLostPublishLeaseLeavesDurableFenceAgainstTransition(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	repository := &blockingRepository{entered: make(chan struct{}), release: make(chan struct{})}
	controller, err := NewStorageController(ctx, pool, bytes.Repeat([]byte("k"), 32), repository, StorageConfig{Driver: "local"})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	published := make(chan error, 1)
	go func() { published <- controller.Publish(ctx, Entry{ID: id}, bytes.NewReader(nil)) }()
	<-repository.entered
	high := uint32(uint64(backupStorageLock) >> 32)
	low := uint32(uint64(backupStorageLock) & 0xffffffff)
	var terminated bool
	if err := pool.QueryRow(ctx, `select coalesce(bool_or(pg_terminate_backend(activity.pid)),false) from pg_locks locks join pg_stat_activity activity on activity.pid=locks.pid where locks.locktype='advisory' and locks.classid=$1 and locks.objid=$2 and activity.pid<>pg_backend_pid()`, high, low).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate lease=%t,%v", terminated, err)
	}
	settings, err := controller.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = controller.Update(ctx, StorageUpdate{StorageConfig: StorageConfig{Driver: "s3", Bucket: "other", Region: "us-east-1"}, Revision: settings.Revision})
	if !errors.Is(err, ErrStorageConflict) {
		t.Fatalf("transition after lease loss=%v", err)
	}
	recovered, err := NewStorageController(ctx, pool, bytes.Repeat([]byte("k"), 32), repository, StorageConfig{Driver: "local"})
	if err != nil {
		t.Fatalf("restart with incomplete publication fence: %v", err)
	}
	var pending int
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.backup_storage_operations where backup_id=$1 and status='pending'`, id).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("restart retained pending fence=%d err=%v", pending, err)
	}
	close(repository.release)
	if err := <-published; err == nil {
		t.Fatal("publish reported success after lease loss")
	}
	if err := recovered.Delete(ctx, id); err != nil {
		t.Fatalf("explicitly discard uncertain publication: %v", err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.backup_storage_operations where backup_id=$1`, id).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("explicit discard retained fence=%d err=%v", pending, err)
	}
}

func TestConcurrentControllerRecoveryWaitsForHealthyPublisher(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	repository := &blockingRepository{entered: make(chan struct{}), release: make(chan struct{})}
	controller, err := NewStorageController(ctx, pool, bytes.Repeat([]byte("k"), 32), repository, StorageConfig{Driver: "local"})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	published := make(chan error, 1)
	go func() { published <- controller.Publish(ctx, Entry{ID: id}, bytes.NewReader(nil)) }()
	<-repository.entered
	recovered := make(chan error, 1)
	go func() {
		_, recoveryErr := NewStorageController(ctx, pool, bytes.Repeat([]byte("k"), 32), repository, StorageConfig{Driver: "local"})
		recovered <- recoveryErr
	}()
	select {
	case err := <-recovered:
		t.Fatalf("recovery did not wait for active publisher: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(repository.release)
	if err := <-published; err != nil {
		t.Fatal(err)
	}
	if err := <-recovered; err != nil {
		t.Fatal(err)
	}
	var status string
	if err := pool.QueryRow(ctx, `select status from pgbase_meta.backup_storage_operations where backup_id=$1`, id).Scan(&status); err != nil || status != "committed" {
		t.Fatalf("recovered operation status=%q err=%v", status, err)
	}
}
