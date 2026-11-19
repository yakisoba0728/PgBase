//go:build integration

package backup

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/migrate"
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

func TestOnlineSnapshotPinsFileStorageAcrossInventoryAndCopy(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	databaseURL := testutil.NewDatabase(t)
	pool, err := database.Open(ctx, databaseURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	secret := bytes.Repeat([]byte{7}, 32)
	controller, err := filestore.NewStorageController(ctx, pool, secret, filestore.StorageConfig{Driver: "local", LocalDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	files, err := filestore.NewService(pool, controller, func(context.Context, pgx.Tx, identity.Subject, uuid.UUID, uuid.UUID, string, filestore.Operation) error {
		return nil
	}, filestore.Options{MaximumBytes: 1024, MaximumImagePixels: 1024, ThumbnailDimension: 16, AllowedTypes: []string{"text/plain"}, TokenSecret: secret, TokenTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	namespace, key := strings.Repeat("a", 64), strings.Repeat("b", 64)
	object := "extensions/" + namespace + "/" + key + "/" + uuid.NewString()
	const content = "snapshot contents"
	if err := controller.Put(ctx, object, strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.extension_objects(namespace_hash,key_hash,object_key,size_bytes,logical_namespace,logical_key) values($1,$2,$3,$4,'test','value')`, namespace, key, object, len(content)); err != nil {
		t.Fatal(err)
	}
	observer, err := pool.OpenCoordinationSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close(context.Background())
	const storageTransitionLock int64 = 0x7067626173650034
	ctx = context.WithValue(ctx, progressKey{}, func(phase string) error {
		if phase != "dumping_database" && !strings.HasPrefix(phase, "copying_objects_") {
			return nil
		}
		// Prove the exporting connection owns the storage lease throughout the
		// snapshot, so loss of that session cannot leave a usable backup.
		var held bool
		if err := observer.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_locks gc JOIN pg_locks storage USING (pid, database)
			WHERE gc.locktype='advisory' AND gc.classid=($1::bigint>>32)::oid
			AND gc.objid=($1::bigint&4294967295)::oid AND gc.mode='ExclusiveLock' AND gc.granted
			AND storage.locktype='advisory' AND storage.classid=($2::bigint>>32)::oid
			AND storage.objid=($2::bigint&4294967295)::oid AND storage.mode='ShareLock' AND storage.granted
		)`, ObjectGCKey, storageTransitionLock).Scan(&held); err != nil {
			return err
		}
		if !held {
			t.Fatalf("snapshot connection did not pin storage during %q", phase)
		}
		return nil
	})
	directory := filepath.Join(t.TempDir(), "snapshot")
	if err := CreateOnline(ctx, directory, databaseURL, files); err != nil {
		t.Fatal(err)
	}
	if _, err := observer.Exec(ctx, `SELECT pg_advisory_lock($1)`, storageTransitionLock); err != nil {
		t.Fatalf("backup retained storage lock: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(directory, "files", object)); err != nil || string(data) != content {
		t.Fatalf("backup copied %q, %v", data, err)
	}
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
