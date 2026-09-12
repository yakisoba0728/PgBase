//go:build integration

package filestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestObjectMigrationCancellationWaitsForOwnedCopy(t *testing.T) {
	for _, phase := range []string{"before_put", "post_put_preledger"} {
		t.Run(phase, func(t *testing.T) {
			ctx, pool, controller, migrationID, key, source, destination := newLocalMigrationFixture(t, nil)
			blocking := &migrationBlockingStore{migrationMemoryStore: destination, phase: phase, reached: make(chan struct{}), release: make(chan struct{})}
			service := NewObjectMigrationService(controller)
			service.buildStore = migrationStoreFactory(source, blocking)
			copied := make(chan error, 1)
			go func() {
				_, err := service.copyOne(ctx, migrationID)
				copied <- err
			}()
			select {
			case <-blocking.reached:
			case <-time.After(time.Second):
				t.Fatal("copy did not reach the requested interlock")
			}
			if err := service.Cancel(ctx, migrationID); err != nil {
				t.Fatal(err)
			}
			status, err := service.Status(ctx, migrationID)
			if err != nil || status.Status != "canceling" {
				t.Fatalf("status while copy owns intent=%#v err=%v", status, err)
			}
			var cleanup int
			if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.file_storage_source_cleanup where migration_id=$1`, migrationID).Scan(&cleanup); err != nil || cleanup != 0 {
				t.Fatalf("cleanup published before copy reconciled=%d err=%v", cleanup, err)
			}
			if worked, err := service.CleanupOne(ctx); err != nil || worked {
				t.Fatalf("cleanup raced active copy: worked=%t err=%v", worked, err)
			}
			close(blocking.release)
			if err := <-copied; err != nil {
				t.Fatal(err)
			}
			finished, err := service.finishCancellation(ctx, migrationID)
			if err != nil || !finished {
				t.Fatalf("finish cancellation=%t err=%v", finished, err)
			}
			status, _ = service.Status(ctx, migrationID)
			if status.Status != "canceled" {
				t.Fatalf("final status=%s", status.Status)
			}
			if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.file_storage_source_cleanup where migration_id=$1`, migrationID).Scan(&cleanup); err != nil || cleanup != 1 {
				t.Fatalf("durable cleanup after reconciliation=%d err=%v", cleanup, err)
			}
			if _, err := destination.Open(ctx, key); !errors.Is(err, ErrNotFound) {
				t.Fatalf("owned destination remains after canceled copy: %v", err)
			}
		})
	}
}

func TestObjectMigrationLedgerFailureRetainsOwnedCleanupIntent(t *testing.T) {
	ctx, pool, controller, migrationID, key, source, destination := newLocalMigrationFixture(t, nil)
	service := NewObjectMigrationService(controller)
	service.buildStore = migrationStoreFactory(source, destination)
	if _, err := pool.Exec(ctx, `create function pgbase_meta.test_fail_migration_verified() returns trigger language plpgsql as $$ begin if new.status='verified' then raise exception 'injected verified ledger failure'; end if; return new; end $$; create trigger test_fail_migration_verified before update on pgbase_meta.file_storage_migration_objects for each row execute function pgbase_meta.test_fail_migration_verified()`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.copyOne(ctx, migrationID); err == nil {
		t.Fatal("copy succeeded despite injected ledger failure")
	}
	var owned bool
	if err := pool.QueryRow(ctx, `select created_destination from pgbase_meta.file_storage_migration_objects where migration_id=$1 and object_key=$2`, migrationID, key).Scan(&owned); err != nil || !owned {
		t.Fatalf("pre-Put ownership intent=%t err=%v", owned, err)
	}
	if _, err := destination.Open(ctx, key); err != nil {
		t.Fatalf("fault did not occur after destination publication: %v", err)
	}
	if err := service.Cancel(ctx, migrationID); err != nil {
		t.Fatal(err)
	}
	status, _ := service.Status(ctx, migrationID)
	if status.Status != "canceled" {
		t.Fatalf("status=%s", status.Status)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.file_storage_source_cleanup set not_before=CURRENT_TIMESTAMP-interval '1 second' where migration_id=$1`, migrationID); err != nil {
		t.Fatal(err)
	}
	worked, err := service.CleanupOne(ctx)
	if err != nil || !worked {
		t.Fatalf("cleanup owned destination=%t err=%v", worked, err)
	}
	if _, err := destination.Open(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("untracked destination remains: %v", err)
	}
}

func TestObjectMigrationCancellationPreservesPreexistingIdenticalDestination(t *testing.T) {
	ctx, pool, controller, migrationID, key, source, destination := newLocalMigrationFixture(t, []byte("migration bytes"))
	service := NewObjectMigrationService(controller)
	service.buildStore = migrationStoreFactory(source, destination)
	if copied, err := service.copyOne(ctx, migrationID); err != nil || !copied {
		t.Fatalf("copy=%t err=%v", copied, err)
	}
	var owned bool
	if err := pool.QueryRow(ctx, `select created_destination from pgbase_meta.file_storage_migration_objects where migration_id=$1 and object_key=$2`, migrationID, key).Scan(&owned); err != nil || owned {
		t.Fatalf("preexisting object ownership=%t err=%v", owned, err)
	}
	if err := service.Cancel(ctx, migrationID); err != nil {
		t.Fatal(err)
	}
	var cleanup int
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.file_storage_source_cleanup where migration_id=$1`, migrationID).Scan(&cleanup); err != nil || cleanup != 0 {
		t.Fatalf("preexisting object cleanup receipts=%d err=%v", cleanup, err)
	}
	reader, err := destination.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(reader)
	_ = reader.Close()
	if string(got) != "migration bytes" {
		t.Fatalf("preexisting bytes=%q", got)
	}
}

func newLocalMigrationFixture(t *testing.T, destinationBytes []byte) (context.Context, *database.Pool, *StorageController, uuid.UUID, string, *migrationMemoryStore, *migrationMemoryStore) {
	t.Helper()
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Up(ctx); err != nil {
		t.Fatal(err)
	}
	controller, err := NewStorageController(ctx, pool, bytes.Repeat([]byte{8}, 32), StorageConfig{Driver: "local", LocalDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	namespace, object := strings.Repeat("a", 64), strings.Repeat("b", 64)
	key := "extensions/" + namespace + "/" + object
	source := &migrationMemoryStore{values: map[string][]byte{key: []byte("migration bytes")}}
	destination := &migrationMemoryStore{values: map[string][]byte{}}
	if destinationBytes != nil {
		destination.values[key] = append([]byte(nil), destinationBytes...)
	}
	encode := func(snapshot storageSnapshot) []byte {
		raw, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		blob, err := controller.encrypt(raw)
		if err != nil {
			t.Fatal(err)
		}
		return blob
	}
	migrationID := uuid.New()
	sourceBlob := encode(storageSnapshot{Driver: "local", LocalDirectory: "source"})
	destinationBlob := encode(storageSnapshot{Driver: "local", LocalDirectory: "destination"})
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.extension_objects(namespace_hash,key_hash,object_key,size_bytes) values($1,$2,$3,$4)`, namespace, object, key, len("migration bytes")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.file_storage_migrations(id,status,base_revision,source_config_ciphertext,destination_config_ciphertext,request_hash,total_objects) values($1,'copying',1,$2,$3,$4,1)`, migrationID, sourceBlob, destinationBlob, []byte("request")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.file_storage_migration_objects(migration_id,object_key,object_kind,status) values($1,$2,'extension','pending')`, migrationID, key); err != nil {
		t.Fatal(err)
	}
	return ctx, pool, controller, migrationID, key, source, destination
}

func migrationStoreFactory(source ObjectStore, destination ObjectStore) func(context.Context, storageSnapshot) (ObjectStore, error) {
	return func(_ context.Context, snapshot storageSnapshot) (ObjectStore, error) {
		if snapshot.LocalDirectory == "source" {
			return source, nil
		}
		return destination, nil
	}
}

type migrationBlockingStore struct {
	*migrationMemoryStore
	phase   string
	reached chan struct{}
	release chan struct{}
	once    sync.Once
	opens   int
}

func (s *migrationBlockingStore) Put(ctx context.Context, key string, body io.Reader) error {
	if s.phase == "before_put" {
		s.once.Do(func() { close(s.reached) })
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.migrationMemoryStore.Put(ctx, key, body)
}

func (s *migrationBlockingStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	s.opens++
	opens := s.opens
	s.mu.Unlock()
	if s.phase == "post_put_preledger" && opens == 2 {
		s.once.Do(func() { close(s.reached) })
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.migrationMemoryStore.Open(ctx, key)
}

func TestObjectMigrationLocalToMinIOCutoverAndDelayedCleanup(t *testing.T) {
	endpoint := os.Getenv("PGBASE_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("PGBASE_TEST_S3_ENDPOINT is not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = migrate.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Up(ctx); err != nil {
		t.Fatal(err)
	}
	localDirectory := t.TempDir()
	controller, err := NewStorageController(ctx, pool, []byte("0123456789abcdef0123456789abcdef"), StorageConfig{Driver: "local", LocalDirectory: localDirectory, AllowInsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	key := "objects/11111111-1111-4111-8111-111111111111"
	removedKey := "objects/22222222-2222-4222-8222-222222222222"
	if err = controller.Put(ctx, key, bytes.NewBufferString("durable migration")); err != nil {
		t.Fatal(err)
	}
	if err = controller.Put(ctx, removedKey, bytes.NewBufferString("removed during migration")); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("durable migration"))
	err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `set local session_replication_role=replica`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into pgbase_meta.record_files(id,collection_id,record_id,field_name,filename,content_type,size_bytes,sha256,object_key,status,created_at,ready_at) values(gen_random_uuid(),gen_random_uuid(),gen_random_uuid(),'attachment','migration.txt','text/plain',17,$2,$1,'ready',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, key, digest[:])
		if err != nil {
			return err
		}
		removedDigest := sha256.Sum256([]byte("removed during migration"))
		_, err = tx.Exec(ctx, `insert into pgbase_meta.record_files(id,collection_id,record_id,field_name,filename,content_type,size_bytes,sha256,object_key,status,created_at,ready_at) values(gen_random_uuid(),gen_random_uuid(),gen_random_uuid(),'attachment','removed.txt','text/plain',24,$2,$1,'ready',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, removedKey, removedDigest[:])
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewObjectMigrationService(controller)
	operationID := uuid.New()
	input := StorageUpdate{Driver: "s3", Bucket: "pgbase-test", Region: "us-east-1", Endpoint: endpoint, AccessKey: "pgbase-test-access", SecretKey: "pgbase-test-secret-0123456789", UsePathStyle: true}
	started, err := service.Start(ctx, operationID, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `update pgbase_meta.record_files set status='deleted',deleted_at=CURRENT_TIMESTAMP where object_key=$1`, removedKey); err != nil {
		t.Fatal(err)
	}
	localBefore, _ := NewLocalStore(localDirectory)
	if err = localBefore.Delete(ctx, removedKey); err != nil {
		t.Fatal(err)
	}
	replayed, err := service.Start(ctx, operationID, input)
	if err != nil || replayed.ID != started.ID {
		t.Fatalf("idempotent replay=%#v err=%v", replayed, err)
	}
	changed := input
	changed.Bucket = "different"
	if _, conflictErr := service.Start(ctx, operationID, changed); !errors.Is(conflictErr, ErrStorageConflict) {
		t.Fatalf("changed replay=%v", conflictErr)
	}
	if _, err = pool.Exec(ctx, `update pgbase_meta.file_storage_migration_objects set status='copying',claim_token=gen_random_uuid(),claim_expires_at=CURRENT_TIMESTAMP-interval '1 second' where migration_id=$1 and object_key=$2`, started.ID, key); err != nil {
		t.Fatal(err)
	}
	if err = service.run(ctx, started.ID); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(ctx, started.ID)
	if err != nil || status.Status != "committed" || status.CopiedObjects != 2 {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	reader, err := controller.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(reader)
	_ = reader.Close()
	if string(got) != "durable migration" {
		t.Fatalf("object=%q", got)
	}
	local, _ := NewLocalStore(localDirectory)
	if old, openErr := local.Open(ctx, key); openErr != nil {
		t.Fatal("source removed before delay", openErr)
	} else {
		_ = old.Close()
	}
	returned, err := service.Start(ctx, uuid.New(), StorageUpdate{Driver: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.run(ctx, returned.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `update pgbase_meta.file_storage_source_cleanup set not_before=case when migration_id=$1 then CURRENT_TIMESTAMP-interval '1 minute' else CURRENT_TIMESTAMP end`, started.ID); err != nil {
		t.Fatal(err)
	}
	worked, err := service.CleanupOne(ctx)
	if err != nil || worked {
		t.Fatalf("cleanup worked=%v err=%v", worked, err)
	}
	if active, openErr := controller.Open(ctx, key); openErr != nil {
		t.Fatal("round-trip cleanup removed active object", openErr)
	} else {
		_ = active.Close()
	}
	for attempts := 0; attempts < 4 && !worked; attempts++ {
		worked, err = service.CleanupOne(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !worked {
		t.Fatal("inactive S3 cleanup did not run")
	}
	if active, openErr := controller.Open(ctx, key); openErr != nil {
		t.Fatal("inactive cleanup removed active object", openErr)
	} else {
		_ = active.Close()
	}
	if _, err = local.Open(ctx, key); err != nil {
		t.Fatal("active local object was removed", err)
	}
	_ = controller.Delete(ctx, key)
}

func TestObjectMigrationCancelKeepsCurrentDestination(t *testing.T) {
	endpoint := os.Getenv("PGBASE_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("PGBASE_TEST_S3_ENDPOINT is not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = migrate.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Up(ctx); err != nil {
		t.Fatal(err)
	}
	controller, err := NewStorageController(ctx, pool, []byte("0123456789abcdef0123456789abcdef"), StorageConfig{Driver: "local", LocalDirectory: t.TempDir(), AllowInsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	service := NewObjectMigrationService(controller)
	started, err := service.Start(ctx, uuid.New(), StorageUpdate{Driver: "s3", Bucket: "pgbase-test", Region: "us-east-1", Endpoint: endpoint, AccessKey: "pgbase-test-access", SecretKey: "pgbase-test-secret-0123456789", UsePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, conflictErr := service.Start(ctx, uuid.New(), StorageUpdate{Driver: "s3", Bucket: "pgbase-test", Region: "us-east-1", Endpoint: endpoint, AccessKey: "pgbase-test-access", SecretKey: "pgbase-test-secret-0123456789", UsePathStyle: true}); !errors.Is(conflictErr, ErrStorageConflict) {
		t.Fatalf("second active migration=%v", conflictErr)
	}
	if err = service.Cancel(ctx, started.ID); err != nil {
		t.Fatal(err)
	}
	if err = service.run(ctx, started.ID); err != nil {
		t.Fatal(err)
	}
	status, _ := service.Status(ctx, started.ID)
	settings, _ := controller.Settings(ctx)
	if status.Status != "canceled" || settings.Driver != "local" {
		t.Fatalf("status=%s driver=%s", status.Status, settings.Driver)
	}
	failed, err := service.Start(ctx, uuid.New(), StorageUpdate{Driver: "s3", Bucket: "pgbase-test", Region: "us-east-1", Endpoint: endpoint, AccessKey: "pgbase-test-access", SecretKey: "pgbase-test-secret-0123456789", UsePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `update pgbase_meta.file_storage_migrations set status='failed' where id=$1`, failed.ID); err != nil {
		t.Fatal(err)
	}
	if err = service.Cancel(ctx, failed.ID); err != nil {
		t.Fatal(err)
	}
	failedStatus, _ := service.Status(ctx, failed.ID)
	if failedStatus.Status != "canceled" {
		t.Fatalf("failed cancellation=%s", failedStatus.Status)
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- service.Run(runCtx) }()
	stop()
	select {
	case runErr := <-done:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("shutdown=%v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("migration worker did not stop after cancellation")
	}
}
