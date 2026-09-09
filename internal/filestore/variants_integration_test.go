//go:build integration

package filestore

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestVariantInsertFailureQueuesUploadedObjectOutsideAbortedTransaction(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := image.NewRGBA(image.Rect(0, 0, 4, 4))
	source.Set(0, 0, color.White)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	sourceKey := "objects/" + uuid.NewString()
	if err := store.Put(ctx, sourceKey, bytes.NewReader(encoded.Bytes())); err != nil {
		t.Fatal(err)
	}
	service := &Service{pool: pool, objects: store, options: Options{MaximumBytes: 1 << 20, MaximumImagePixels: 1_000_000, Now: time.Now}}
	key, err := service.variantObject(ctx, File{ID: uuid.New(), ObjectKey: sourceKey, ContentType: "image/png"}, "2x2")
	if err == nil {
		t.Fatal("variant insert unexpectedly succeeded")
	}
	if key == "" {
		t.Fatal("uploaded variant key was not retained for cleanup")
	}
	var queued bool
	if err := pool.QueryRow(ctx, `select exists(select 1 from pgbase_meta.file_object_deletions where object_key=$1)`, key).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if !queued {
		t.Fatalf("orphaned variant %q was not queued", key)
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
