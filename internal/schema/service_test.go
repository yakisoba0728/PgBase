package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestCatalogSnapshotsCannotMutatePublishedCache(t *testing.T) {
	cache := &Cache{}
	catalog := cacheTestCatalog(4, "posts")
	posts := catalog.Collections["posts"]
	posts.Fields = []Field{{
		ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("posts.title")), Name: "title", ColumnName: "title",
		Type: TypeText, Default: json.RawMessage(`"safe"`), Ordinal: 0,
	}}
	catalog.Collections["posts"] = posts
	if err := cache.Replace(catalog); err != nil {
		t.Fatal(err)
	}

	first := cache.Load()
	first.Collections["pages"] = Collection{Name: "pages"}
	loadedPosts := first.Collections["posts"]
	loadedPosts.Fields[0].Name = "changed"
	loadedPosts.Fields[0].Default[1] = 'X'
	first.Collections["posts"] = loadedPosts

	second := cache.Load()
	if len(second.Collections) != 1 || second.Collections["posts"].Fields[0].Name != "title" || string(second.Collections["posts"].Fields[0].Default) != `"safe"` {
		t.Fatalf("published cache was mutated through Load: %+v", second)
	}
}

func TestCatalogCacheDoesNotRegressToAnOlderVersion(t *testing.T) {
	cache := &Cache{}
	if err := cache.Replace(cacheTestCatalog(2, "pages")); err != nil {
		t.Fatal(err)
	}
	if err := cache.Replace(cacheTestCatalog(1, "posts")); err != nil {
		t.Fatal(err)
	}

	current := cache.Load()
	if current.Version != 2 {
		t.Fatalf("cache version = %d, want 2", current.Version)
	}
	if _, ok := current.Collections["pages"]; !ok {
		t.Fatalf("newer catalog was replaced by older snapshot: %+v", current)
	}
}

func TestCatalogCacheRejectsInvalidPublicationAndCompilesOneSnapshotPerVersion(t *testing.T) {
	cache := &Cache{}
	catalog := cacheTestCatalog(8, "posts")
	if err := cache.Replace(catalog); err != nil {
		t.Fatal(err)
	}
	first := cache.current.Load()
	if first == nil {
		t.Fatal("cache did not publish a compiled snapshot")
	}
	if err := cache.Replace(cloneCatalog(catalog)); err != nil {
		t.Fatal(err)
	}
	if second := cache.current.Load(); second != first {
		t.Fatal("same catalog version was compiled and published more than once")
	}

	invalid := cacheTestCatalog(9, "broken")
	broken := invalid.Collections["broken"]
	broken.ID = uuid.Nil
	invalid.Collections["broken"] = broken
	if err := cache.Replace(invalid); !errors.Is(err, ErrConflict) {
		t.Fatalf("Replace(invalid) error = %v, want ErrConflict", err)
	}
	if current := cache.current.Load(); current != first {
		t.Fatal("invalid catalog changed the published snapshot")
	}
}

func TestCatalogCacheSharesPreparedSnapshotAndDiscardsRollback(t *testing.T) {
	cache := &Cache{}
	catalog := cacheTestCatalog(7, "posts")
	const workers = 32
	results := make(chan *compiledCatalog, workers)
	errors := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			compiled, err := cache.prepare(cloneCatalog(catalog), maximumCatalogWireBytes)
			results <- compiled
			errors <- err
		}()
	}
	group.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("prepare catalog: %v", err)
		}
	}
	var prepared *compiledCatalog
	for compiled := range results {
		if prepared == nil {
			prepared = compiled
		}
		if compiled != prepared {
			t.Fatal("one catalog version produced multiple compiled snapshots")
		}
	}
	if cache.current.Load() != nil {
		t.Fatal("prepared catalog was published before commit")
	}
	if found := cache.find(catalog.Version); found != prepared {
		t.Fatal("prepared snapshot was not reusable by a committed-version reader")
	}

	cache.discardPrepared(prepared)
	replacement, err := cache.prepare(cacheTestCatalog(catalog.Version, "pages"), maximumCatalogWireBytes)
	if err != nil {
		t.Fatal(err)
	}
	if replacement == prepared {
		t.Fatal("rolled-back prepared snapshot survived discard")
	}
	if _, ok := replacement.catalog.Collections["pages"]; !ok {
		t.Fatalf("replacement prepared catalog = %+v", replacement.catalog)
	}
}

func TestCatalogCacheReplacesDifferentRolledBackCandidateBeforeDiscard(t *testing.T) {
	cache := &Cache{}
	rolledBack, err := cache.prepare(cacheTestCatalog(8, "posts"), maximumCatalogWireBytes)
	if err != nil {
		t.Fatal(err)
	}

	committing, err := cache.prepare(cacheTestCatalog(8, "pages"), maximumCatalogWireBytes)
	if err != nil {
		t.Fatal(err)
	}
	if committing == rolledBack {
		t.Fatal("different metadata for one candidate version reused a rolled-back compiled snapshot")
	}
	cache.discardPrepared(rolledBack)
	if found := cache.find(8); found != committing {
		t.Fatal("late rollback cleanup discarded the replacement candidate")
	}
}

func TestCatalogWireLimitIsExactlySixteenMiB(t *testing.T) {
	const sixteenMiB = 16 * 1024 * 1024
	if maximumCatalogWireBytes != sixteenMiB {
		t.Fatalf("catalog wire limit = %d, want %d", maximumCatalogWireBytes, sixteenMiB)
	}
	if err := validateCatalogWireSizeLimit(maximumCatalogWireBytes, maximumCatalogWireBytes); err != nil {
		t.Fatalf("exact limit rejected: %v", err)
	}
	if err := validateCatalogWireSizeLimit(maximumCatalogWireBytes+1, maximumCatalogWireBytes); !errors.Is(err, ErrCatalogLimitExceeded) {
		t.Fatalf("limit+1 error = %v, want ErrCatalogLimitExceeded", err)
	}
}

func TestCompiledCatalogWireLimitAppliesToTheCanonicalSnapshot(t *testing.T) {
	catalog := cacheTestCatalog(3, "posts")
	wire, err := marshalCatalog(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compileCatalogWithLimit(catalog, len(wire)); err != nil {
		t.Fatalf("exact canonical snapshot limit rejected: %v", err)
	}
	var encoderOutput bytes.Buffer
	if err := json.NewEncoder(&encoderOutput).Encode(catalog); err != nil {
		t.Fatal(err)
	}
	if encoderOutput.Len() != len(wire)+1 || encoderOutput.Bytes()[encoderOutput.Len()-1] != '\n' {
		t.Fatalf("encoder output = %d bytes, canonical snapshot = %d bytes", encoderOutput.Len(), len(wire))
	}
	if _, err := compileCatalogWithLimit(catalog, len(wire)-1); !errors.Is(err, ErrCatalogLimitExceeded) {
		t.Fatalf("limit below canonical snapshot error = %v, want ErrCatalogLimitExceeded", err)
	}
}

func TestCatalogLimitUsesBudgetTelemetryOutcome(t *testing.T) {
	if got := schemaOperationOutcome(ErrCatalogLimitExceeded); got != observability.OutcomeBudget {
		t.Fatalf("catalog limit outcome = %q, want %q", got, observability.OutcomeBudget)
	}
}

func cacheTestCatalog(version int64, name string) Catalog {
	return Catalog{
		Version: version,
		Collections: map[string]Collection{name: {
			ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(name)), Name: name, TableName: name,
		}},
		Incoming: map[uuid.UUID][]RelationRef{},
	}
}

type schemaTelemetryRecorder struct {
	mu     sync.Mutex
	events []observability.Event
}

func TestSchemaOperationPanicIsRecordedAsFailureAndRepanics(t *testing.T) {
	recorder := &schemaTelemetryRecorder{}
	service := &Service{recorder: recorder}
	panicValue := errors.New("schema panic sentinel")
	recovered := func() (value any) {
		defer func() { value = recover() }()
		var operationErr error
		defer service.finishOperation(context.Background(), observability.OperationSchemaUpdate, time.Now(), &operationErr)
		panic(panicValue)
	}()
	if recovered != panicValue {
		t.Fatalf("recovered panic = %v, want original panic", recovered)
	}
	events := recorder.snapshot()
	if len(events) != 1 || events[0].Operation() != observability.OperationSchemaUpdate || events[0].Outcome() != observability.OutcomeFailure {
		t.Fatalf("panic telemetry = %#v", events)
	}
}

func (r *schemaTelemetryRecorder) Record(_ context.Context, event observability.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *schemaTelemetryRecorder) snapshot() []observability.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]observability.Event(nil), r.events...)
}

type schemaCounts struct {
	version     int64
	collections int64
	fields      int64
	migrations  int64
}

func newSchemaServiceTest(t *testing.T) (*database.Pool, *Service, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	administratorID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000001")
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.admins (id, email, password_hash, active, created_at, updated_at)
		values ($1, 'schema-test@example.com', 'test', true, current_timestamp, current_timestamp)`, administratorID); err != nil {
		t.Fatalf("insert administrator: %v", err)
	}
	return pool, NewService(pool, &Cache{}, nil), administratorID
}

func applyFreshPlan(t *testing.T, service *Service, input CreateCollectionInput, administratorID uuid.UUID) Collection {
	t.Helper()
	plan, err := service.PlanCreate(context.Background(), input)
	if err != nil {
		t.Fatalf("plan create: %v", err)
	}
	collection, err := service.ApplyCreate(context.Background(), input, plan.Checksum, plan.BaseVersion, administratorID)
	if err != nil {
		t.Fatalf("apply create: %v", err)
	}
	return collection
}

func assertSchemaCounts(t *testing.T, pool *database.Pool, want schemaCounts) {
	t.Helper()
	var got schemaCounts
	ctx := context.Background()
	if err := pool.QueryRow(ctx, `select version from pgbase_meta.schema_state where singleton`).Scan(&got.version); err != nil {
		t.Fatal(err)
	}
	for query, destination := range map[string]*int64{
		`select count(*) from pgbase_meta.collections`:       &got.collections,
		`select count(*) from pgbase_meta.fields`:            &got.fields,
		`select count(*) from pgbase_meta.schema_migrations`: &got.migrations,
	} {
		if err := pool.QueryRow(ctx, query).Scan(destination); err != nil {
			t.Fatal(err)
		}
	}
	if got != want {
		t.Fatalf("schema counts = %+v, want %+v", got, want)
	}
}

func waitForAdvisoryLockWaiter(t *testing.T, pool *database.Pool) {
	waitForAdvisoryLockWaiters(t, pool, 1)
}

func waitForAdvisoryLockWaiters(t *testing.T, pool *database.Pool, wanted int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiters int
		if err := pool.QueryRow(context.Background(), `select count(*) from pg_stat_activity
			where datname = current_database() and wait_event_type = 'Lock' and wait_event = 'advisory'`).Scan(&waiters); err != nil {
			t.Fatalf("read advisory lock waiters: %v", err)
		}
		if waiters >= wanted {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("advisory lock waiters did not reach %d", wanted)
}

func waitForPinnedSchemaStateReader(t *testing.T, pool *database.Pool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiters int
		if err := pool.QueryRow(context.Background(), `select count(*) from pg_stat_activity
			where datname = current_database()
			  and wait_event_type = 'Lock'
			  and query like '%pgbase_meta.schema_state%'
			  and query like '%FOR SHARE%'`).Scan(&waiters); err != nil {
			t.Fatalf("read schema_state shared lock waiters: %v", err)
		}
		if waiters >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("catalog reader did not wait on the pinned schema_state row")
}

func waitForSchemaStateLockWaiter(t *testing.T, pool *database.Pool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiters int
		if err := pool.QueryRow(context.Background(), `select count(*) from pg_stat_activity
			where datname = current_database()
			  and wait_event_type = 'Lock'
			  and query like '%pgbase_meta.schema_state%'
			  and query like '%FOR UPDATE%'`).Scan(&waiters); err != nil {
			t.Fatalf("read schema_state lock waiters: %v", err)
		}
		if waiters >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("schema writer did not wait on the pinned schema_state row")
}
