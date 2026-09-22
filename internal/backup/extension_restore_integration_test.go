//go:build integration

package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestOnlineExtensionObjectSnapshotRestoresIndependentBytes(t *testing.T) {
	for _, driver := range []string{"local", "s3"} {
		t.Run(driver, func(t *testing.T) {
			endpoint := os.Getenv("PGBASE_TEST_S3_ENDPOINT")
			if driver == "s3" && endpoint == "" {
				t.Skip("PGBASE_TEST_S3_ENDPOINT is not set")
			}
			ctx := context.Background()
			sourceURL := testutil.NewDatabase(t)
			pool, err := database.Open(ctx, sourceURL, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(pool.Close)
			if err := migrate.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Up(ctx); err != nil {
				t.Fatal(err)
			}
			var store filestore.ObjectStore
			if driver == "local" {
				store, err = filestore.NewLocalStore(t.TempDir())
			} else {
				store, err = filestore.NewS3Store(ctx, filestore.S3Config{Bucket: "pgbase-test", Region: "us-east-1", Endpoint: endpoint, AccessKey: "pgbase-test-access", SecretKey: "pgbase-test-secret-0123456789", UsePathStyle: true})
			}
			if err != nil {
				t.Fatal(err)
			}
			namespaceHash := sha256.Sum256([]byte("module.js"))
			keyHash := sha256.Sum256([]byte("state"))
			namespace, key := hex.EncodeToString(namespaceHash[:]), hex.EncodeToString(keyHash[:])
			prefix := "extensions/" + namespace + "/" + key
			original, replacement := prefix+"/"+uuid.NewString(), prefix+"/"+uuid.NewString()
			content := []byte("extension state at the backup snapshot")
			for object, body := range map[string][]byte{original: content, replacement: []byte("later extension state")} {
				if err := store.Put(ctx, object, bytes.NewReader(body)); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Delete(context.Background(), object) })
			}
			if _, err := pool.Exec(ctx, `insert into pgbase_meta.extension_objects(namespace_hash,key_hash,object_key,size_bytes,logical_namespace,logical_key) values($1,$2,$3,$4,'module.js','state')`, namespace, key, original, len(content)); err != nil {
				t.Fatal(err)
			}
			if driver == "s3" {
				if _, err := pool.Exec(ctx, `insert into pgbase_meta.file_storage_settings(singleton,driver,s3_bucket,s3_region,s3_endpoint,s3_access_key,s3_secret_ciphertext,s3_use_path_style) values(true,'s3','pgbase-test','us-east-1',$1,'source',decode('01','hex'),true)`, endpoint); err != nil {
					t.Fatal(err)
				}
			}
			source := onlineObjectSource{store: store, beforeOpen: func() {
				// The backup must copy the version selected by its snapshot even
				// after a committed replacement queues that version for deletion.
				if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
					var acquired bool
					if err := tx.QueryRow(ctx, `select pg_try_advisory_xact_lock_shared($1)`, ObjectGCKey).Scan(&acquired); err != nil {
						return err
					}
					if acquired {
						t.Fatal("GC gate was not held while copying extension bytes")
					}
					if _, err := tx.Exec(ctx, `update pgbase_meta.extension_objects set object_key=$3,size_bytes=$4 where namespace_hash=$1 and key_hash=$2`, namespace, key, replacement, len("later extension state")); err != nil {
						return err
					}
					_, err := tx.Exec(ctx, `insert into pgbase_meta.file_object_deletions(object_key,queued_at) values($1,CURRENT_TIMESTAMP) on conflict do nothing`, original)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}}
			directory := filepath.Join(t.TempDir(), "snapshot")
			if err := CreateOnline(ctx, directory, sourceURL, source); err != nil {
				t.Fatal(err)
			}
			var archive bytes.Buffer
			if err := Pack(ctx, directory, &archive); err != nil {
				t.Fatal(err)
			}
			unpacked := filepath.Join(t.TempDir(), "unpacked")
			if err := Unpack(ctx, &archive, unpacked); err != nil {
				t.Fatal(err)
			}
			for _, object := range []string{original, replacement} {
				if err := store.Delete(ctx, object); err != nil {
					t.Fatal(err)
				}
			}
			target, err := RestoreNew(ctx, unpacked, sourceURL, filepath.Join(t.TempDir(), "targets"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := DiscardTarget(context.Background(), target); err != nil {
					t.Errorf("discard restored target: %v", err)
				}
			})
			restored, err := database.Open(ctx, target.DatabaseURL, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			if err := filestore.ResetRestoredStorage(ctx, restored); err != nil {
				t.Fatal(err)
			}
			var physical string
			if err := restored.QueryRow(ctx, `select object_key from pgbase_meta.extension_objects where logical_namespace='module.js' and logical_key='state'`).Scan(&physical); err != nil || physical != original {
				t.Fatalf("restored extension identity=%q want=%q err=%v", physical, original, err)
			}
			local, err := filestore.NewStorageController(ctx, restored, bytes.Repeat([]byte{7}, 32), filestore.StorageConfig{Driver: "local", LocalDirectory: target.FilesDirectory})
			if err != nil {
				t.Fatal(err)
			}
			reader, err := local.Open(ctx, physical)
			if err != nil {
				t.Fatalf("restored extension metadata has no readable object: %v", err)
			}
			got, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			if readErr != nil || closeErr != nil || !bytes.Equal(got, content) {
				t.Fatalf("restored extension bytes=%q read=%v close=%v", got, readErr, closeErr)
			}
			var current string
			if err := pool.QueryRow(ctx, `select object_key from pgbase_meta.extension_objects where namespace_hash=$1 and key_hash=$2`, namespace, key).Scan(&current); err != nil || current != replacement {
				t.Fatalf("concurrent replacement=%q want=%q err=%v", current, replacement, err)
			}
		})
	}
}
