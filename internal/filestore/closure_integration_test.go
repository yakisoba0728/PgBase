//go:build integration

package filestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

type closureRenderStore struct {
	ObjectStore
	entered, resume chan struct{}
}

func (s *closureRenderStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if s.entered != nil {
		close(s.entered)
		select {
		case <-s.resume:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.ObjectStore.Open(ctx, key)
}

func newClosureFile(t *testing.T, field string) (*database.Pool, *Service, File, identity.Subject, *closureRenderStore) {
	t.Helper()
	ctx := t.Context()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	collection, user := uuid.New(), uuid.New()
	fields, _ := json.Marshal([]map[string]any{{"name": field, "type": "file", "options": map[string]any{"maxSelect": 2, "mimeTypes": []string{"image/png"}, "thumbs": []string{"2x2"}}}})
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.auth_collections(id,name,fields_json,created_at,updated_at) values($1,'closure_members',$2,now(),now());`, collection, fields); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.users(id,auth_collection_id,email,password_hash,active,created_at,updated_at) values($1,$2,'closure@example.test','fixture',true,now(),now())`, user, collection); err != nil {
		t.Fatal(err)
	}
	local, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &closureRenderStore{ObjectStore: local}
	service, err := NewService(pool, store, func(context.Context, pgx.Tx, identity.Subject, uuid.UUID, uuid.UUID, string, Operation) error {
		return nil
	}, Options{MaximumBytes: 1 << 20, MaximumImagePixels: 1_000_000, ThumbnailDimension: 32, AllowedTypes: []string{"image/png"}, TokenSecret: bytes.Repeat([]byte{1}, 32), TokenTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	if err = png.Encode(&body, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	data := body.Bytes()
	sum := sha256.Sum256(data)
	principal := identity.Subject{Kind: identity.Administrator, ID: uuid.New()}
	file, err := service.Upload(ctx, principal, Upload{CollectionID: collection, RecordID: user, Field: field, Filename: "test.png", ContentType: "image/png", Size: int64(len(data)), SHA256: sum, Body: bytes.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	return pool, service, file, principal, store
}

func waitClosureLock(t *testing.T, pool *database.Pool, queryFragment string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(t.Context(), `select exists(select 1 from pg_stat_activity where datname=current_database() and pid<>pg_backend_pid() and wait_event_type='Lock' and query like $1)`, "%"+queryFragment+"%").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no lock waiter for %s", queryFragment)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestVariantPublicationSerializesWithDeleteAndQueuesNewObject(t *testing.T) {
	pool, service, file, principal, store := newClosureFile(t, "attachment")
	store.entered, store.resume = make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-store.resume:
		default:
			close(store.resume)
		}
	})
	rendered := make(chan error, 1)
	var variantKey string
	go func() { key, err := service.variantObject(t.Context(), file, "2x2"); variantKey = key; rendered <- err }()
	select {
	case <-store.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("render never started")
	}
	deleted := make(chan error, 1)
	go func() { deleted <- service.Delete(t.Context(), principal, file.ID) }()
	waitClosureLock(t, pool, "FOR UPDATE")
	close(store.resume)
	if err := <-rendered; err != nil {
		t.Fatal(err)
	}
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	store.entered = nil
	var variants, queued int
	if err := pool.QueryRow(t.Context(), `select (select count(*) from pgbase_meta.record_file_variants where file_id=$1),(select count(*) from pgbase_meta.file_object_deletions)`, file.ID).Scan(&variants, &queued); err != nil {
		t.Fatal(err)
	}
	if variants != 0 || queued != 3 {
		t.Fatalf("variants=%d queued=%d", variants, queued)
	}
	if _, err := service.variantObject(t.Context(), file, "3x3"); err != ErrNotFound {
		t.Fatalf("deleted variant: %v", err)
	}
	for {
		worked, err := service.DrainOne(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	for _, key := range []string{file.ObjectKey, file.ThumbnailKey, variantKey} {
		if reader, err := store.Open(t.Context(), key); err != ErrNotFound {
			if reader != nil {
				_ = reader.Close()
			}
			t.Fatalf("deleted object %s remains: %v", key, err)
		}
	}
}

func TestDeleteWaitsForFieldLockWithoutHoldingFileRow(t *testing.T) {
	pool, service, file, principal, _ := newClosureFile(t, "attachment")
	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(t.Context(), `select pg_advisory_xact_lock(hashtextextended($1,0))`, file.CollectionID.String()+"/"+file.RecordID.String()+"/"+file.Field); err != nil {
		t.Fatal(err)
	}
	deleted := make(chan error, 1)
	go func() { deleted <- service.Delete(t.Context(), principal, file.ID) }()
	waitClosureLock(t, pool, "pg_advisory_xact_lock")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err = tx.Exec(ctx, `select id from pgbase_meta.record_files where id=$1 for update`, file.ID); err != nil {
		t.Fatalf("Delete held row before field lock: %v", err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = <-deleted; err != nil {
		t.Fatal(err)
	}
}

func TestPairedAuthCollectionFileIdentityAndLongField(t *testing.T) {
	pool, service, file, principal, _ := newClosureFile(t, "attachment")
	if _, err := pool.Exec(t.Context(), `insert into pgbase_meta.collections(id,name,table_name,collection_type,status,created_at,updated_at) values($1,'closure_members','closure_members','auth','active',now(),now())`, file.CollectionID); err != nil {
		t.Fatal(err)
	}
	collection, record, err := service.ResolveRecordID(t.Context(), "closure_members", file.RecordID.String())
	if err != nil || collection != file.CollectionID || record != file.RecordID {
		t.Fatalf("identity %s/%s: %v", collection, record, err)
	}
	field := "a" + strings.Repeat("x", 63)
	fields, _ := json.Marshal([]map[string]any{{"name": field, "type": "file"}})
	if _, err = pool.Exec(t.Context(), `update pgbase_meta.auth_collections set fields_json=$2 where id=$1`, collection, fields); err != nil {
		t.Fatal(err)
	}
	if _, err = service.List(t.Context(), principal, collection, record, field); err != nil {
		t.Fatalf("64-char List: %v", err)
	}
}

func TestAuthFileUploadAccepts64CharacterFieldName(t *testing.T) {
	_, service, file, principal, _ := newClosureFile(t, "a"+strings.Repeat("x", 63))
	items, err := service.List(t.Context(), principal, file.CollectionID, file.RecordID, file.Field)
	if err != nil || len(items) != 1 {
		t.Fatalf("long field files=%v err=%v", items, err)
	}
}
