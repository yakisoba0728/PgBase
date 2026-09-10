//go:build integration

package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

type s3ObjectSource struct{ store *filestore.S3Store }

func (s s3ObjectSource) OpenObject(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.store.Open(ctx, key)
}

func TestS3FileSnapshotRestoreNewThenStorageResetPreservesIndependentLocalBytes(t *testing.T) {
	endpoint := os.Getenv("PGBASE_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("PGBASE_TEST_S3_ENDPOINT is not set")
	}
	ctx := context.Background()
	sourceURL := testutil.NewDatabase(t)
	pool, err := database.Open(ctx, sourceURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.admins(id,email,password_hash,active,created_at,updated_at) values($1,$2,'x',true,now(),now())`, actor, actor.String()+"@example.com"); err != nil {
		t.Fatal(err)
	}
	service := schema.NewService(pool, &schema.Cache{}, nil)
	change := schema.NewCreateChange(schema.CreateCollectionInput{Name: "restore_files", Fields: []schema.FieldInput{{Name: "attachment", Type: schema.TypeFile}}})
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := service.Apply(ctx, schema.LegacyApply(change, plan, actor))
	if err != nil {
		t.Fatal(err)
	}
	recordID := uuid.New()
	eventID := uuid.New()
	if _, err = pool.Exec(ctx, `insert into app.restore_files(id,created_at,updated_at) values($1,now(),now())`, recordID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.record_audit_events(event_id,actor_kind,actor_id,request_id,collection_id,collection_name,record_id,operation,schema_version,schema_digest,previous_version,new_version,previous_etag,new_etag,changed_fields,input_hash,idempotency_key_hash,compatibility,committed_at) values($1,'administrator',$2,'backup-file-restore',$3,'restore_files',$4,'create',$5,decode(repeat('aa',32),'hex'),null,1,null,'"pgb1.file"',array[]::text[],decode(repeat('bb',32),'hex'),decode(repeat('cc',32),'hex'),'none',now())`, eventID, actor, applied.Collection.ID, recordID, plan.BaseVersion+1); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.record_revisions(collection_id,record_id,version,validator,last_event_id,updated_at) values($1,$2,1,decode(repeat('00',32),'hex'),$3,now())`, applied.Collection.ID, recordID, eventID); err != nil {
		t.Fatal(err)
	}
	store, err := filestore.NewS3Store(ctx, filestore.S3Config{Bucket: "pgbase-test", Region: "us-east-1", Endpoint: endpoint, AccessKey: "pgbase-test-access", SecretKey: "pgbase-test-secret-0123456789", UsePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	key := "objects/" + uuid.NewString()
	content := []byte("independent restored bytes")
	if err = store.Put(ctx, key, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	defer store.Delete(context.Background(), key)
	digest := sha256.Sum256(content)
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.record_files(id,collection_id,record_id,field_name,filename,content_type,size_bytes,sha256,object_key,status,created_at,ready_at) values($1,$2,$3,'attachment','restore.txt','text/plain',$4,$5,$6,'ready',now(),now())`, uuid.New(), applied.Collection.ID, recordID, len(content), digest[:], key); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `update pgbase_meta.file_storage_settings set driver='s3',s3_bucket='pgbase-test',s3_region='us-east-1',s3_endpoint=$1,s3_access_key='source',s3_secret_ciphertext=decode('01','hex'),s3_use_path_style=true`, endpoint); err != nil {
		t.Fatal(err)
	}
	pool.Close()
	directory := filepath.Join(t.TempDir(), "snapshot")
	if err = CreateOnline(ctx, directory, sourceURL, s3ObjectSource{store}); err != nil {
		t.Fatal(err)
	}
	if err = store.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	target, err := RestoreNew(ctx, directory, sourceURL, filepath.Join(t.TempDir(), "targets"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cleanupErr := DiscardTarget(context.Background(), target); cleanupErr != nil {
			t.Errorf("discard restored target: %v", cleanupErr)
		}
	})
	targetPool, err := database.Open(ctx, target.DatabaseURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer targetPool.Close()
	// Managed restore calls this reset after RestoreNew and before the candidate
	// application starts. This lower-level test invokes the same boundary directly.
	if err = filestore.ResetRestoredStorage(ctx, targetPool); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(target.FilesDirectory, filepath.FromSlash(key)))
	if err != nil || !bytes.Equal(raw, content) {
		t.Fatalf("restored bytes=%q err=%v", raw, err)
	}
	var driver string
	var secret []byte
	if err = targetPool.QueryRow(ctx, `select driver,s3_secret_ciphertext from pgbase_meta.file_storage_settings where singleton`).Scan(&driver, &secret); err != nil || driver != "local" || len(secret) != 0 {
		t.Fatalf("restored settings driver=%q secret=%d err=%v", driver, len(secret), err)
	}
	if _, err = store.Open(ctx, key); !errors.Is(err, filestore.ErrNotFound) {
		t.Fatalf("Open deleted source object = %v, want ErrNotFound", err)
	}
}
