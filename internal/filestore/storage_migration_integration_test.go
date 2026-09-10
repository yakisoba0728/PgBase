//go:build integration

package filestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

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
