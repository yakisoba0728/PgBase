//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type blockingCreateHook struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (h *blockingCreateHook) BeforeMutation(ctx context.Context, event Mutation) error {
	if event.Operation != "create" {
		return nil
	}
	h.once.Do(func() { close(h.entered) })
	select {
	case <-h.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (*blockingCreateHook) AfterMutation(context.Context, Mutation) {}

func TestTruncateLinearizesAfterInFlightCreate(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	collectionID := recordOperationsCollectionID(t, ctx, schemas)
	hook := &blockingCreateHook{entered: make(chan struct{}), release: make(chan struct{})}
	records.SetHooks([]MutationHook{hook})

	createDone := make(chan error, 1)
	go func() {
		_, err := records.Create(ctx, CreateRequest{
			Principal: principal, RequestID: "truncate-create-before", Collection: "records",
			Input: map[string]json.RawMessage{"title": json.RawMessage(`"created-before-truncate"`)},
		})
		createDone <- err
	}()
	mustReceiveSignal(t, ctx, hook.entered, "create hook")

	truncateDone := make(chan error, 1)
	go func() {
		_, err := records.Truncate(ctx, TruncateRequest{
			Principal: principal, RequestID: "truncate-after-create", Collection: "records",
		})
		truncateDone <- err
	}()
	collectionLock := testAdvisoryLockKey(t, ctx, pool, collectionMutationLockDomain+collectionID.String())
	mustObserveAdvisoryWaiter(t, ctx, pool, collectionLock)

	close(hook.release)
	if err := mustReceiveError(t, ctx, createDone, "create"); err != nil {
		t.Fatal(err)
	}
	if err := mustReceiveError(t, ctx, truncateDone, "truncate"); err != nil {
		t.Fatal(err)
	}
	assertRecordCount(t, ctx, pool, 0)
}

func TestCreateLinearizesAfterInFlightTruncate(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "truncate-seed", Collection: "records",
		Input: map[string]json.RawMessage{"title": json.RawMessage(`"truncate-seed"`)},
	}); err != nil {
		t.Fatal(err)
	}
	collectionID := recordOperationsCollectionID(t, ctx, schemas)
	collectionLock := testAdvisoryLockKey(t, ctx, pool, collectionMutationLockDomain+collectionID.String())
	barrierLock := testAdvisoryLockKey(t, ctx, pool, "truncate-test-delete-barrier:"+collectionID.String())
	barrier, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer barrier.Release()
	barrierHeld := true
	defer func() {
		if barrierHeld {
			_, _ = barrier.Exec(context.Background(), "select pg_advisory_unlock($1)", barrierLock)
		}
	}()
	if _, err := barrier.Exec(ctx, "select pg_advisory_lock($1)", barrierLock); err != nil {
		t.Fatal(err)
	}
	triggerSQL := fmt.Sprintf(`
		create function app.block_truncate_delete() returns trigger language plpgsql as $$
		begin
			perform pg_catalog.pg_advisory_xact_lock(%d);
			return old;
		end $$;
		create trigger block_truncate_delete before delete on app.records
		for each row execute function app.block_truncate_delete()`, barrierLock)
	if _, err := pool.Exec(ctx, triggerSQL); err != nil {
		t.Fatal(err)
	}

	truncateDone := make(chan error, 1)
	go func() {
		_, err := records.Truncate(ctx, TruncateRequest{
			Principal: principal, RequestID: "truncate-before-create", Collection: "records",
		})
		truncateDone <- err
	}()
	mustObserveAdvisoryWaiter(t, ctx, pool, barrierLock)

	createDone := make(chan error, 1)
	go func() {
		_, err := records.Create(ctx, CreateRequest{
			Principal: principal, RequestID: "create-after-truncate", Collection: "records",
			Input: map[string]json.RawMessage{"title": json.RawMessage(`"created-after-truncate"`)},
		})
		createDone <- err
	}()
	mustObserveAdvisoryWaiter(t, ctx, pool, collectionLock)

	var unlocked bool
	if err := barrier.QueryRow(ctx, "select pg_advisory_unlock($1)", barrierLock).Scan(&unlocked); err != nil || !unlocked {
		t.Fatalf("release truncate barrier: unlocked=%t err=%v", unlocked, err)
	}
	barrierHeld = false
	if err := mustReceiveError(t, ctx, truncateDone, "truncate"); err != nil {
		t.Fatal(err)
	}
	if err := mustReceiveError(t, ctx, createDone, "create"); err != nil {
		t.Fatal(err)
	}
	assertRecordCount(t, ctx, pool, 1)
	var title string
	if err := pool.QueryRow(ctx, `select title from app.records`).Scan(&title); err != nil || title != "created-after-truncate" {
		t.Fatalf("surviving title=%q err=%v", title, err)
	}
}

func TestCreateCollectionMutationLockWaitHonorsCancellation(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	collectionID := recordOperationsCollectionID(t, ctx, schemas)
	collectionLock := testAdvisoryLockKey(t, ctx, pool, collectionMutationLockDomain+collectionID.String())
	blocker, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Release()
	if _, err := blocker.Exec(ctx, "select pg_advisory_lock($1)", collectionLock); err != nil {
		t.Fatal(err)
	}
	defer blocker.Exec(context.Background(), "select pg_advisory_unlock($1)", collectionLock)

	createCtx, cancelCreate := context.WithCancel(ctx)
	createDone := make(chan error, 1)
	go func() {
		_, err := records.Create(createCtx, CreateRequest{
			Principal: principal, RequestID: "cancel-waiting-create", Collection: "records",
			Input: map[string]json.RawMessage{"title": json.RawMessage(`"must-not-commit"`)},
		})
		createDone <- err
	}()
	mustObserveAdvisoryWaiter(t, ctx, pool, collectionLock)
	cancelCreate()
	if err := mustReceiveError(t, ctx, createDone, "cancelled create"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled create error=%v", err)
	}
	assertRecordCount(t, ctx, pool, 0)
}

var errTruncateHookVeto = errors.New("truncate hook veto")

type secondDeleteVetoHook struct {
	mu     sync.Mutex
	before int
	after  int
}

func (h *secondDeleteVetoHook) BeforeMutation(_ context.Context, event Mutation) error {
	if event.Operation != "delete" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.before++
	if h.before == 2 {
		return errTruncateHookVeto
	}
	return nil
}

func (h *secondDeleteVetoHook) AfterMutation(_ context.Context, event Mutation) {
	if event.Operation != "delete" {
		return
	}
	h.mu.Lock()
	h.after++
	h.mu.Unlock()
}

func TestTruncateHookVetoRollsBackRecordsAndDurableArtifacts(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for index, title := range []string{"veto-one", "veto-two"} {
		if _, err := records.Create(ctx, CreateRequest{
			Principal: principal, RequestID: fmt.Sprintf("truncate-veto-seed-%d", index), Collection: "records",
			Input: map[string]json.RawMessage{"title": json.RawMessage(`"` + title + `"`)},
		}); err != nil {
			t.Fatal(err)
		}
	}
	before := durableRecordArtifactCounts(t, ctx, pool)
	hook := &secondDeleteVetoHook{}
	records.SetHooks([]MutationHook{hook})
	_, err := records.Truncate(ctx, TruncateRequest{
		Principal: principal, RequestID: "truncate-veto", Collection: "records",
	})
	if !errors.Is(err, errTruncateHookVeto) {
		t.Fatalf("truncate veto error=%v", err)
	}
	assertRecordCount(t, ctx, pool, 2)
	after := durableRecordArtifactCounts(t, ctx, pool)
	if before != after {
		t.Fatalf("durable artifacts changed across rollback: before=%+v after=%+v", before, after)
	}
	hook.mu.Lock()
	defer hook.mu.Unlock()
	if hook.before != 2 || hook.after != 0 {
		t.Fatalf("hook calls before=%d after=%d", hook.before, hook.after)
	}
}

type artifactCounts struct {
	revisions int
	audit     int
	outbox    int
}

func durableRecordArtifactCounts(t *testing.T, ctx context.Context, pool *database.Pool) artifactCounts {
	t.Helper()
	var counts artifactCounts
	if err := pool.QueryRow(ctx, `select
		(select count(*) from pgbase_meta.record_revisions),
		(select count(*) from pgbase_meta.record_audit_events),
		(select count(*) from pgbase_meta.record_outbox)`).Scan(&counts.revisions, &counts.audit, &counts.outbox); err != nil {
		t.Fatal(err)
	}
	return counts
}

func recordOperationsCollectionID(t *testing.T, ctx context.Context, schemas *schema.Service) uuid.UUID {
	t.Helper()
	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	collection, ok := catalog.Collections["records"]
	if !ok {
		t.Fatal("records collection is missing")
	}
	return collection.ID
}

func testAdvisoryLockKey(t *testing.T, ctx context.Context, pool *database.Pool, name string) int64 {
	t.Helper()
	var key int64
	if err := pool.QueryRow(ctx, `select pg_catalog.hashtextextended($1, 0)`, name).Scan(&key); err != nil {
		t.Fatal(err)
	}
	return key
}

func mustObserveAdvisoryWaiter(t *testing.T, ctx context.Context, pool *database.Pool, key int64) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		err := pool.QueryRow(ctx, `with expected(value) as (values ($1::bigint))
			select exists (
				select 1 from pg_catalog.pg_locks, expected
				where locktype='advisory' and database=(select oid from pg_catalog.pg_database where datname=current_database())
				  and objsubid=1 and classid::bigint=((value >> 32) & 4294967295)
				  and objid::bigint=(value & 4294967295) and not granted
			)`, key).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("advisory lock %d never had a waiter: %v", key, ctx.Err())
		case <-ticker.C:
		}
	}
}

func mustReceiveSignal(t *testing.T, ctx context.Context, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatalf("wait for %s: %v", name, ctx.Err())
	}
}

func mustReceiveError(t *testing.T, ctx context.Context, result <-chan error, name string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		t.Fatalf("wait for %s: %v", name, ctx.Err())
		return ctx.Err()
	}
}

func assertRecordCount(t *testing.T, ctx context.Context, pool *database.Pool, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `select count(*) from app.records`).Scan(&count); err != nil || count != want {
		t.Fatalf("record count=%d want=%d err=%v", count, want, err)
	}
}
