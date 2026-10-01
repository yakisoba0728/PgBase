//go:build integration

package backup

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

type onlineObjectSource struct {
	store      filestore.ObjectStore
	beforeOpen func()
}

func (s onlineObjectSource) OpenObject(ctx context.Context, key string) (io.ReadCloser, error) {
	if s.beforeOpen != nil {
		s.beforeOpen()
	}
	return s.store.Open(ctx, key)
}

func TestOnlineSnapshotRetainsConcurrentDatabaseAndObjectState(t *testing.T) {
	for _, driver := range []string{"local", "s3"} {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			databaseURL := testutil.NewDatabase(t)
			conn, err := pgx.Connect(ctx, databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close(ctx)
			_, err = conn.Exec(ctx, `CREATE SCHEMA app;CREATE SCHEMA pgbase_meta;CREATE SCHEMA pgbase_relation;
   CREATE TABLE app.probe(value text);INSERT INTO app.probe VALUES('snapshot');
   CREATE TABLE pgbase_meta.record_files(id uuid,object_key text,thumbnail_object_key text,ready_at timestamptz,status text);
   CREATE TABLE pgbase_meta.record_file_variants(file_id uuid,object_key text);
   CREATE TABLE pgbase_meta.extension_objects(object_key text);`)
			if err != nil {
				t.Fatal(err)
			}
			var store filestore.ObjectStore
			if driver == "local" {
				store, err = filestore.NewLocalStore(t.TempDir())
			} else {
				endpoint := os.Getenv("PGBASE_TEST_S3_ENDPOINT")
				if endpoint == "" {
					t.Skip("PGBASE_TEST_S3_ENDPOINT not set")
				}
				store, err = filestore.NewS3Store(ctx, filestore.S3Config{Bucket: "pgbase-test", Region: "us-east-1", Endpoint: endpoint, AccessKey: "pgbase-test-access", SecretKey: "pgbase-test-secret-0123456789", UsePathStyle: true})
			}
			if err != nil {
				t.Fatal(err)
			}
			key := "objects/" + uuid.NewString()
			if err = store.Put(ctx, key, bytes.NewBufferString("snapshot object")); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Delete(ctx, key) })
			_, err = conn.Exec(ctx, `INSERT INTO pgbase_meta.record_files VALUES($1,$2,NULL,now(),'ready')`, uuid.New(), key)
			if err != nil {
				t.Fatal(err)
			}
			source := onlineObjectSource{store: store, beforeOpen: func() {
				var acquired bool
				if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock_shared($1)`, ObjectGCKey).Scan(&acquired); err != nil {
					t.Fatal(err)
				}
				if acquired {
					t.Fatal("GC lock was not held while copying")
				}
				if _, err := conn.Exec(ctx, `UPDATE app.probe SET value='later';DELETE FROM pgbase_meta.record_files`); err != nil {
					t.Fatal(err)
				}
			}}
			directory := filepath.Join(t.TempDir(), "snapshot")
			if err = CreateOnline(ctx, directory, databaseURL, source); err != nil {
				t.Fatal(err)
			}
			// Closing the exporting connection sends Terminate; PostgreSQL may
			// release its session locks just after the client's Close returns.
			// A bounded acquisition verifies release without racing that message.
			unlockCtx, cancelUnlock := context.WithTimeout(ctx, 5*time.Second)
			_, err = conn.Exec(unlockCtx, `SELECT pg_advisory_lock_shared($1)`, ObjectGCKey)
			cancelUnlock()
			if err != nil {
				t.Fatalf("GC gate retained err=%v", err)
			}
			_, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock_shared($1)`, ObjectGCKey)
			restoredURL := testutil.NewDatabase(t)
			restoredFiles := filepath.Join(t.TempDir(), "files")
			if err = Restore(ctx, directory, restoredURL, restoredFiles); err != nil {
				t.Fatal(err)
			}
			restored, err := pgx.Connect(ctx, restoredURL)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close(ctx)
			var value string
			var count int
			if err = restored.QueryRow(ctx, `SELECT value,(SELECT count(*) FROM pgbase_meta.record_files) FROM app.probe`).Scan(&value, &count); err != nil || value != "snapshot" || count != 1 {
				t.Fatalf("restored=%q count=%d err=%v", value, count, err)
			}
			data, err := os.ReadFile(filepath.Join(restoredFiles, key))
			if err != nil || string(data) != "snapshot object" {
				t.Fatalf("object=%q err=%v", data, err)
			}
		})
	}
}
