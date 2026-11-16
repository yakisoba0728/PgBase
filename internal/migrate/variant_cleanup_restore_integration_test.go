//go:build integration

package migrate

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestVariantCleanupRestoresSchema68BeforeUpgrade(t *testing.T) {
	ctx := t.Context()
	sourceURL := testutil.NewDatabase(t)
	source, err := database.Open(ctx, sourceURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(source.Close)
	legacy := New(source, nil)
	legacy.migrations = legacy.migrations[:len(legacy.migrations)-1]
	if legacy.migrations[len(legacy.migrations)-1].Version != 68 {
		t.Fatal("fixture must start at schema68")
	}
	if err = legacy.Up(ctx); err != nil {
		t.Fatal(err)
	}
	key := "objects/" + uuid.NewString()
	if _, err = source.Exec(ctx, `insert into pgbase_meta.file_storage_settings(singleton,driver,s3_bucket,s3_access_key,s3_secret_ciphertext) values(true,'s3','source-bucket','source-key',$1)`, bytes.Repeat([]byte{3}, 48)); err != nil {
		t.Fatal(err)
	}
	if _, err = source.Exec(ctx, `insert into pgbase_meta.file_object_deletions(object_key,queued_at) values($1,now())`, key); err != nil {
		t.Fatal(err)
	}
	source.Close()
	files := t.TempDir()
	if err = os.MkdirAll(filepath.Join(files, "objects"), 0700); err != nil {
		t.Fatal(err)
	}
	content := []byte("schema68 restored file bytes")
	if err = os.WriteFile(filepath.Join(files, filepath.FromSlash(key)), content, 0600); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "snapshot")
	if err = backup.Create(ctx, directory, sourceURL, files); err != nil {
		t.Fatal(err)
	}
	target, err := backup.RestoreNew(ctx, directory, sourceURL, filepath.Join(t.TempDir(), "targets"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backup.DiscardTarget(context.Background(), target); err != nil {
			t.Errorf("discard restored target: %v", err)
		}
	})
	restored, err := database.Open(ctx, target.DatabaseURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	// Managed restore detaches inherited storage before starting the candidate
	// application, so this call must work before migration 69 adds retention.
	if err = filestore.ResetRestoredStorage(ctx, restored); err != nil {
		t.Fatalf("reset restored schema68 before migrations: %v", err)
	}
	for range 2 {
		if err = New(restored, nil).Up(ctx); err != nil {
			t.Fatal(err)
		}
	}
	status, err := New(restored, nil).Status(ctx)
	if err != nil || status.Current != 69 || status.Latest != 69 || status.Dirty || !status.Compatible {
		t.Fatalf("restored migration status=%+v err=%v", status, err)
	}
	var driver, bucket, access string
	var secret []byte
	var queued int
	if err = restored.QueryRow(ctx, `select driver,s3_bucket,s3_access_key,s3_secret_ciphertext,(select count(*) from pgbase_meta.file_object_deletions) from pgbase_meta.file_storage_settings where singleton`).Scan(&driver, &bucket, &access, &secret, &queued); err != nil || driver != "local" || bucket != "" || access != "" || len(secret) != 0 || queued != 0 {
		t.Fatalf("source namespace survived reset: driver=%q bucket=%q access=%q secret=%d queued=%d err=%v", driver, bucket, access, len(secret), queued, err)
	}
	raw, err := os.ReadFile(filepath.Join(target.FilesDirectory, filepath.FromSlash(key)))
	if err != nil || !bytes.Equal(raw, content) {
		t.Fatalf("restored bytes=%q err=%v", raw, err)
	}
}
