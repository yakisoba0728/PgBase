//go:build integration

package schema

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestApplyIntentUsesOneKernelForCreateUpdateAndDelete(t *testing.T) {
	_, service, administratorID := newSchemaServiceTest(t)
	ctx := context.Background()
	create := NewCreateChange(CreateCollectionInput{Name: "kernel_posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}})
	createPlan, err := service.Preview(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Apply(ctx, LegacyApply(create, createPlan, administratorID))
	if err != nil {
		t.Fatal(err)
	}
	if created.Collection.ID == uuid.Nil || created.Deleted {
		t.Fatalf("create result = %#v", created)
	}

	update := NewUpdateChange(UpdateCollectionInput{Name: "kernel_posts", Fields: []FieldInput{{Name: "title", Type: TypeText, Indexed: true}}})
	updatePlan, err := service.Preview(ctx, update)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.Apply(ctx, LegacyApply(update, updatePlan, administratorID))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Collection.ID != created.Collection.ID || updated.Deleted {
		t.Fatalf("update result = %#v, created = %#v", updated, created)
	}

	deleteChange := NewDeleteChange("kernel_posts")
	deletePlan, err := service.Preview(ctx, deleteChange)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := service.Apply(ctx, LegacyApply(deleteChange, deletePlan, administratorID))
	if err != nil {
		t.Fatal(err)
	}
	if !deleted.Deleted || deleted.Collection.ID != uuid.Nil {
		t.Fatalf("delete result = %#v", deleted)
	}
}

func TestPreviewAndApplyRejectCatalogLimitWithoutCommittingDDL(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	service.catalogWireLimit = 64
	ctx := context.Background()
	change := NewCreateChange(CreateCollectionInput{Name: "catalog_limit"})
	if _, err := service.Preview(ctx, change); !errors.Is(err, ErrCatalogLimitExceeded) {
		t.Fatalf("Preview() error = %v, want ErrCatalogLimitExceeded", err)
	}

	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := CompilePlan(catalog, change)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(ctx, LegacyApply(change, plan, administratorID)); !errors.Is(err, ErrCatalogLimitExceeded) {
		t.Fatalf("Apply() error = %v, want ErrCatalogLimitExceeded", err)
	}
	assertSchemaCounts(t, pool, schemaCounts{})
	var physicalTable string
	if err := pool.QueryRow(ctx, `select coalesce(to_regclass('app.catalog_limit')::text, '')`).Scan(&physicalTable); err != nil {
		t.Fatal(err)
	}
	if physicalTable != "" {
		t.Fatalf("catalog-limit rejection left physical table %q", physicalTable)
	}
}

func TestApplyOperationsRecordBoundedOutcomesAndDurations(t *testing.T) {
	pool, _, administratorID := newSchemaServiceTest(t)
	recorder := &schemaTelemetryRecorder{}
	service := NewService(pool, &Cache{}, recorder)
	ctx := context.Background()

	createInput := CreateCollectionInput{Name: "telemetry_records", Fields: []FieldInput{{Name: "title", Type: TypeText}}}
	createPlan, err := service.Preview(ctx, NewCreateChange(createInput))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyCreate(ctx, service, createInput, createPlan, administratorID); err != nil {
		t.Fatalf("applyCreate(): %v", err)
	}

	updateInput := UpdateCollectionInput{Name: createInput.Name, Fields: []FieldInput{{Name: "title", Type: TypeText, Indexed: true}}}
	updatePlan, err := service.Preview(ctx, NewUpdateChange(updateInput))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyUpdate(ctx, service, updateInput, updatePlan, administratorID); err != nil {
		t.Fatalf("applyUpdate(): %v", err)
	}

	deletePlan, err := service.Preview(ctx, NewDeleteChange(createInput.Name))
	if err != nil {
		t.Fatal(err)
	}
	if err := applyDelete(ctx, service, createInput.Name, deletePlan, administratorID); err != nil {
		t.Fatalf("applyDelete(): %v", err)
	}
	if err := applyDelete(ctx, service, createInput.Name, deletePlan, administratorID); !errors.Is(err, ErrConflict) {
		t.Fatalf("applyDelete(stale) error = %v, want conflict", err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	canceledPlan, err := service.Preview(ctx, NewCreateChange(CreateCollectionInput{Name: "canceled_apply"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyCreate(canceled, service, CreateCollectionInput{Name: "canceled_apply"}, canceledPlan, administratorID); !errors.Is(err, context.Canceled) {
		t.Fatalf("applyCreate(canceled) error = %v", err)
	}

	events := recorder.snapshot()
	want := []struct {
		operation observability.Operation
		outcome   observability.Outcome
	}{
		{observability.OperationSchemaCreate, observability.OutcomeSuccess},
		{observability.OperationSchemaUpdate, observability.OutcomeSuccess},
		{observability.OperationSchemaDelete, observability.OutcomeSuccess},
		{observability.OperationSchemaDelete, observability.OutcomeConflict},
		{observability.OperationSchemaCreate, observability.OutcomeCanceled},
	}
	if len(events) != len(want) {
		t.Fatalf("operation telemetry count = %d, want %d: %#v", len(events), len(want), events)
	}
	for index, expected := range want {
		event := events[index]
		if event.Kind() != observability.EventKindOperation || event.Component() != observability.ComponentSchema || event.Operation() != expected.operation || event.Outcome() != expected.outcome || event.Duration() < 0 {
			t.Fatalf("operation telemetry[%d] = %#v, want operation=%q outcome=%q", index, event, expected.operation, expected.outcome)
		}
	}
}

func TestRelationSchemaPlanAndApplyRecordBoundedRelationEvents(t *testing.T) {
	pool, _, administratorID := newSchemaServiceTest(t)
	recorder := &schemaTelemetryRecorder{}
	service := NewService(pool, &Cache{}, recorder)
	ctx := context.Background()

	targetInput := CreateCollectionInput{Name: "telemetry_targets", Fields: []FieldInput{{Name: "title", Type: TypeText}}}
	targetPlan, err := service.Preview(ctx, NewCreateChange(targetInput))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyCreate(ctx, service, targetInput, targetPlan, administratorID); err != nil {
		t.Fatalf("applyCreate(target): %v", err)
	}

	sourceInput := CreateCollectionInput{Name: "telemetry_sources", Fields: []FieldInput{{
		Name: "target", Type: TypeRelation, Relation: &RelationInput{TargetCollection: targetInput.Name},
	}}}
	sourcePlan, err := service.Preview(ctx, NewCreateChange(sourceInput))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyCreate(ctx, service, sourceInput, sourcePlan, administratorID); err != nil {
		t.Fatalf("applyCreate(source): %v", err)
	}

	deletePlan, err := service.Preview(ctx, NewDeleteChange(sourceInput.Name))
	if err != nil {
		t.Fatal(err)
	}
	if err := applyDelete(ctx, service, sourceInput.Name, deletePlan, administratorID); err != nil {
		t.Fatalf("applyDelete(source): %v", err)
	}

	var relationEvents []observability.Event
	for _, event := range recorder.snapshot() {
		if event.Component() == observability.ComponentRelation {
			relationEvents = append(relationEvents, event)
		}
	}
	if len(relationEvents) != 4 {
		t.Fatalf("relation schema telemetry = %#v, want plan/apply create and plan/apply delete events", relationEvents)
	}
	for index, event := range relationEvents {
		if event.Operation() != observability.OperationRelationSchema || event.Outcome() != observability.OutcomeSuccess || event.Duration() < 0 {
			t.Fatalf("relation schema telemetry[%d] = %#v", index, event)
		}
	}
}

func TestRelationSchemaPlanRecordsValidationAndSkipsScalarPlans(t *testing.T) {
	pool, _, _ := newSchemaServiceTest(t)
	recorder := &schemaTelemetryRecorder{}
	service := NewService(pool, &Cache{}, recorder)
	ctx := context.Background()

	if _, err := service.Preview(ctx, NewCreateChange(CreateCollectionInput{
		Name:   "telemetry_scalar_plan",
		Fields: []FieldInput{{Name: "title", Type: TypeText}},
	})); err != nil {
		t.Fatalf("Preview(create scalar): %v", err)
	}
	if _, err := service.Preview(ctx, NewCreateChange(CreateCollectionInput{
		Name: "telemetry_relation_plan",
		Fields: []FieldInput{{
			Name: "parent", Type: TypeRelation,
			Relation: &RelationInput{TargetCollection: "telemetry_relation_plan"},
		}},
	})); err != nil {
		t.Fatalf("Preview(create relation): %v", err)
	}
	if _, err := service.Preview(ctx, NewCreateChange(CreateCollectionInput{
		Name: "telemetry_invalid_relation_plan",
		Fields: []FieldInput{{
			Name: "parent", Type: TypeRelation,
			Relation: &RelationInput{TargetCollection: "missing_telemetry_target"},
		}},
	})); err == nil {
		t.Fatal("Preview(create invalid relation) succeeded")
	}

	events := recorder.snapshot()
	if len(events) != 2 {
		t.Fatalf("relation plan telemetry = %#v, want success and validation events only", events)
	}
	want := []observability.Outcome{observability.OutcomeSuccess, observability.OutcomeValidation}
	for index, event := range events {
		if event.Component() != observability.ComponentRelation ||
			event.Operation() != observability.OperationRelationSchema ||
			event.Outcome() != want[index] || event.Duration() < 0 {
			t.Fatalf("relation plan telemetry[%d] = %#v, want outcome=%q", index, event, want[index])
		}
	}
}

func TestApplyCreatePersistsCatalogDDLAndAuditAtomically(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	input := CreateCollectionInput{
		Name: "posts",
		Fields: []FieldInput{
			{Name: "title", Type: TypeText, Required: true, Indexed: true},
			{Name: "views", Type: TypeBigInt, Default: json.RawMessage(`"0"`)},
		},
	}

	plan, err := service.Preview(context.Background(), NewCreateChange(input))
	if err != nil {
		t.Fatalf("plan create: %v", err)
	}
	collection, err := applyCreate(context.Background(), service, input, plan, administratorID)
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
	pool, service, administratorID := newSchemaServiceTest(t)
	input := CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}
	applyFreshPlan(t, service, input, administratorID)

	second, err := service.Preview(context.Background(), NewCreateChange(input))
	if err != nil {
		t.Fatal(err)
	}
	_, err = applyCreate(context.Background(), service, input, second, administratorID)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate apply error = %v, want ErrConflict", err)
	}
	assertSchemaCounts(t, pool, schemaCounts{version: 1, collections: 1, fields: 1, migrations: 1})
}

func TestApplyCreateRejectsStaleBaseVersionAndWrongChecksum(t *testing.T) {
	t.Run("explicit base version mismatch", func(t *testing.T) {
		pool, service, administratorID := newSchemaServiceTest(t)
		input := CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}
		plan, err := service.Preview(context.Background(), NewCreateChange(input))
		if err != nil {
			t.Fatal(err)
		}
		_, err = applyCreate(context.Background(), service, input, Plan{Checksum: plan.Checksum, BaseVersion: plan.BaseVersion + 1}, administratorID)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("base-version mismatch error = %v, want ErrConflict", err)
		}
		assertSchemaCounts(t, pool, schemaCounts{})
	})

	t.Run("stale base version", func(t *testing.T) {
		pool, service, administratorID := newSchemaServiceTest(t)
		staleInput := CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}
		stalePlan, err := service.Preview(context.Background(), NewCreateChange(staleInput))
		if err != nil {
			t.Fatal(err)
		}
		applyFreshPlan(t, service, CreateCollectionInput{Name: "pages"}, administratorID)

		_, err = applyCreate(context.Background(), service, staleInput, stalePlan, administratorID)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("stale apply error = %v, want ErrConflict", err)
		}
		assertSchemaCounts(t, pool, schemaCounts{version: 1, collections: 1, fields: 0, migrations: 1})
	})

	t.Run("wrong checksum", func(t *testing.T) {
		pool, service, administratorID := newSchemaServiceTest(t)
		_, err := applyCreate(context.Background(), service, CreateCollectionInput{Name: "posts"}, Plan{Checksum: "wrong-checksum", BaseVersion: 0}, administratorID)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("wrong checksum error = %v, want ErrConflict", err)
		}
		assertSchemaCounts(t, pool, schemaCounts{})
	})
}

func TestApplyCreateRollsBackDDLMetadataAuditAndVersionOnStatementFailure(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	input := CreateCollectionInput{Name: "broken"}
	plan, err := service.Preview(context.Background(), NewCreateChange(input))
	if err != nil {
		t.Fatal(err)
	}
	service.beforeDDL = func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, plan.Statements[0]); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `CREATE TABLE "app"."will_not_parse" (`)
		return err
	}
	_, err = applyCreate(context.Background(), service, input, plan, administratorID)
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

func TestApplyCreateCompletesDDLBeyondThirtySecondsAndCanBeRechecked(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	administratorID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000011")
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.admins (id, email, password_hash, active, created_at, updated_at)
		values ($1, 'slow-schema@example.com', 'test', true, current_timestamp, current_timestamp)`, administratorID); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, &Cache{}, nil)
	input := CreateCollectionInput{Name: "slow_table"}
	plan, err := service.Preview(ctx, NewCreateChange(input))
	if err != nil {
		t.Fatal(err)
	}
	service.beforeDDL = func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `select pg_sleep(30.1)`)
		return err
	}
	started := time.Now()
	if _, err := applyCreate(ctx, service, input, plan, administratorID); err != nil {
		t.Fatalf("applyCreate() applied the request statement timeout to DDL: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 30*time.Second {
		t.Fatalf("slow DDL completed in %s, want a characterization longer than 30s", elapsed)
	}

	// Re-read through a fresh cache as a client must after a long request whose
	// response may have raced an outer HTTP write deadline.
	recheck := NewService(pool, &Cache{}, nil)
	catalog, err := recheck.Catalog(ctx)
	if err != nil {
		t.Fatalf("recheck catalog after slow DDL: %v", err)
	}
	if _, ok := catalog.Collections["slow_table"]; !ok {
		t.Fatalf("rechecked catalog omitted committed slow_table: %#v", catalog.Collections)
	}
	var physicalTable string
	if err := pool.QueryRow(ctx, `select coalesce(to_regclass('app.slow_table')::text, '')`).Scan(&physicalTable); err != nil {
		t.Fatalf("recheck physical table after slow DDL: %v", err)
	}
	if physicalTable != "app.slow_table" {
		t.Fatalf("rechecked physical table = %q, want app.slow_table", physicalTable)
	}
}

func TestApplyCreateSerializesConcurrentUseOfOnePlan(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	inputs := []CreateCollectionInput{
		{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}},
		{Name: "pages", Fields: []FieldInput{{Name: "body", Type: TypeText}}},
	}
	plans := make([]Plan, len(inputs))
	for index, input := range inputs {
		plan, err := service.Preview(context.Background(), NewCreateChange(input))
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
			_, applyErr := applyCreate(context.Background(), service, input, plan, administratorID)
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
	pool, service, administratorID := newSchemaServiceTest(t)
	input := CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}
	plan, err := service.Preview(context.Background(), NewCreateChange(input))
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
		_, applyErr := applyCreate(context.Background(), service, input, plan, administratorID)
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

func TestEnsureCurrentRejectsSnapshotThatPredatesACommittedSchemaChange(t *testing.T) {
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
	firstService := NewService(firstPool, firstCache, nil)
	secondService := NewService(secondPool, secondCache, nil)
	snapshotPinned := make(chan struct{})
	newerPublished := make(chan struct{})
	type ensureResult struct{ err error }
	result := make(chan ensureResult, 1)
	go func() {
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
			_, err := firstService.EnsureCurrent(ctx, tx)
			return err
		})
		result <- ensureResult{err: err}
	}()
	<-snapshotPinned

	applyFreshPlan(t, secondService, CreateCollectionInput{Name: "posts"}, administratorID)
	if err := publishCatalog(firstCache, secondCache.Load()); err != nil {
		t.Fatalf("publish newer catalog snapshot: %v", err)
	}
	close(newerPublished)

	ensured := <-result
	var postgresError *pgconn.PgError
	if !errors.As(ensured.err, &postgresError) || postgresError.Code != "40001" {
		t.Fatalf("EnsureCurrent error = %v, want serialization failure 40001", ensured.err)
	}
	if current := firstCache.Load(); current.Version != 1 {
		t.Fatalf("opportunistic publication regressed cache to version %d, want 1", current.Version)
	}
}

// TestCatalogRetriesSerializationFailureFromACommittedSchemaChange holds a
// schema writer inside its planner while it owns the exclusive schema_state
// row, so the catalog reader pins the shared side of the same row from a
// repeatable-read snapshot that predates the writer's commit. PostgreSQL
// aborts that wait with 40001; without Catalog's retry loop the reader would
// surface the serialization failure instead of reading the committed catalog.
func TestCatalogRetriesSerializationFailureFromACommittedSchemaChange(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	ctx := context.Background()
	input := CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}
	plan, err := service.Preview(ctx, NewCreateChange(input))
	if err != nil {
		t.Fatalf("plan create: %v", err)
	}

	recorder := &schemaTelemetryRecorder{}
	service.recorder = recorder
	plannerEntered := make(chan struct{})
	plannerRelease := make(chan struct{})
	var plannerEnteredOnce sync.Once
	service.beforeDDL = func(context.Context, pgx.Tx) error {
		plannerEnteredOnce.Do(func() { close(plannerEntered) })
		<-plannerRelease
		return nil
	}
	var releaseWriter sync.Once
	release := func() { releaseWriter.Do(func() { close(plannerRelease) }) }
	defer release()

	applyResult := make(chan error, 1)
	go func() {
		_, applyErr := applyCreate(ctx, service, input, plan, administratorID)
		applyResult <- applyErr
	}()
	<-plannerEntered

	type catalogResult struct {
		catalog Catalog
		err     error
	}
	catalogRead := make(chan catalogResult, 1)
	go func() {
		catalog, err := service.Catalog(ctx)
		catalogRead <- catalogResult{catalog: catalog, err: err}
	}()
	waitForPinnedSchemaStateReader(t, pool)
	release()

	if err := <-applyResult; err != nil {
		t.Fatalf("applyCreate while a catalog reader waited: %v", err)
	}
	read := <-catalogRead
	if read.err != nil {
		t.Fatalf("Catalog() error = %v, want a retry against a fresh snapshot", read.err)
	}
	if read.catalog.Version != 1 {
		t.Fatalf("catalog version = %d, want the committed version 1", read.catalog.Version)
	}
	if _, ok := read.catalog.Collections["posts"]; !ok {
		t.Fatalf("catalog collections = %v, want the committed posts collection", read.catalog.Collections)
	}

	var retries []observability.Event
	for _, event := range recorder.snapshot() {
		if event.Kind() == observability.EventKindTransactionRetry {
			retries = append(retries, event)
		}
	}
	if len(retries) != 1 || retries[0].RetryCause() != observability.RetryCauseSerialization || retries[0].Exhausted() {
		t.Fatalf("retry telemetry = %#v, want one non-exhausted serialization event", retries)
	}
}

func TestPinnedRecordCatalogBlocksSchemaDDLUntilTheRecordTransactionEnds(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	ctx := context.Background()
	applyFreshPlan(t, service, CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}, administratorID)
	update := UpdateCollectionInput{Name: "posts", Fields: []FieldInput{
		{Name: "title", Type: TypeText},
		{Name: "summary", Type: TypeText},
	}}
	plan, err := service.Preview(ctx, NewUpdateChange(update))
	if err != nil {
		t.Fatal(err)
	}

	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	recordTx, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer recordTx.Rollback(ctx)
	if _, err := service.EnsureCurrent(ctx, recordTx); err != nil {
		t.Fatal(err)
	}

	applyResult := make(chan error, 1)
	go func() {
		_, err := applyUpdate(ctx, service, update, plan, administratorID)
		applyResult <- err
	}()
	waitForSchemaStateLockWaiter(t, pool)

	var summaryColumns int
	if err := pool.QueryRow(ctx, `select count(*) from information_schema.columns
		where table_schema = 'app' and table_name = 'posts' and column_name = 'summary'`).Scan(&summaryColumns); err != nil {
		t.Fatal(err)
	}
	if summaryColumns != 0 {
		t.Fatal("schema DDL ran while a record transaction held the catalog pin")
	}
	if err := recordTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-applyResult; err != nil {
		t.Fatalf("applyUpdate after record transaction ended: %v", err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from information_schema.columns
		where table_schema = 'app' and table_name = 'posts' and column_name = 'summary'`).Scan(&summaryColumns); err != nil {
		t.Fatal(err)
	}
	if summaryColumns != 1 {
		t.Fatalf("summary column count = %d after schema apply, want 1", summaryColumns)
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
	service := NewService(pool, cache, nil)
	input := CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}
	plan, err := service.Preview(ctx, NewCreateChange(input))
	if err != nil {
		t.Fatalf("plan create: %v", err)
	}
	plannerEntered := make(chan struct{})
	plannerRelease := make(chan struct{})
	var plannerEnteredOnce sync.Once
	service.beforeDDL = func(context.Context, pgx.Tx) error {
		plannerEnteredOnce.Do(func() { close(plannerEntered) })
		<-plannerRelease
		return nil
	}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	applyResult := make(chan error, 1)
	go func() {
		_, applyErr := applyCreate(requestCtx, service, input, plan, administratorID)
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
		t.Fatalf("applyCreate reported failure after committing schema state: %v", err)
	}

	assertSchemaCounts(t, pool, schemaCounts{version: 1, collections: 1, fields: 1, migrations: 1})
	published := cache.Load()
	if published.Version != 1 || published.Collections["posts"].Name != "posts" {
		t.Fatalf("published catalog after commit = %+v", published)
	}
}

func TestApplyUpdatePersistsCatalogDDLAndAuditAtomically(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
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
	plan, err := service.Preview(ctx, NewUpdateChange(input))
	if err != nil {
		t.Fatalf("plan update: %v", err)
	}
	updated, err := applyUpdate(ctx, service, input, plan, administratorID)
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

	var dataColumnType string
	var extraValue int64
	if err := pool.QueryRow(ctx, `select data_type from information_schema.columns
		where table_schema = 'app' and table_name = 'posts' and column_name = 'views'`).Scan(&dataColumnType); err != nil {
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

func TestApplyUpdatePreservesRetainedFieldIDsAndCreatesIDsOnlyForNewFields(t *testing.T) {
	_, service, administratorID := newSchemaServiceTest(t)
	created := applyFreshPlan(t, service, CreateCollectionInput{Name: "posts", Fields: []FieldInput{
		{Name: "title", Type: TypeText},
		{Name: "views", Type: TypeBigInt},
	}}, administratorID)

	input := UpdateCollectionInput{Name: "posts", Fields: []FieldInput{
		{Name: "title", Type: TypeText, Indexed: true},
		{Name: "views", Type: TypeNumeric},
		{Name: "summary", Type: TypeText},
	}}
	plan, err := service.Preview(context.Background(), NewUpdateChange(input))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := applyUpdate(context.Background(), service, input, plan, administratorID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Fields[0].ID != created.Fields[0].ID || updated.Fields[1].ID != created.Fields[1].ID {
		t.Fatalf("retained field IDs changed: before=%v after=%v", []uuid.UUID{created.Fields[0].ID, created.Fields[1].ID}, []uuid.UUID{updated.Fields[0].ID, updated.Fields[1].ID})
	}
	if updated.Fields[2].ID.Version() != 7 || updated.Fields[2].ID == created.Fields[0].ID || updated.Fields[2].ID == created.Fields[1].ID {
		t.Fatalf("new field ID = %s, want a fresh UUIDv7", updated.Fields[2].ID)
	}
}

func TestApplyUpdateRejectsUnknownCollectionStaleVersionAndChecksum(t *testing.T) {
	_, service, administratorID := newSchemaServiceTest(t)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}, administratorID)
	ctx := context.Background()
	input := UpdateCollectionInput{Name: "ghosts", Fields: nil}
	if _, err := service.Preview(ctx, NewUpdateChange(input)); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("plan unknown collection error = %v, want ErrCollectionNotFound", err)
	}
	if _, err := applyUpdate(ctx, service, input, Plan{Checksum: "", BaseVersion: 1}, administratorID); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("apply unknown collection error = %v, want ErrCollectionNotFound", err)
	}

	valid := UpdateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText, Indexed: true}}}
	plan, err := service.Preview(ctx, NewUpdateChange(valid))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyUpdate(ctx, service, valid, Plan{Checksum: plan.Checksum, BaseVersion: plan.BaseVersion + 5}, administratorID); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale base version error = %v, want ErrConflict", err)
	}
	if _, err := applyUpdate(ctx, service, valid, Plan{Checksum: "wrong-checksum", BaseVersion: plan.BaseVersion}, administratorID); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong checksum error = %v, want ErrConflict", err)
	}
}

func TestApplyUpdateRollsBackDDLMetadataAuditAndVersionOnStatementFailure(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}, administratorID)
	input := UpdateCollectionInput{Name: "posts"}
	plan, err := service.Preview(context.Background(), NewUpdateChange(input))
	if err != nil {
		t.Fatal(err)
	}
	service.beforeDDL = func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, plan.Statements[0]); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `ALTER TABLE "app"."posts" WILL_NOT_PARSE`)
		return err
	}

	_, err = applyUpdate(context.Background(), service, input, plan, administratorID)
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
	pool, service, administratorID := newSchemaServiceTest(t)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}, administratorID)
	input := UpdateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText, Indexed: true}}}
	plan, err := service.Preview(context.Background(), NewUpdateChange(input))
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
		_, applyErr := applyUpdate(context.Background(), service, input, plan, administratorID)
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
	pool, service, administratorID := newSchemaServiceTest(t)
	applyFreshPlan(t, service, CreateCollectionInput{
		Name:   "posts",
		Fields: []FieldInput{{Name: "title", Type: TypeText, Indexed: true}},
	}, administratorID)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "pages", Fields: []FieldInput{{Name: "body", Type: TypeText}}}, administratorID)
	ctx := context.Background()

	plan, err := service.Preview(ctx, NewDeleteChange("posts"))
	if err != nil {
		t.Fatalf("plan delete: %v", err)
	}
	if err := applyDelete(ctx, service, "posts", plan, administratorID); err != nil {
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

	if err := applyDelete(ctx, service, "posts", Plan{Checksum: plan.Checksum, BaseVersion: 3}, administratorID); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("second delete error = %v, want ErrCollectionNotFound", err)
	}
	if _, err := service.Preview(ctx, NewDeleteChange("ghosts")); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("plan unknown delete error = %v, want ErrCollectionNotFound", err)
	}
}

func TestApplyDeleteRejectsStaleVersionAndChecksum(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}, administratorID)
	ctx := context.Background()
	plan, err := service.Preview(ctx, NewDeleteChange("posts"))
	if err != nil {
		t.Fatal(err)
	}
	if err := applyDelete(ctx, service, "posts", Plan{Checksum: plan.Checksum, BaseVersion: plan.BaseVersion + 1}, administratorID); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale base version error = %v, want ErrConflict", err)
	}
	if err := applyDelete(ctx, service, "posts", Plan{Checksum: "wrong-checksum", BaseVersion: plan.BaseVersion}, administratorID); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong checksum error = %v, want ErrConflict", err)
	}
	assertSchemaCounts(t, pool, schemaCounts{version: 1, collections: 1, fields: 1, migrations: 1})
}

func TestPlanUpdateOmitsSetDefaultForUnchangedObjectDefaults(t *testing.T) {
	_, service, administratorID := newSchemaServiceTest(t)
	created := applyFreshPlan(t, service, CreateCollectionInput{
		Name: "events",
		Fields: []FieldInput{
			{Name: "starts_at", Type: TypeTimestampTZ, Default: json.RawMessage(`{"kind":"now"}`)},
			{Name: "payload", Type: TypeJSONB, Default: json.RawMessage(`{"z":1,"a":[1,2,3]}`)},
		},
	}, administratorID)
	ctx := context.Background()

	input := UpdateCollectionInput{Name: "events", Fields: []FieldInput{
		{Name: "starts_at", Type: TypeTimestampTZ, Default: json.RawMessage(`{"kind":"now"}`)},
		{Name: "payload", Type: TypeJSONB, Default: json.RawMessage(`{"a":[1,2,3],"z":1}`)},
	}}
	plan, err := service.Preview(ctx, NewUpdateChange(input))
	if err != nil {
		t.Fatalf("plan update: %v", err)
	}
	if len(plan.Statements) != 0 {
		t.Fatalf("unchanged object defaults emitted DDL: %#v", plan.Statements)
	}
	if len(created.Fields) != 2 {
		t.Fatalf("fixture fields = %d", len(created.Fields))
	}
}

func TestApplyUpdateCreatesEnforcesAndDropsIndexesForFieldFlags(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}}}, administratorID)
	ctx := context.Background()

	input := UpdateCollectionInput{Name: "posts", Fields: []FieldInput{
		{Name: "title", Type: TypeText},
		{Name: "slug", Type: TypeText, Unique: true, Indexed: true},
	}}
	plan, err := service.Preview(ctx, NewUpdateChange(input))
	if err != nil {
		t.Fatalf("plan update: %v", err)
	}
	if _, err := applyUpdate(ctx, service, input, plan, administratorID); err != nil {
		t.Fatalf("apply update: %v", err)
	}

	var indexes int
	if err := pool.QueryRow(ctx, `select count(*) from pg_indexes
		where schemaname = 'app' and tablename = 'posts' and indexname = $1`,
		indexName("posts", "slug")).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if indexes != 1 {
		t.Fatalf("added unique field produced %d indexes, want 1", indexes)
	}
	if _, err := pool.Exec(ctx, `insert into app.posts (id, created_at, updated_at, title, slug)
		values (gen_random_uuid(), statement_timestamp(), statement_timestamp(), 't', 'same')`); err != nil {
		t.Fatalf("first slug insert: %v", err)
	}
	if _, err := pool.Exec(ctx, `insert into app.posts (id, created_at, updated_at, title, slug)
		values (gen_random_uuid(), statement_timestamp(), statement_timestamp(), 't', 'same')`); err == nil {
		t.Fatal("duplicate slug insert succeeded without a unique index")
	}

	demoted := UpdateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText}, {Name: "slug", Type: TypeText}}}
	plan, err = service.Preview(ctx, NewUpdateChange(demoted))
	if err != nil {
		t.Fatalf("plan demote: %v", err)
	}
	if _, err := applyUpdate(ctx, service, demoted, plan, administratorID); err != nil {
		t.Fatalf("apply demote: %v", err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from pg_indexes
		where schemaname = 'app' and tablename = 'posts' and indexname = $1`,
		indexName("posts", "slug")).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if indexes != 0 {
		t.Fatalf("demoted field kept %d indexes, want 0", indexes)
	}
}

func TestApplyUpdateReportsDataConstraintInsteadOfInternalError(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "note", Type: TypeText}}}, administratorID)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `insert into app.posts (id, created_at, updated_at, note)
		values (gen_random_uuid(), statement_timestamp(), statement_timestamp(), null)`); err != nil {
		t.Fatalf("seed null row: %v", err)
	}

	input := UpdateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "note", Type: TypeText, Required: true}}}
	plan, err := service.Preview(ctx, NewUpdateChange(input))
	if err != nil {
		t.Fatalf("plan update: %v", err)
	}
	_, err = applyUpdate(ctx, service, input, plan, administratorID)
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

func TestApplyUpdateReportsExistingDuplicateValuesAsDataConstraint(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "slug", Type: TypeText}}}, administratorID)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `insert into app.posts (id, created_at, updated_at, slug) values
		(gen_random_uuid(), statement_timestamp(), statement_timestamp(), 'duplicate'),
		(gen_random_uuid(), statement_timestamp(), statement_timestamp(), 'duplicate')`); err != nil {
		t.Fatal(err)
	}

	input := UpdateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "slug", Type: TypeText, Unique: true}}}
	plan, err := service.Preview(ctx, NewUpdateChange(input))
	if err != nil {
		t.Fatal(err)
	}
	_, err = applyUpdate(ctx, service, input, plan, administratorID)
	if !errors.Is(err, ErrDataConstraint) {
		t.Fatalf("applyUpdate error = %v, want ErrDataConstraint", err)
	}
	assertSchemaCounts(t, pool, schemaCounts{version: 1, collections: 1, fields: 1, migrations: 1})
}

func TestApplyUpdateReportsOversizedExistingBtreeValueAsDataConstraint(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "slug", Type: TypeText}}}, administratorID)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `insert into app.posts (id, created_at, updated_at, slug)
		select gen_random_uuid(), statement_timestamp(), statement_timestamp(), string_agg(md5(value::text), '')
		from generate_series(1, 200) as value`); err != nil {
		t.Fatal(err)
	}

	input := UpdateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "slug", Type: TypeText, Indexed: true}}}
	plan, err := service.Preview(ctx, NewUpdateChange(input))
	if err != nil {
		t.Fatal(err)
	}
	_, err = applyUpdate(ctx, service, input, plan, administratorID)
	if !errors.Is(err, ErrDataConstraint) {
		t.Fatalf("applyUpdate error = %v, want ErrDataConstraint", err)
	}
	assertSchemaCounts(t, pool, schemaCounts{version: 1, collections: 1, fields: 1, migrations: 1})
}

// TestCatalogReadsSurviveLegacyOversizedTextDefault pins the wiring half of the
// input/stored default split. validateCatalogIntegrity runs on every
// EnsureCurrent, so pointing it at the input validator would turn one legacy
// column into ErrConflict for every collection in the database, not just the
// one carrying the default. The unit test around normalizeStoredDefault cannot
// catch that: it passes whether or not the catalog path actually calls it.
func TestCatalogReadsSurviveLegacyOversizedTextDefault(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	ctx := context.Background()

	input := CreateCollectionInput{
		Name: "legacy_defaults",
		Fields: []FieldInput{
			{Name: "body", Type: TypeText, Default: json.RawMessage(`"short"`)},
		},
	}
	plan, err := service.Preview(ctx, NewCreateChange(input))
	if err != nil {
		t.Fatalf("Preview(create): %v", err)
	}
	if _, err := applyCreate(ctx, service, input, plan, administratorID); err != nil {
		t.Fatalf("applyCreate: %v", err)
	}
	bystander := CreateCollectionInput{Name: "bystander", Fields: []FieldInput{{Name: "title", Type: TypeText}}}
	bystanderPlan, err := service.Preview(ctx, NewCreateChange(bystander))
	if err != nil {
		t.Fatalf("Preview(create) bystander: %v", err)
	}
	if _, err := applyCreate(ctx, service, bystander, bystanderPlan, administratorID); err != nil {
		t.Fatalf("applyCreate bystander: %v", err)
	}

	// An older binary bounded record values but not schema defaults, so a
	// catalog it wrote can hold a text default this binary refuses on input.
	oversized := strings.Repeat("x", MaximumTextBytes+1)
	if _, err := pool.Exec(ctx, `update pgbase_meta.fields as fields
		set default_json = to_jsonb($1::text)
		from pgbase_meta.collections as collections
		where fields.collection_id = collections.id
			and collections.name = 'legacy_defaults' and fields.name = 'body'`, oversized); err != nil {
		t.Fatalf("write legacy default: %v", err)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.schema_state
		set version = version + 1, updated_at = current_timestamp where singleton`); err != nil {
		t.Fatalf("bump schema version: %v", err)
	}

	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatalf("Catalog() error = %v, want the legacy default to stay readable", err)
	}
	legacy, ok := catalog.Collections["legacy_defaults"]
	if !ok {
		t.Fatal("catalog lost the collection holding the legacy default")
	}
	if got := string(legacy.Fields[0].Default); got != strconv.Quote(oversized) {
		t.Fatalf("legacy default = %d bytes, want the stored value preserved", len(got))
	}
	carry := UpdateCollectionInput{
		Name: "legacy_defaults",
		Fields: []FieldInput{{
			Name: "body", Type: TypeText, Default: json.RawMessage(strconv.Quote(oversized)),
		}},
	}
	carryPlan, err := service.Preview(ctx, NewUpdateChange(carry))
	if err != nil {
		t.Fatalf("Preview(update) unchanged legacy default: %v", err)
	}
	if len(carryPlan.Statements) != 0 {
		t.Fatalf("unchanged legacy default emitted DDL: %#v", carryPlan.Statements)
	}
	updated, err := applyUpdate(ctx, service, carry, carryPlan, administratorID)
	if err != nil {
		t.Fatalf("applyUpdate unchanged legacy default: %v", err)
	}
	if got := string(updated.Fields[0].Default); got != strconv.Quote(oversized) {
		t.Fatalf("applied legacy default = %d bytes, want the stored value preserved", len(got))
	}
	// The whole catalog has to survive, not only the offending collection.
	if _, ok := catalog.Collections["bystander"]; !ok {
		t.Fatal("catalog lost an unrelated collection, so one legacy default locked the database")
	}
	// The bound still closes the input path.
	if _, err := service.Preview(ctx, NewCreateChange(CreateCollectionInput{
		Name:   "rejected_defaults",
		Fields: []FieldInput{{Name: "body", Type: TypeText, Default: json.RawMessage(strconv.Quote(oversized))}},
	})); err == nil {
		t.Fatal("Preview(create) accepted an oversized text default, want validation error")
	}
}
