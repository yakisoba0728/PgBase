//go:build integration

package filestore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestStorageControllerMinIOSaveRotateRestartAndRollback(t *testing.T) {
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
	secret := []byte("0123456789abcdef0123456789abcdef")
	controller, err := NewStorageController(ctx, pool, secret, StorageConfig{Driver: "local", LocalDirectory: t.TempDir(), AllowInsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := controller.Update(ctx, StorageUpdate{Driver: "s3", Bucket: "pgbase-test", Region: "us-east-1", Endpoint: endpoint, AccessKey: "pgbase-test-access", SecretKey: "pgbase-test-secret-0123456789", UsePathStyle: true, Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	if settings.Revision != 2 || !settings.SecretConfigured {
		t.Fatalf("settings=%#v", settings)
	}
	var encryptedSecret []byte
	if err = pool.QueryRow(ctx, `select s3_secret_ciphertext from pgbase_meta.file_storage_settings where singleton`).Scan(&encryptedSecret); err != nil {
		t.Fatal(err)
	}
	if len(encryptedSecret) == 0 || bytes.Contains(encryptedSecret, []byte("pgbase-test-secret-0123456789")) {
		t.Fatal("stored S3 secret is empty or contains plaintext")
	}
	key := "objects/11111111-1111-4111-8111-111111111111"
	if err = controller.Put(ctx, key, bytes.NewBufferString("persisted")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Delete(context.Background(), key) })

	restarted, err := NewStorageController(ctx, pool, secret, StorageConfig{Driver: "local", LocalDirectory: t.TempDir(), AllowInsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	r, err := restarted.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	_ = r.Close()
	if string(got) != "persisted" {
		t.Fatalf("restarted object=%q", got)
	}
	preserved, err := restarted.Update(ctx, StorageUpdate{Driver: "s3", Bucket: "pgbase-test", Region: "us-east-1", Endpoint: endpoint, AccessKey: "pgbase-test-access", UsePathStyle: true, Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !preserved.SecretConfigured || preserved.Revision != 3 {
		t.Fatalf("preserved=%#v", preserved)
	}
	_, err = restarted.Update(ctx, StorageUpdate{Driver: "s3", Bucket: "pgbase-test", Region: "us-east-1", Endpoint: endpoint, AccessKey: "pgbase-test-access", SecretKey: "wrong-secret-value", UsePathStyle: true, Revision: 3})
	if err == nil {
		t.Fatal("wrong credentials were saved")
	}
	after, err := restarted.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != 3 || !after.SecretConfigured {
		t.Fatalf("rollback=%#v", after)
	}
	if err = restarted.Test(ctx); err != nil {
		t.Fatal(err)
	}
	activeDownload, err := restarted.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, updateErr := restarted.Update(ctx, StorageUpdate{Driver: "s3", Bucket: "pgbase-test", Region: "us-east-1", Endpoint: endpoint, AccessKey: "pgbase-test-access", UsePathStyle: true, Revision: 3})
		done <- updateErr
	}()
	select {
	case err := <-done:
		t.Fatalf("settings update did not wait for active file operation: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := activeDownload.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.file_object_deletions(object_key,queued_at) values('objects/22222222-2222-4222-8222-222222222222',CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	_, err = restarted.Update(ctx, StorageUpdate{Driver: "local", Revision: 4})
	if !errors.Is(err, ErrStorageConflict) {
		t.Fatalf("populated location change=%v", err)
	}
}

func TestStorageLeaseDoesNotExhaustRequestPoolAndLostLeaseCannotPublish(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t)+"&pool_max_conns=4", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = migrate.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Up(ctx); err != nil {
		t.Fatal(err)
	}
	controller, err := NewStorageController(ctx, pool, bytes.Repeat([]byte{9}, 32), StorageConfig{Driver: "local", LocalDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	leases := make([]func(), 4)
	for i := range leases {
		_, leases[i], err = controller.BeginStorageOperation(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var one int
		return tx.QueryRow(ctx, `select 1`).Scan(&one)
	}); err != nil {
		t.Fatalf("request transaction with four storage leases: %v", err)
	}
	for _, release := range leases {
		release()
	}
	if active := len(controller.operations); active != 0 {
		t.Fatalf("released coordination slots=%d, want 0", active)
	}

	leasedCtx, release, err := controller.BeginStorageOperation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lease := leasedCtx.Value(storageLeaseContextKey{}).(storageLease)
	var terminated bool
	if err := pool.QueryRow(ctx, `select pg_terminate_backend($1)`, int32(lease.backend)).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate storage lease backend=%t err=%v", terminated, err)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.file_storage_settings set revision=revision+1 where singleton`); err != nil {
		t.Fatal(err)
	}
	err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return controller.GuardStoragePublication(leasedCtx, tx)
	})
	if !errors.Is(err, ErrStorageConflict) {
		t.Fatalf("publication through lost stale lease = %v", err)
	}
	release()
	if active := len(controller.operations); active != 0 {
		t.Fatalf("lost-session release retained coordination slots=%d", active)
	}
}
