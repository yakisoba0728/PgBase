package schema

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestApplyCreatePersistsCatalogDDLAndAuditAtomically(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	input := CreateCollectionInput{
		Name: "posts",
		Fields: []FieldInput{
			{Name: "title", Type: TypeText, Required: true, Indexed: true},
			{Name: "views", Type: TypeBigInt, Default: json.RawMessage(`"0"`)},
		},
	}

	plan, err := service.PlanCreate(context.Background(), input)
	if err != nil {
		t.Fatalf("plan create: %v", err)
	}
	collection, err := service.ApplyCreate(context.Background(), input, plan.Checksum, plan.BaseVersion, administratorID)
	if err != nil {
		t.Fatalf("apply create: %v", err)
	}
	if collection.ID.Version() != 7 {
		t.Fatalf("collection ID version = %d, want 7", collection.ID.Version())
	}
	if len(collection.Fields) != 2 || collection.Fields[0].ID.Version() != 7 || collection.Fields[1].ID.Version() != 7 {
		t.Fatalf("field IDs are not UUIDv7: %+v", collection.Fields)
	}

	catalog, err := service.Catalog(context.Background())
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if catalog.Version != 1 {
		t.Fatalf("catalog version = %d, want 1", catalog.Version)
	}
	persisted, ok := catalog.Collections["posts"]
	if !ok || persisted.TableName != "posts" || len(persisted.Fields) != 2 {
		t.Fatalf("persisted collection = %+v, present=%t", persisted, ok)
	}
	if got := string(persisted.Fields[1].Default); got != `"0"` {
		t.Fatalf("persisted default = %s, want %s", got, `"0"`)
	}

	var physicalTable string
	if err := pool.QueryRow(context.Background(), `select coalesce(to_regclass('app.posts')::text, '')`).Scan(&physicalTable); err != nil {
		t.Fatalf("read physical table: %v", err)
	}
	if physicalTable != "app.posts" {
		t.Fatalf("physical table = %q, want app.posts", physicalTable)
	}

	var version, migrations, indexes int64
	var checksum []byte
	if err := pool.QueryRow(context.Background(), `select version from pgbase_meta.schema_state where singleton`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `select count(*) from pgbase_meta.schema_migrations`).Scan(&migrations); err != nil {
		t.Fatalf("read migration audit: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `select checksum from pgbase_meta.schema_migrations where version = 1`).Scan(&checksum); err != nil {
		t.Fatalf("read migration checksum: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `select count(*) from pgbase_meta.collection_indexes`).Scan(&indexes); err != nil {
		t.Fatalf("read indexes: %v", err)
	}
	wantChecksum, err := hex.DecodeString(plan.Checksum)
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 || migrations != 1 || indexes != 1 || !bytes.Equal(checksum, wantChecksum) {
		t.Fatalf("version=%d migrations=%d indexes=%d checksum=%x, want 1/1/1/%x", version, migrations, indexes, checksum, wantChecksum)
	}
}

func TestApplyCreateRejectsDuplicateCollectionWithoutChangingState(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	input := CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}
	applyFreshPlan(t, service, input, administratorID)

	second, err := service.PlanCreate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ApplyCreate(context.Background(), input, second.Checksum, second.BaseVersion, administratorID)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate apply error = %v, want ErrConflict", err)
	}
	assertSchemaCounts(t, pool, schemaCounts{version: 1, collections: 1, fields: 1, migrations: 1})
}

func TestApplyCreateRejectsStaleBaseVersionAndWrongChecksum(t *testing.T) {
	t.Run("explicit base version mismatch", func(t *testing.T) {
		pool, service, administratorID := newSchemaServiceTest(t, Planner{})
		input := CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}
		plan, err := service.PlanCreate(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.ApplyCreate(context.Background(), input, plan.Checksum, plan.BaseVersion+1, administratorID)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("base-version mismatch error = %v, want ErrConflict", err)
		}
		assertSchemaCounts(t, pool, schemaCounts{})
	})

	t.Run("stale base version", func(t *testing.T) {
		pool, service, administratorID := newSchemaServiceTest(t, Planner{})
		staleInput := CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}
		stalePlan, err := service.PlanCreate(context.Background(), staleInput)
		if err != nil {
			t.Fatal(err)
		}
		applyFreshPlan(t, service, CreateCollectionInput{Name: "pages"}, administratorID)

		_, err = service.ApplyCreate(context.Background(), staleInput, stalePlan.Checksum, stalePlan.BaseVersion, administratorID)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("stale apply error = %v, want ErrConflict", err)
		}
		assertSchemaCounts(t, pool, schemaCounts{version: 1, collections: 1, fields: 0, migrations: 1})
	})

	t.Run("wrong checksum", func(t *testing.T) {
		pool, service, administratorID := newSchemaServiceTest(t, Planner{})
		_, err := service.ApplyCreate(context.Background(), CreateCollectionInput{Name: "posts"}, "wrong-checksum", 0, administratorID)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("wrong checksum error = %v, want ErrConflict", err)
		}
		assertSchemaCounts(t, pool, schemaCounts{})
	})
}

func TestApplyCreateRollsBackDDLMetadataAuditAndVersionOnStatementFailure(t *testing.T) {
	plan := Plan{
		Operation:   createCollectionOperation,
		BaseVersion: 0,
		Checksum:    "invalid-ddl-checksum",
		Definition:  PlannedCollection{Name: "broken", TableName: "broken"},
		Statements: []string{
			`CREATE TABLE "app"."broken" ("id" uuid primary key)`,
			`CREATE TABLE "app"."will_not_parse" (`,
		},
	}
	pool, service, administratorID := newSchemaServiceTest(t, fixedPlanBuilder{plan: plan})
	_, err := service.ApplyCreate(context.Background(), CreateCollectionInput{Name: "broken"}, plan.Checksum, plan.BaseVersion, administratorID)
	if err == nil {
		t.Fatal("invalid DDL apply succeeded")
	}
	assertSchemaCounts(t, pool, schemaCounts{})
	var physicalTable string
	if queryErr := pool.QueryRow(context.Background(), `select coalesce(to_regclass('app.broken')::text, '')`).Scan(&physicalTable); queryErr != nil {
		t.Fatal(queryErr)
	}
	if physicalTable != "" {
		t.Fatalf("rolled-back physical table still exists: %q", physicalTable)
	}
}

func TestApplyCreateSerializesConcurrentUseOfOnePlan(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	inputs := []CreateCollectionInput{
		{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}},
		{Name: "pages", Fields: []FieldInput{{Name: "body", Type: TypeText}}},
	}
	plans := make([]Plan, len(inputs))
	for index, input := range inputs {
		plan, err := service.PlanCreate(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		plans[index] = plan
	}

	lockingConnection, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lockingConnection.Release()
	lockingTx, err := lockingConnection.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_ = lockingTx.Rollback(context.Background())
		}
	}()
	if _, err := lockingTx.Exec(context.Background(), "select pg_advisory_xact_lock($1)", schemaAdvisoryLockKey); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for index := range inputs {
		workers.Add(1)
		go func(input CreateCollectionInput, plan Plan) {
			defer workers.Done()
			<-start
			_, applyErr := service.ApplyCreate(context.Background(), input, plan.Checksum, plan.BaseVersion, administratorID)
			results <- applyErr
		}(inputs[index], plans[index])
	}
	close(start)
	waitForAdvisoryLockWaiters(t, pool, 2)
	if err := lockingTx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	locked = false
	workers.Wait()
	close(results)

	var successes, conflicts int
	for result := range results {
		switch {
		case result == nil:
			successes++
		case errors.Is(result, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent apply error: %v", result)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want 1/1", successes, conflicts)
	}
	assertSchemaCounts(t, pool, schemaCounts{version: 1, collections: 1, fields: 1, migrations: 1})
}

func TestApplyCreateValidatesCompleteCatalogCommittedBeforeLockedSnapshot(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	input := CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}
	plan, err := service.PlanCreate(context.Background(), input)
	if err != nil {
		t.Fatalf("plan create: %v", err)
	}

	lockingConnection, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire locking connection: %v", err)
	}
	defer lockingConnection.Release()
	lockingTx, err := lockingConnection.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin locking transaction: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = lockingTx.Rollback(context.Background())
		}
	}()
	if _, err := lockingTx.Exec(context.Background(), "select pg_advisory_xact_lock($1)", schemaAdvisoryLockKey); err != nil {
		t.Fatalf("acquire schema advisory lock: %v", err)
	}
	if _, err := lockingTx.Exec(context.Background(), `insert into pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at)
		values ($1, 'drifted', 'drifted', 'active', current_timestamp, current_timestamp)`,
		uuid.MustParse("019551d0-e9c0-7000-8000-000000000099")); err != nil {
		t.Fatalf("insert drifted catalog metadata: %v", err)
	}
	if _, err := lockingTx.Exec(context.Background(), `insert into pgbase_meta.fields
		(id, collection_id, name, column_name, field_type, required, is_unique, is_indexed,
		 has_default, default_json, ordinal, created_at)
		values ($1, $2, 'title', 'different_column', 'text', false, false, false,
		 false, null, 0, current_timestamp)`,
		uuid.MustParse("019551d0-e9c0-7000-8000-000000000098"),
		uuid.MustParse("019551d0-e9c0-7000-8000-000000000099")); err != nil {
		t.Fatalf("insert drifted field metadata: %v", err)
	}

	applyResult := make(chan error, 1)
	go func() {
		_, applyErr := service.ApplyCreate(context.Background(), input, plan.Checksum, plan.BaseVersion, administratorID)
		applyResult <- applyErr
	}()
	waitForAdvisoryLockWaiter(t, pool)
	select {
	case result := <-applyResult:
		t.Fatalf("apply returned before the catalog-locking transaction completed: %v", result)
	default:
	}
	if err := lockingTx.Commit(context.Background()); err != nil {
		t.Fatalf("commit drifted catalog metadata: %v", err)
	}
	committed = true

	if err := <-applyResult; !errors.Is(err, ErrConflict) {
		t.Fatalf("apply error = %v, want ErrConflict for complete-catalog drift", err)
	}
	assertSchemaCounts(t, pool, schemaCounts{collections: 1, fields: 1})
	var physicalTable string
	if err := pool.QueryRow(context.Background(), `select coalesce(to_regclass('app.posts')::text, '')`).Scan(&physicalTable); err != nil {
		t.Fatalf("read physical table: %v", err)
	}
	if physicalTable != "" {
		t.Fatalf("apply executed DDL despite catalog drift: %q", physicalTable)
	}
}

func TestCatalogSnapshotsCannotMutatePublishedCache(t *testing.T) {
	cache := &Cache{}
	cache.Replace(Catalog{Version: 4, Collections: map[string]Collection{
		"posts": {Name: "posts", Fields: []Field{{Name: "title", Default: json.RawMessage(`"safe"`)}}},
	}})

	first := cache.Load()
	first.Collections["pages"] = Collection{Name: "pages"}
	posts := first.Collections["posts"]
	posts.Fields[0].Name = "changed"
	posts.Fields[0].Default[1] = 'X'
	first.Collections["posts"] = posts

	second := cache.Load()
	if len(second.Collections) != 1 || second.Collections["posts"].Fields[0].Name != "title" || string(second.Collections["posts"].Fields[0].Default) != `"safe"` {
		t.Fatalf("published cache was mutated through Load: %+v", second)
	}
}

func TestCatalogCacheDoesNotRegressToAnOlderVersion(t *testing.T) {
	cache := &Cache{}
	cache.Replace(Catalog{Version: 2, Collections: map[string]Collection{"pages": {Name: "pages"}}})
	cache.Replace(Catalog{Version: 1, Collections: map[string]Collection{"posts": {Name: "posts"}}})

	current := cache.Load()
	if current.Version != 2 {
		t.Fatalf("cache version = %d, want 2", current.Version)
	}
	if _, ok := current.Collections["pages"]; !ok {
		t.Fatalf("newer catalog was replaced by older snapshot: %+v", current)
	}
}

func TestEnsureCurrentReturnsCatalogPinnedToCallerTransactionSnapshot(t *testing.T) {
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	firstPool, err := database.Open(ctx, databaseURL, 5*time.Second)
	if err != nil {
		t.Fatalf("open first pool: %v", err)
	}
	t.Cleanup(firstPool.Close)
	secondPool, err := database.Open(ctx, databaseURL, 5*time.Second)
	if err != nil {
		t.Fatalf("open second pool: %v", err)
	}
	t.Cleanup(secondPool.Close)
	if err := migrate.New(firstPool, nil).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	administratorID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000003")
	if _, err := firstPool.Exec(ctx, `insert into pgbase_meta.admins (id, email, password_hash, active, created_at, updated_at)
		values ($1, 'snapshot-test@example.com', 'test', true, current_timestamp, current_timestamp)`, administratorID); err != nil {
		t.Fatalf("insert administrator: %v", err)
	}

	firstCache := &Cache{}
	secondCache := &Cache{}
	firstService := NewService(firstPool, firstCache, Planner{})
	secondService := NewService(secondPool, secondCache, Planner{})
	snapshotPinned := make(chan struct{})
	newerPublished := make(chan struct{})
	type ensureResult struct {
		catalog Catalog
		err     error
	}
	result := make(chan ensureResult, 1)
	go func() {
		var catalog Catalog
		err := firstPool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
			var pinnedVersion int64
			if err := tx.QueryRow(ctx, `select version from pgbase_meta.schema_state where singleton`).Scan(&pinnedVersion); err != nil {
				return err
			}
			if pinnedVersion != 0 {
				return fmt.Errorf("pinned version = %d, want 0", pinnedVersion)
			}
			close(snapshotPinned)
			<-newerPublished
			var err error
			catalog, err = firstService.EnsureCurrent(ctx, tx)
			return err
		})
		result <- ensureResult{catalog: catalog, err: err}
	}()
	<-snapshotPinned

	applyFreshPlan(t, secondService, CreateCollectionInput{Name: "posts"}, administratorID)
	firstCache.Replace(secondCache.Load())
	close(newerPublished)

	ensured := <-result
	if ensured.err != nil {
		t.Fatalf("EnsureCurrent: %v", ensured.err)
	}
	if ensured.catalog.Version != 0 || len(ensured.catalog.Collections) != 0 {
		t.Fatalf("transaction-local catalog = %+v, want empty version 0 snapshot", ensured.catalog)
	}
	if current := firstCache.Load(); current.Version != 1 {
		t.Fatalf("opportunistic publication regressed cache to version %d, want 1", current.Version)
	}
}

func TestApplyCreateDoesNotReportFailureAfterCommitWhenRequestIsCanceled(t *testing.T) {
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	parsedURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	query := parsedURL.Query()
	query.Set("pool_max_conns", "2")
	parsedURL.RawQuery = query.Encode()
	pool, err := database.Open(ctx, parsedURL.String(), 5*time.Second)
	if err != nil {
		t.Fatalf("open constrained pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	heldConnection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("reserve pool connection: %v", err)
	}
	t.Cleanup(heldConnection.Release)
	administratorID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000004")
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.admins (id, email, password_hash, active, created_at, updated_at)
		values ($1, 'post-commit-test@example.com', 'test', true, current_timestamp, current_timestamp)`, administratorID); err != nil {
		t.Fatalf("insert administrator: %v", err)
	}

	cache := &Cache{}
	service := NewService(pool, cache, Planner{})
	input := CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}
	plan, err := service.PlanCreate(ctx, input)
	if err != nil {
		t.Fatalf("plan create: %v", err)
	}
	plannerEntered := make(chan struct{})
	plannerRelease := make(chan struct{})
	service.planner = &barrierPlanBuilder{delegate: Planner{}, entered: plannerEntered, release: plannerRelease}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	applyResult := make(chan error, 1)
	go func() {
		_, applyErr := service.ApplyCreate(requestCtx, input, plan.Checksum, plan.BaseVersion, administratorID)
		applyResult <- applyErr
	}()
	<-plannerEntered

	waiterStarted := make(chan struct{})
	waiterAcquired := make(chan *pgxpool.Conn, 1)
	waiterError := make(chan error, 1)
	go func() {
		close(waiterStarted)
		connection, acquireErr := pool.Acquire(context.Background())
		if acquireErr != nil {
			waiterError <- acquireErr
			return
		}
		waiterAcquired <- connection
	}()
	<-waiterStarted
	time.Sleep(25 * time.Millisecond)
	close(plannerRelease)

	var waiter *pgxpool.Conn
	select {
	case waiter = <-waiterAcquired:
	case err := <-waiterError:
		t.Fatalf("acquire post-commit barrier connection: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("post-commit barrier did not acquire the released transaction connection")
	}
	cancelRequest()
	waiter.Release()
	if err := <-applyResult; err != nil {
		t.Fatalf("ApplyCreate reported failure after committing schema state: %v", err)
	}

	assertSchemaCounts(t, pool, schemaCounts{version: 1, collections: 1, fields: 1, migrations: 1})
	published := cache.Load()
	if published.Version != 1 || published.Collections["posts"].Name != "posts" {
		t.Fatalf("published catalog after commit = %+v", published)
	}
}

func TestApplyUpdatePersistsCatalogDDLAndAuditAtomically(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	created := applyFreshPlan(t, service, CreateCollectionInput{
		Name: "posts",
		Fields: []FieldInput{
			{Name: "title", Type: TypeText, Required: true, Indexed: true},
			{Name: "views", Type: TypeBigInt, Default: json.RawMessage(`"0"`)},
		},
	}, administratorID)
	ctx := context.Background()
	for _, title := range []string{"first", "second"} {
		if _, err := pool.Exec(ctx, `insert into app.posts (id, created_at, updated_at, title, views)
			values (gen_random_uuid(), statement_timestamp(), statement_timestamp(), $1, 3)`, title); err != nil {
			t.Fatalf("seed record: %v", err)
		}
	}

	input := UpdateCollectionInput{
		Name: "posts",
		Fields: []FieldInput{
			{Name: "title", Type: TypeText, Required: true, Indexed: true},
			{Name: "views", Type: TypeNumeric},
			{Name: "extra", Type: TypeInteger, Default: json.RawMessage(`"1"`)},
		},
	}
	plan, err := service.PlanUpdate(ctx, input)
	if err != nil {
		t.Fatalf("plan update: %v", err)
	}
	updated, err := service.ApplyUpdate(ctx, input, plan.Checksum, plan.BaseVersion, administratorID)
	if err != nil {
		t.Fatalf("apply update: %v", err)
	}
	if updated.ID != created.ID || updated.CreatedAt.IsZero() {
		t.Fatalf("updated collection identity = %+v, want preserved ID and created_at", updated)
	}

	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if catalog.Version != 2 {
		t.Fatalf("catalog version = %d, want 2", catalog.Version)
	}
	persisted := catalog.Collections["posts"]
	if len(persisted.Fields) != 3 || persisted.Fields[1].Type != TypeNumeric || persisted.Fields[2].Name != "extra" {
		t.Fatalf("persisted fields = %+v", persisted.Fields)
	}
	if got := string(persisted.Fields[2].Default); got != `"1"` {
		t.Fatalf("persisted extra default = %s, want %s", got, `"1"`)
	}

	var dataColumnType, extraDefault string
	var extraValue int64
	if err := pool.QueryRow(ctx, `select data_type, coalesce(column_default, '') from information_schema.columns
		where table_schema = 'app' and table_name = 'posts' and column_name = 'views'`).Scan(&dataColumnType, &extraDefault); err != nil {
		t.Fatalf("read views column: %v", err)
	}
	if dataColumnType != "numeric" {
		t.Fatalf("views column type = %s, want numeric", dataColumnType)
	}
	if err := pool.QueryRow(ctx, `select min(extra) from app.posts`).Scan(&extraValue); err != nil {
		t.Fatalf("read extra column: %v", err)
	}
	if extraValue != 1 {
		t.Fatalf("existing rows did not receive the new default: %d", extraValue)
	}
	_ = extraDefault

	var migrations int64
	var operation string
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.schema_migrations`).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select operation from pgbase_meta.schema_migrations where version = 2`).Scan(&operation); err != nil {
		t.Fatal(err)
	}
	var indexes int64
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.collection_indexes`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if migrations != 2 || operation != updateCollectionOperation || indexes != 1 {
		t.Fatalf("migrations=%d operation=%q indexes=%d, want 2/%q/1", migrations, operation, indexes, updateCollectionOperation)
	}
	assertSchemaCounts(t, pool, schemaCounts{version: 2, collections: 1, fields: 3, migrations: 2})
}

func TestApplyUpdateRejectsUnknownCollectionStaleVersionAndChecksum(t *testing.T) {
	_, service, administratorID := newSchemaServiceTest(t, Planner{})
	applyFreshPlan(t, service, CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}, administratorID)
	ctx := context.Background()
	input := UpdateCollectionInput{Name: "ghosts", Fields: nil}
	if _, err := service.PlanUpdate(ctx, input); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("plan unknown collection error = %v, want ErrCollectionNotFound", err)
	}
	if _, err := service.ApplyUpdate(ctx, input, "", 1, administratorID); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("apply unknown collection error = %v, want ErrCollectionNotFound", err)
	}

	valid := UpdateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText, Indexed: true}}}
	plan, err := service.PlanUpdate(ctx, valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyUpdate(ctx, valid, plan.Checksum, plan.BaseVersion+5, administratorID); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale base version error = %v, want ErrConflict", err)
	}
	if _, err := service.ApplyUpdate(ctx, valid, "wrong-checksum", plan.BaseVersion, administratorID); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong checksum error = %v, want ErrConflict", err)
	}
}

func TestApplyUpdateRollsBackDDLMetadataAuditAndVersionOnStatementFailure(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	applyFreshPlan(t, service, CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}, administratorID)
	broken := Plan{
		Operation:   updateCollectionOperation,
		BaseVersion: 1,
		Checksum:    "invalid-update-checksum",
		Definition:  PlannedCollection{Name: "posts", TableName: "posts"},
		Statements: []string{
			`ALTER TABLE "app"."posts" DROP COLUMN "title"`,
			`ALTER TABLE "app"."posts" WILL_NOT_PARSE`,
		},
	}
	service.planner = fixedPlanBuilder{plan: broken}

	_, err := service.ApplyUpdate(context.Background(), UpdateCollectionInput{Name: "posts"}, broken.Checksum, 1, administratorID)
	if err == nil {
		t.Fatal("invalid DDL update succeeded")
	}
	assertSchemaCounts(t, pool, schemaCounts{version: 1, collections: 1, fields: 1, migrations: 1})
	var columns int64
	if err := pool.QueryRow(context.Background(), `select count(*) from information_schema.columns
		where table_schema = 'app' and table_name = 'posts' and column_name = 'title'`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 1 {
		t.Fatalf("rolled-back column drop persisted: %d", columns)
	}
}

func TestApplyUpdateSerializesUnderAdvisoryLock(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	applyFreshPlan(t, service, CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}, administratorID)
	input := UpdateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText, Indexed: true}}}
	plan, err := service.PlanUpdate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	lockingConnection, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lockingConnection.Release()
	lockingTx, err := lockingConnection.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_ = lockingTx.Rollback(context.Background())
		}
	}()
	if _, err := lockingTx.Exec(context.Background(), "select pg_advisory_xact_lock($1)", schemaAdvisoryLockKey); err != nil {
		t.Fatal(err)
	}

	applied := make(chan error, 1)
	go func() {
		_, applyErr := service.ApplyUpdate(context.Background(), input, plan.Checksum, plan.BaseVersion, administratorID)
		applied <- applyErr
	}()
	waitForAdvisoryLockWaiter(t, pool)
	if err := lockingTx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	locked = false
	if err := <-applied; err != nil {
		t.Fatalf("apply update after lock release: %v", err)
	}
	assertSchemaCounts(t, pool, schemaCounts{version: 2, collections: 1, fields: 1, migrations: 2})
}

func TestApplyDeleteRemovesCatalogTableAndAudit(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	applyFreshPlan(t, service, CreateCollectionInput{
		Name:   "posts",
		Fields: []FieldInput{{Name: "title", Type: TypeText, Indexed: true}},
	}, administratorID)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "pages", Fields: []FieldInput{{Name: "body", Type: TypeText}}}, administratorID)
	ctx := context.Background()

	plan, err := service.PlanDelete(ctx, "posts")
	if err != nil {
		t.Fatalf("plan delete: %v", err)
	}
	if err := service.ApplyDelete(ctx, "posts", plan.Checksum, plan.BaseVersion, administratorID); err != nil {
		t.Fatalf("apply delete: %v", err)
	}

	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if catalog.Version != 3 {
		t.Fatalf("catalog version = %d, want 3", catalog.Version)
	}
	if _, exists := catalog.Collections["posts"]; exists {
		t.Fatal("deleted collection remains in catalog")
	}
	var physicalTable string
	if err := pool.QueryRow(ctx, `select coalesce(to_regclass('app.posts')::text, '')`).Scan(&physicalTable); err != nil {
		t.Fatal(err)
	}
	if physicalTable != "" {
		t.Fatalf("physical table survived delete: %q", physicalTable)
	}
	var operation string
	if err := pool.QueryRow(ctx, `select operation from pgbase_meta.schema_migrations where version = 3`).Scan(&operation); err != nil {
		t.Fatal(err)
	}
	if operation != deleteCollectionOperation {
		t.Fatalf("audit operation = %q, want %q", operation, deleteCollectionOperation)
	}
	assertSchemaCounts(t, pool, schemaCounts{version: 3, collections: 1, fields: 1, migrations: 3})

	if err := service.ApplyDelete(ctx, "posts", plan.Checksum, 3, administratorID); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("second delete error = %v, want ErrCollectionNotFound", err)
	}
	if _, err := service.PlanDelete(ctx, "ghosts"); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("plan unknown delete error = %v, want ErrCollectionNotFound", err)
	}
}

func TestApplyDeleteRejectsStaleVersionAndChecksum(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	applyFreshPlan(t, service, CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}, administratorID)
	ctx := context.Background()
	plan, err := service.PlanDelete(ctx, "posts")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyDelete(ctx, "posts", plan.Checksum, plan.BaseVersion+1, administratorID); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale base version error = %v, want ErrConflict", err)
	}
	if err := service.ApplyDelete(ctx, "posts", "wrong-checksum", plan.BaseVersion, administratorID); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong checksum error = %v, want ErrConflict", err)
	}
	assertSchemaCounts(t, pool, schemaCounts{version: 1, collections: 1, fields: 1, migrations: 1})
}

func TestApplyUpdateReportsDataConstraintInsteadOfInternalError(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	applyFreshPlan(t, service, CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "note", Type: TypeText}}}, administratorID)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `insert into app.posts (id, created_at, updated_at, note)
		values (gen_random_uuid(), statement_timestamp(), statement_timestamp(), null)`); err != nil {
		t.Fatalf("seed null row: %v", err)
	}

	input := UpdateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "note", Type: TypeText, Required: true}}}
	plan, err := service.PlanUpdate(ctx, input)
	if err != nil {
		t.Fatalf("plan update: %v", err)
	}
	_, err = service.ApplyUpdate(ctx, input, plan.Checksum, plan.BaseVersion, administratorID)
	if !errors.Is(err, ErrDataConstraint) {
		t.Fatalf("apply error = %v, want ErrDataConstraint", err)
	}
	assertSchemaCounts(t, pool, schemaCounts{version: 1, collections: 1, fields: 1, migrations: 1})
	var nullable string
	if err := pool.QueryRow(ctx, `select is_nullable from information_schema.columns
		where table_schema = 'app' and table_name = 'posts' and column_name = 'note'`).Scan(&nullable); err != nil {
		t.Fatal(err)
	}
	if nullable != "YES" {
		t.Fatalf("column nullability after rollback = %q, want YES", nullable)
	}
}

type barrierPlanBuilder struct {
	delegate PlanBuilder
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (b *barrierPlanBuilder) PlanCreate(version int64, input CreateCollectionInput) (Plan, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return b.delegate.PlanCreate(version, input)
}

func (b *barrierPlanBuilder) PlanUpdate(version int64, current Collection, input UpdateCollectionInput) (Plan, error) {
	return b.delegate.PlanUpdate(version, current, input)
}

func (b *barrierPlanBuilder) PlanDelete(version int64, current Collection) (Plan, error) {
	return b.delegate.PlanDelete(version, current)
}

type fixedPlanBuilder struct{ plan Plan }

func (f fixedPlanBuilder) PlanCreate(int64, CreateCollectionInput) (Plan, error) { return f.plan, nil }

func (f fixedPlanBuilder) PlanUpdate(int64, Collection, UpdateCollectionInput) (Plan, error) {
	return f.plan, nil
}

func (f fixedPlanBuilder) PlanDelete(int64, Collection) (Plan, error) { return f.plan, nil }

type schemaCounts struct {
	version     int64
	collections int64
	fields      int64
	migrations  int64
}

func newSchemaServiceTest(t *testing.T, planner PlanBuilder) (*database.Pool, *Service, uuid.UUID) {
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
	return pool, NewService(pool, &Cache{}, planner), administratorID
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
