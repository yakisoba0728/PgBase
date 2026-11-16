//go:build integration

package filestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

type cleanupBudgetValueKey struct{}

type interruptedUploadStore struct {
	ObjectStore
	cancel  context.CancelFunc
	deletes chan context.Context
	resume  chan struct{}
}

func (s *interruptedUploadStore) Put(ctx context.Context, key string, body io.Reader) error {
	if err := s.ObjectStore.Put(ctx, key, body); err != nil {
		return err
	}
	s.cancel()
	return context.Canceled
}

func (s *interruptedUploadStore) Delete(ctx context.Context, _ string) error {
	s.deletes <- ctx
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.resume:
		return errors.New("test released stalled storage")
	}
}

func TestUploadFailureCleanupBoundsSlowDeletionAndRetainsDurableIntent(t *testing.T) {
	pool, service, existing, principal, originalStore := newClosureFile(t, "attachment")
	parent := context.WithValue(t.Context(), cleanupBudgetValueKey{}, "storage-lease-observation")
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	blocked := &interruptedUploadStore{ObjectStore: originalStore, cancel: cancel, deletes: make(chan context.Context, 1), resume: make(chan struct{})}
	service.objects = blocked
	t.Cleanup(func() {
		select {
		case <-blocked.resume:
		default:
			close(blocked.resume)
		}
	})
	body := []byte("interrupted after writing these bytes")
	done := make(chan error, 1)
	go func() {
		_, err := service.Upload(ctx, principal, Upload{
			CollectionID: existing.CollectionID, RecordID: existing.RecordID, Field: existing.Field,
			Filename: "interrupted.png", ContentType: "image/png", Size: int64(len(body)),
			SHA256: sha256.Sum256(body), Body: bytes.NewReader(body),
		})
		done <- err
	}()
	var cleanup context.Context
	select {
	case cleanup = <-blocked.deletes:
	case err := <-done:
		t.Fatalf("upload returned without attempting durable cleanup: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("failed upload never reached storage cleanup")
	}
	if cleanup.Value(cleanupBudgetValueKey{}) != "storage-lease-observation" {
		t.Error("cleanup discarded request-scoped storage metadata")
	}
	if ctx.Err() != context.Canceled || cleanup.Err() != nil {
		t.Errorf("request cancellation leaked into independent cleanup: request=%v cleanup=%v", ctx.Err(), cleanup.Err())
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("upload replaced its original cancellation with %v", err)
		}
	case <-time.After(3 * time.Second):
		close(blocked.resume)
		<-done
		t.Fatal("failed upload remained blocked in detached storage cleanup")
	}
	if !errors.Is(cleanup.Err(), context.DeadlineExceeded) {
		t.Fatalf("storage cleanup did not exhaust an independent deadline: %v", cleanup.Err())
	}
	var key, status string
	var queued bool
	if err := pool.QueryRow(t.Context(), `select object_key,status,exists(select 1 from pgbase_meta.file_object_deletions d where d.object_key=f.object_key) from pgbase_meta.record_files f where filename='interrupted.png'`).Scan(&key, &status, &queued); err != nil {
		t.Fatal(err)
	}
	if status != "deleted" || !queued {
		t.Fatalf("timed-out physical cleanup lost durable intent: status=%q queued=%t", status, queued)
	}
	reader, err := originalStore.Open(t.Context(), key)
	if err != nil {
		t.Fatalf("fixture failed to retain interrupted object: %v", err)
	}
	_ = reader.Close()
	service.objects = originalStore
	if worked, err := service.DrainOne(t.Context()); err != nil || !worked {
		t.Fatalf("later cleanup could not resume durable work: worked=%t err=%v", worked, err)
	}
	if reader, err := originalStore.Open(t.Context(), key); !errors.Is(err, ErrNotFound) {
		if reader != nil {
			_ = reader.Close()
		}
		t.Fatalf("retry left interrupted bytes: %v", err)
	}
	if reader, err := originalStore.Open(t.Context(), existing.ObjectKey); err != nil {
		t.Fatalf("cleanup removed an unrelated ready file: %v", err)
	} else {
		_ = reader.Close()
	}
}

func TestUploadFailureCleanupBoundsExhaustedPool(t *testing.T) {
	databaseURL, err := url.Parse(testutil.NewDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	query := databaseURL.Query()
	query.Set("pool_max_conns", "4")
	databaseURL.RawQuery = query.Encode()
	pool, err := database.Open(t.Context(), databaseURL.String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	var held []*pgxpool.Conn
	release := func() {
		for _, conn := range held {
			conn.Release()
		}
		held = nil
	}
	t.Cleanup(release)
	for range 4 {
		conn, err := pool.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, conn)
	}
	before := pool.Stats().CanceledAcquireCount
	done := make(chan struct{})
	go func() {
		(&Service{pool: pool}).failUpload(context.WithoutCancel(t.Context()), File{ID: uuid.New()})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		release()
		<-done
		t.Fatal("failed upload cleanup waited indefinitely for an exhausted request pool")
	}
	if pool.Stats().CanceledAcquireCount <= before {
		t.Fatal("cleanup did not cancel its waiting pool acquisition")
	}
}

// A cleanup deadline without durable intent loses the only reference to bytes
// if the publishing transaction settles after that deadline by rolling back.
func TestVariantCleanupQueuesAfterDelayedPublisherRollback(t *testing.T) {
	ctx := t.Context()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	// Isolate the publisher outcome without unrelated record metadata.
	if _, err := pool.Exec(ctx, `alter table pgbase_meta.record_file_variants drop constraint record_file_variants_file_id_fkey`); err != nil {
		t.Fatal(err)
	}
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fileID, key, variant := uuid.New(), "thumbnails/"+uuid.NewString(), "2x2"
	if err := store.Put(ctx, key, bytes.NewBufferString("uncommitted variant")); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, fileID.String()+"/"+variant); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `insert into pgbase_meta.record_file_variants(file_id,variant,object_key,created_at) values($1,$2,$3,current_timestamp)`, fileID, variant, key); err != nil {
		t.Fatal(err)
	}
	service := &Service{pool: pool, objects: store}
	done := make(chan error, 1)
	go func() { done <- service.queueUnreferencedVariant(context.WithoutCancel(ctx), fileID, variant, key) }()
	// Settle strictly after the rejected one-second cleanup budget. This is
	// not a performance assertion; it distinguishes the two outcome paths.
	<-time.After(1500 * time.Millisecond)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var cleanupErr error
	select {
	case cleanupErr = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cleanup did not settle after publisher rollback")
	}
	var queued bool
	if err := pool.QueryRow(ctx, `select exists(select 1 from pgbase_meta.file_object_deletions where object_key=$1)`, key).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if !queued || cleanupErr != nil {
		t.Fatalf("aborted variant lost durable cleanup intent: queued=%t cleanup=%v", queued, cleanupErr)
	}
	if worked, err := service.DrainOne(ctx); err != nil || !worked {
		t.Fatalf("queued variant cleanup failed: worked=%t err=%v", worked, err)
	}
	if reader, err := store.Open(ctx, key); !errors.Is(err, ErrNotFound) {
		if reader != nil {
			_ = reader.Close()
		}
		t.Fatalf("rolled-back variant bytes survived durable cleanup: %v", err)
	}
}
