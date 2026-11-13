//go:build integration

package filestore

import (
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
	service := &Service{pool: pool}
	fileID, key, variant := uuid.New(), "thumbnails/"+uuid.NewString(), "2x2"
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, fileID.String()+"/"+variant); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `insert into pgbase_meta.record_file_variants(file_id,variant,object_key,created_at) values($1,$2,$3,CURRENT_TIMESTAMP)`, fileID, variant, key); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- service.queueUnreferencedVariant(ctx, fileID, variant, key) }()
	select {
	case err := <-done:
		t.Fatalf("cleanup did not wait for publisher: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var queued bool
	if err := pool.QueryRow(ctx, `select exists(select 1 from pgbase_meta.file_object_deletions where object_key=$1)`, key).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued {
		t.Fatal("committed variant object was queued for deletion")
	}
}
