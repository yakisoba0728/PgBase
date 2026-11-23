//go:build integration

package filestore

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestDeletingFileQueuesAllVariantObjectsInTheSameTransaction(t *testing.T) {
	ctx := t.Context()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	// Isolate deletion/queue atomicity from unrelated record authorization.
	if _, err := pool.Exec(ctx, `alter table pgbase_meta.record_file_variants drop constraint record_file_variants_file_id_fkey`); err != nil {
		t.Fatal(err)
	}
	file := File{ID: uuid.New(), ObjectKey: "objects/" + uuid.NewString(), ThumbnailKey: "thumbnails/" + uuid.NewString()}
	for _, variant := range []string{"1x1", "2x2"} {
		if _, err := pool.Exec(ctx, `insert into pgbase_meta.record_file_variants(file_id,variant,object_key,created_at) values($1,$2,$3,current_timestamp)`, file.ID, variant, "thumbnails/"+uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	rollback := errors.New("rollback")
	err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := enqueueFileObjects(ctx, tx, file); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback = %v", err)
	}
	assertState := func(wantVariants, wantQueued int) {
		t.Helper()
		var variants, queued int
		if err := pool.QueryRow(ctx, `select (select count(*) from pgbase_meta.record_file_variants),(select count(*) from pgbase_meta.file_object_deletions)`).Scan(&variants, &queued); err != nil {
			t.Fatal(err)
		}
		if variants != wantVariants || queued != wantQueued {
			t.Fatalf("variants/queued = %d/%d, want %d/%d", variants, queued, wantVariants, wantQueued)
		}
	}
	assertState(2, 0)
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error { return enqueueFileObjects(ctx, tx, file) }); err != nil {
		t.Fatal(err)
	}
	assertState(0, 4)
}

func TestVariantRejectsMissingFileBeforeUploading(t *testing.T) {
	ctx := t.Context()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{pool: pool, objects: store, options: Options{MaximumBytes: 1 << 20, MaximumImagePixels: 1_000_000, Now: time.Now}}
	key, err := service.variantObject(ctx, File{ID: uuid.New(), ObjectKey: "objects/" + uuid.NewString(), ContentType: "image/png"}, "2x2")
	if !errors.Is(err, ErrNotFound) || key != "" {
		t.Fatalf("key=%q err=%v", key, err)
	}
}

// A cached variant is served from one autocommit read: no storage lease,
// reservation intent, row lock or commit. A writer holding the file row (for
// example a delete waiting on its own locks) must not delay it.
func TestCachedVariantHitTakesNoLockAndWritesNothing(t *testing.T) {
	ctx := t.Context()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{pool: pool, objects: store, options: Options{MaximumBytes: 1 << 20, MaximumImagePixels: 1_000_000, Now: time.Now}}
	file := File{ID: uuid.New(), ObjectKey: "objects/" + uuid.NewString(), ContentType: "image/png"}
	cached := "thumbnails/" + uuid.NewString()
	// Replica mode skips the record foreign key; the hit path never reads it.
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `set local session_replication_role = replica`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into pgbase_meta.record_files(id,collection_id,record_id,field_name,filename,content_type,size_bytes,sha256,object_key,status,created_at,ready_at)
			values($1,$2,$3,'files','f.png','image/png',1,decode(repeat('aa',32),'hex'),$4,'ready',now(),now())`, file.ID, uuid.New(), uuid.New(), file.ObjectKey); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into pgbase_meta.record_file_variants(file_id,variant,object_key,created_at) values($1,'2x2',$2,now())`, file.ID, cached)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(context.Background()) }()
	if _, err := holder.Exec(ctx, `select 1 from pgbase_meta.record_files where id=$1 for update`, file.ID); err != nil {
		t.Fatal(err)
	}
	hitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	key, err := service.variantObject(hitCtx, file, "2x2")
	if err != nil || key != cached {
		t.Fatalf("cached variant key=%q err=%v, want %q", key, err, cached)
	}
	var intents int
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.file_object_deletions`).Scan(&intents); err != nil || intents != 0 {
		t.Fatalf("cache hit left %d object intents (err=%v)", intents, err)
	}
	// A file that is no longer ready is not served from its stale variant row.
	if _, err := holder.Exec(ctx, `update pgbase_meta.record_files set status='deleted',deleted_at=now() where id=$1`, file.ID); err != nil {
		t.Fatal(err)
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if key, err := service.variantObject(ctx, file, "2x2"); !errors.Is(err, ErrNotFound) || key != "" {
		t.Fatalf("variant of deleted file key=%q err=%v", key, err)
	}
}

func TestVariantCleanupWaitsForPublisherCommitBeforeReferenceCheck(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	// Isolate the advisory-lock behavior without constructing unrelated record
	// metadata; the production uniqueness and object-key constraints remain.
	if _, err := pool.Exec(ctx, `alter table pgbase_meta.record_file_variants drop constraint record_file_variants_file_id_fkey`); err != nil {
		t.Fatal(err)
	}
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{pool: pool, objects: store}
	fileID, key, variant := uuid.New(), "thumbnails/"+uuid.NewString(), "2x2"
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.file_object_deletions(object_key,queued_at,retain_on_success) values($1,now(),true)`, key); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, key, bytes.NewBufferString("committed variant")); err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := lockObjectReference(ctx, tx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `insert into pgbase_meta.record_file_variants(file_id,variant,object_key,created_at) values($1,$2,$3,CURRENT_TIMESTAMP)`, fileID, variant, key); err != nil {
		t.Fatal(err)
	}
	// The production worker defers this intent while the publisher owns the
	// reference lock, even though its uncommitted reference is not yet visible.
	if worked, err := service.DrainOne(ctx); err != nil || worked {
		t.Fatalf("cleanup crossed pending publisher: worked=%t err=%v", worked, err)
	}
	requireVariantBytes(t, store, key, true)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if worked, err := service.DrainOne(ctx); err != nil || !worked {
		t.Fatalf("committed reference cleanup: worked=%t err=%v", worked, err)
	}
	requireVariantBytes(t, store, key, true)
	var queued bool
	if err := pool.QueryRow(ctx, `select exists(select 1 from pgbase_meta.file_object_deletions where object_key=$1)`, key).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued {
		t.Fatal("committed variant retained its obsolete cleanup intent")
	}
}
