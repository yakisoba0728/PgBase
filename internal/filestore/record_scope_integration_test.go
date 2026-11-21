//go:build integration

package filestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

// TestRecordFileCountsAndListsStayWithinOneOwner covers every record-scoped
// record_files predicate for both owner kinds: upload capacity, required-file
// deletion and listing must count only the addressed record.
func TestRecordFileCountsAndListsStayWithinOneOwner(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}

	baseCollection := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.collections(id,name,table_name,status,created_at,updated_at) VALUES($1,'scoped_files','scoped_files','active',now(),now())`, baseCollection); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.fields(id,collection_id,name,column_name,field_type,required,is_unique,is_indexed,has_default,default_json,ordinal,created_at) VALUES($1,$2,'attachment','attachment','file',true,false,false,false,NULL,0,now())`, uuid.New(), baseCollection); err != nil {
		t.Fatal(err)
	}
	baseFirst, baseSecond := uuid.New(), uuid.New()
	for _, record := range []uuid.UUID{baseFirst, baseSecond} {
		insertScopedRecordRevision(t, ctx, pool, baseCollection, record)
	}

	authCollection := uuid.New()
	fields := `[{"name":"attachment","type":"file","required":true,"unique":false,"indexed":false,"options":{"maxSelect":2,"mimeTypes":["text/plain"]}}]`
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.auth_collections(id,name,registration_enabled,profile_schema,fields_json,created_at,updated_at) VALUES($1,'scoped_members',true,'{}',$2,now(),now())`, authCollection, fields); err != nil {
		t.Fatal(err)
	}
	authFirst, authSecond := uuid.New(), uuid.New()
	for index, user := range []uuid.UUID{authFirst, authSecond} {
		if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.users(id,auth_collection_id,email,password_hash,active,created_at,updated_at) VALUES($1,$2,$3,'fixture',true,now(),now())`, user, authCollection, []string{"scope-first@example.test", "scope-second@example.test"}[index]); err != nil {
			t.Fatal(err)
		}
	}

	objects, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(pool, objects, func(context.Context, pgx.Tx, identity.Subject, uuid.UUID, uuid.UUID, string, Operation) error {
		return nil
	}, Options{
		MaximumBytes: 32, MaximumImagePixels: 1_000_000, ThumbnailDimension: 64, AllowedTypes: []string{"text/plain"}, TokenSecret: bytes.Repeat([]byte{5}, 32), TokenTTL: time.Minute,
		Policy: func(context.Context, pgx.Tx, uuid.UUID, string) (FieldPolicy, error) {
			return FieldPolicy{MaximumCount: 2, Required: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	admin := identity.Subject{Kind: identity.Administrator, ID: uuid.New()}
	upload := func(collection, record uuid.UUID, name string) (File, error) {
		body := []byte(name)
		return service.Upload(ctx, admin, Upload{CollectionID: collection, RecordID: record, Field: "attachment", Filename: name, ContentType: "text/plain", Size: int64(len(body)), SHA256: sha256.Sum256(body), Body: bytes.NewReader(body)})
	}
	list := func(t *testing.T, collection, record uuid.UUID) []File {
		t.Helper()
		files, err := service.List(ctx, admin, collection, record, "attachment")
		if err != nil {
			t.Fatal(err)
		}
		return files
	}

	for _, owner := range []struct {
		name                      string
		collection, first, second uuid.UUID
		wantAuthColumn            bool
	}{
		{"base", baseCollection, baseFirst, baseSecond, false},
		{"auth", authCollection, authFirst, authSecond, true},
	} {
		t.Run(owner.name, func(t *testing.T) {
			first, err := upload(owner.collection, owner.first, owner.name+"-1.txt")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := upload(owner.collection, owner.first, owner.name+"-2.txt"); err != nil {
				t.Fatal(err)
			}
			if _, err := upload(owner.collection, owner.first, owner.name+"-3.txt"); !errors.Is(err, ErrTooMany) {
				t.Fatalf("third upload error = %v, want ErrTooMany", err)
			}
			// The other record's capacity is independent of the full record.
			only, err := upload(owner.collection, owner.second, owner.name+"-other.txt")
			if err != nil {
				t.Fatalf("other record upload: %v", err)
			}
			var authColumn bool
			if err := pool.QueryRow(ctx, `select auth_collection_id is not null from pgbase_meta.record_files where id=$1`, only.ID).Scan(&authColumn); err != nil || authColumn != owner.wantAuthColumn {
				t.Fatalf("owner columns auth=%t err=%v", authColumn, err)
			}

			if files := list(t, owner.collection, owner.first); len(files) != 2 || files[0].ID != first.ID || files[0].CollectionID != owner.collection || files[0].RecordID != owner.first {
				t.Fatalf("first record list = %#v", files)
			}
			if files := list(t, owner.collection, owner.second); len(files) != 1 || files[0].ID != only.ID {
				t.Fatalf("second record list = %#v", files)
			}

			// The required-file guard counts only the addressed record.
			if err := service.Delete(ctx, admin, only.ID); !errors.Is(err, ErrRequired) {
				t.Fatalf("delete the only required file error = %v, want ErrRequired", err)
			}
			if err := service.Delete(ctx, admin, first.ID); err != nil {
				t.Fatalf("delete one of two required files: %v", err)
			}
			remaining := list(t, owner.collection, owner.first)
			if len(remaining) != 1 {
				t.Fatalf("first record after delete = %#v", remaining)
			}
			if err := service.Delete(ctx, admin, remaining[0].ID); !errors.Is(err, ErrRequired) {
				t.Fatalf("delete the last required file error = %v, want ErrRequired", err)
			}
		})
	}
}

// TestRecordMetadataResolvesPublicIDsInsideOneCollection keeps a shared text ID
// in another collection out of the requested collection's metadata.
func TestRecordMetadataResolvesPublicIDsInsideOneCollection(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	policy := `{"kind":"text","minLength":1,"maxLength":32,"pattern":"^[A-Za-z0-9]+$"}`
	collections := []uuid.UUID{uuid.New(), uuid.New()}
	records := []uuid.UUID{uuid.New(), uuid.New()}
	fileIDs := []uuid.UUID{uuid.New(), uuid.New()}
	for index, collection := range collections {
		name := []string{"metadata_scope_first", "metadata_scope_second"}[index]
		if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.collections(id,name,table_name,status,record_id,created_at,updated_at) VALUES($1,$2,$2,'active',$3::jsonb,now(),now())`, collection, name, policy); err != nil {
			t.Fatal(err)
		}
		insertScopedRecordRevision(t, ctx, pool, collection, records[index])
		if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.record_identities(collection_id,collection_kind,scope_id,internal_id,external_id) VALUES($1,'base',$1,$2,'SharedMeta01')`, collection, records[index]); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.record_files(id,collection_id,record_id,field_name,filename,content_type,size_bytes,sha256,object_key,status,created_at,ready_at) VALUES($1,$2,$3,'attachment','meta.txt','text/plain',0,decode(repeat('00',32),'hex'),$4,'ready',now(),now())`, fileIDs[index], collection, records[index], "objects/"+uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	service := &Service{pool: pool}
	service.options.MetadataCatalog = func(context.Context, pgx.Tx, uuid.UUID) (schema.Collection, error) {
		return schema.Collection{RecordID: schema.RecordIDPolicy{Kind: schema.RecordIDText}, Fields: []schema.Field{{Name: "attachment", Type: schema.TypeFile}}}, nil
	}
	admin := identity.Subject{Kind: identity.Administrator, ID: uuid.New()}
	for index, collection := range collections {
		metadata, err := service.ListRecordMetadata(ctx, admin, collection, []string{"SharedMeta01"})
		if err != nil {
			t.Fatal(err)
		}
		files := metadata["SharedMeta01"]["attachment"]
		if len(files) != 1 || files[0].ID != fileIDs[index] || files[0].RecordID != records[index] {
			t.Fatalf("collection %d metadata = %#v", index, files)
		}
	}
}

func insertScopedRecordRevision(t *testing.T, ctx context.Context, pool *database.Pool, collection, record uuid.UUID) {
	t.Helper()
	eventID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.record_audit_events
		(event_id,actor_kind,actor_id,request_id,collection_id,collection_name,record_id,operation,schema_version,schema_digest,previous_version,new_version,previous_etag,new_etag,changed_fields,input_hash,idempotency_key_hash,compatibility,committed_at)
		VALUES($1,'administrator',$2,'file-scope-request',$3,'scoped',$4,'create',1,decode(repeat('11',32),'hex'),NULL,1,NULL,'"etag"',ARRAY['attachment'],decode(repeat('22',32),'hex'),decode(repeat('33',32),'hex'),'none',now())`, eventID, uuid.New(), collection, record); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.record_revisions(collection_id,record_id,version,validator,last_event_id,updated_at)
		VALUES($1,$2,1,decode(repeat('44',32),'hex'),$3,now())`, collection, record, eventID); err != nil {
		t.Fatal(err)
	}
}
