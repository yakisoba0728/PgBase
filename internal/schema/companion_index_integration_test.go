//go:build integration

package schema

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
)

// nullableDescendingPageOrderBy is the ordering internal/query renders for
// "sort=-<field>" over a nullable column: the column keeps the public NULLS LAST
// contract and the appended id tiebreaker keeps PostgreSQL's DESC default. The
// string is duplicated rather than imported because internal/query depends on
// this package; the companion index is worthless if the two ever disagree, so
// the EXPLAIN assertion below is what keeps them honest.
const nullableDescendingPageOrderBy = `ORDER BY %s DESC NULLS LAST, "id" DESC NULLS FIRST`

func companionRolloutFixture(t *testing.T) (*database.Pool, *Service, uuid.UUID, Collection) {
	t.Helper()
	pool, service, administratorID := newSchemaServiceTest(t, Planner{})
	collection := applyFreshPlan(t, service, CreateCollectionInput{
		Name: "companion_posts",
		Fields: []FieldInput{
			{Name: "title", Type: TypeText, Required: true, Indexed: true},
			{Name: "nickname", Type: TypeText, Indexed: true},
		},
	}, administratorID)
	return pool, service, administratorID, collection
}

func companionSpec(t *testing.T, service *Service, collection Collection, field string) IndexSpec {
	t.Helper()
	catalog, err := service.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ExpectedPhysicalSchemaForCatalog(catalog)
	if err != nil {
		t.Fatal(err)
	}
	want := NullableDescCompanionIndexName(collection.Name, field)
	for _, companion := range manifest.CompanionIndexes() {
		if companion.Name == want {
			return companion
		}
	}
	t.Fatalf("catalog has no companion index for %s.%s", collection.Name, field)
	return IndexSpec{}
}

func activateCompanionRollout(t *testing.T, service *Service) CompanionIndexReceipt {
	t.Helper()
	instanceID, err := service.instance()
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := service.ActivateCompanionIndexes(context.Background(),
		SchemaWriterFleetProof{Instances: []uuid.UUID{instanceID}})
	if err != nil {
		t.Fatalf("activate companion index rollout: %v", err)
	}
	return receipt
}

func TestCompanionIndexRolloutActivatesBuildsAndPromotes(t *testing.T) {
	ctx := context.Background()
	pool, service, _, collection := companionRolloutFixture(t)

	// Migration 8 seeds pending, so nothing requires a companion until an
	// operator opens the rollout.
	status, err := service.CompanionIndexStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || status.State != CompanionIndexPending || status.Ready() {
		t.Fatalf("seeded capability = %#v", status)
	}

	opened := activateCompanionRollout(t, service)
	if opened.State != CompanionIndexBuilding || opened.Generation != 1 {
		t.Fatalf("activation receipt = %#v", opened)
	}

	ready, err := service.MaintainCompanionIndexes(ctx)
	if err != nil {
		t.Fatalf("maintain companion indexes: %v", err)
	}
	if ready.State != CompanionIndexReady || ready.IndexCount != 1 {
		t.Fatalf("promotion receipt = %#v", ready)
	}

	companion := companionSpec(t, service, collection, "nickname")
	assertCompanionIndexShape(t, pool, companion)

	// The manifest owns the object it just built, so a doctor or a rollback
	// binary reading the same catalog cannot classify it as an orphan.
	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ExpectedPhysicalSchemaForCatalog(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.Owns(PhysicalObjectRef{
		Kind: PhysicalObjectIndex, Namespace: companion.Namespace,
		Table: companion.Table, Name: companion.Name,
	}) {
		t.Fatalf("companion %s is not owned", companion.Name)
	}

	diagnostics := readDiagnostics(t, pool)
	if diagnostics.Expected != 1 || diagnostics.Valid != 1 ||
		diagnostics.Invalid != 0 || diagnostics.Missing != 0 || diagnostics.Mismatched != 0 {
		t.Fatalf("ready diagnostics = %#v", diagnostics)
	}
}

func assertCompanionIndexShape(t *testing.T, pool *database.Pool, companion IndexSpec) {
	t.Helper()
	var definition string
	if err := pool.QueryRow(context.Background(), `select indexdef from pg_indexes
		where schemaname = $1 and tablename = $2 and indexname = $3`,
		companion.Namespace, companion.Table, companion.Name).Scan(&definition); err != nil {
		t.Fatalf("read companion index definition: %v", err)
	}
	for _, fragment := range []string{"DESC NULLS LAST", "id DESC"} {
		if !strings.Contains(definition, fragment) {
			t.Fatalf("companion index definition %q is missing %q", definition, fragment)
		}
	}
}

func readDiagnostics(t *testing.T, pool *database.Pool) CompanionIndexDiagnostics {
	t.Helper()
	var diagnostics CompanionIndexDiagnostics
	if err := pool.InTx(context.Background(), pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	}, func(tx pgx.Tx) error {
		var err error
		diagnostics, err = ReadCompanionIndexDiagnostics(context.Background(), tx)
		return err
	}); err != nil {
		t.Fatalf("read companion index diagnostics: %v", err)
	}
	return diagnostics
}

// A concurrent build that dies leaves an invalid index behind. PostgreSQL's own
// catalog is the checkpoint, so a rerun must find that residue, drop it and
// finish, without any progress record of its own in pgbase_meta.
func TestCompanionIndexMaintenanceRecoversFromAKilledConcurrentBuild(t *testing.T) {
	ctx := context.Background()
	pool, service, _, collection := companionRolloutFixture(t)
	seedCompanionRows(t, pool, collection, 2000)
	activateCompanionRollout(t, service)
	companion := companionSpec(t, service, collection, "nickname")

	// CREATE INDEX CONCURRENTLY commits its invalid catalog entry and then waits
	// for lockers that conflict with ShareLock. An uncommitted write holds
	// RowExclusiveLock, which is exactly that wait, so the build is reliably
	// parked at the point an interrupted build would die.
	blocker, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Release()
	blocking, err := blocker.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blocking.Exec(ctx, "insert into "+
		pgx.Identifier{"app", collection.TableName}.Sanitize()+
		" (id, created_at, updated_at, title, nickname) values"+
		" ('0199c200-0000-7000-8000-000000000001', current_timestamp, current_timestamp,"+
		" 'blocking', 'blocking')"); err != nil {
		t.Fatal(err)
	}

	buildContext, killBuild := context.WithCancel(ctx)
	buildDone := make(chan error, 1)
	go func() {
		_, err := service.MaintainCompanionIndexes(buildContext)
		buildDone <- err
	}()
	waitForInvalidCompanionIndex(t, pool, companion)
	killBuild()
	if err := <-buildDone; err == nil {
		t.Fatal("killed companion index build reported success")
	}
	if err := blocking.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	// The residue is visible and classified as recoverable rather than as a
	// missing contract.
	diagnostics := readDiagnostics(t, pool)
	if diagnostics.Invalid != 1 || diagnostics.Valid != 0 || diagnostics.Missing != 0 {
		t.Fatalf("post-kill diagnostics = %#v", diagnostics)
	}
	if diagnostics.Status.State != CompanionIndexBuilding {
		t.Fatalf("post-kill capability state = %q, want building", diagnostics.Status.State)
	}

	ready, err := service.MaintainCompanionIndexes(ctx)
	if err != nil {
		t.Fatalf("restarted companion index maintenance: %v", err)
	}
	if ready.State != CompanionIndexReady || ready.IndexCount != 1 {
		t.Fatalf("restarted promotion receipt = %#v", ready)
	}
	if got := readDiagnostics(t, pool); got.Valid != 1 || got.Invalid != 0 {
		t.Fatalf("recovered diagnostics = %#v", got)
	}
	assertCompanionIndexShape(t, pool, companion)
}

func waitForInvalidCompanionIndex(t *testing.T, pool *database.Pool, companion IndexSpec) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var present bool
		if err := pool.QueryRow(context.Background(), `select exists(
			select 1 from pg_index as indexes
			join pg_class as index_tables on index_tables.oid = indexes.indexrelid
			join pg_namespace as schemas on schemas.oid = index_tables.relnamespace
			where schemas.nspname = $1 and index_tables.relname = $2
				and not indexes.indisvalid)`,
			companion.Namespace, companion.Name).Scan(&present); err != nil {
			t.Fatal(err)
		}
		if present {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("concurrent build never published an in-progress index to interrupt")
}

func seedCompanionRows(t *testing.T, pool *database.Pool, collection Collection, rows int) {
	t.Helper()
	// Roughly one row in eight is NULL so a descending page actually has to
	// place nulls, and the values repeat so ties exercise the id tiebreaker.
	if _, err := pool.Exec(context.Background(), fmt.Sprintf(`insert into %s
		(id, created_at, updated_at, title, nickname)
		select
			(('0199c000-0000-7000-8000-' || lpad(to_hex(series), 12, '0'))::uuid),
			current_timestamp, current_timestamp,
			'title-' || series,
			case when series %% 8 = 0 then null else 'nick-' || (series %% 500) end
		from generate_series(1, %d) as series`,
		pgx.Identifier{"app", collection.TableName}.Sanitize(), rows)); err != nil {
		t.Fatalf("seed companion fixture rows: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		"analyze "+pgx.Identifier{"app", collection.TableName}.Sanitize()); err != nil {
		t.Fatalf("analyze companion fixture: %v", err)
	}
}

// The whole point of the rollout: on a fixed large fixture the planner must pick
// the companion index for a nullable descending page. Asserted with EXPLAIN
// rather than by reading the DDL.
func TestNullableDescendingPageUsesTheCompanionIndex(t *testing.T) {
	ctx := context.Background()
	pool, service, _, collection := companionRolloutFixture(t)
	seedCompanionRows(t, pool, collection, 20000)
	companion := companionSpec(t, service, collection, "nickname")

	column := pgx.Identifier{"nickname"}.Sanitize()
	page := "select id from " + pgx.Identifier{"app", collection.TableName}.Sanitize() +
		" " + fmt.Sprintf(nullableDescendingPageOrderBy, column) + " limit 51"

	// Before the companion exists the same page cannot be answered from an index.
	if plan := explain(t, pool, page); strings.Contains(plan, companion.Name) {
		t.Fatalf("companion index used before it was built:\n%s", plan)
	}

	activateCompanionRollout(t, service)
	if _, err := service.MaintainCompanionIndexes(ctx); err != nil {
		t.Fatalf("maintain companion indexes: %v", err)
	}
	if _, err := pool.Exec(ctx,
		"analyze "+pgx.Identifier{"app", collection.TableName}.Sanitize()); err != nil {
		t.Fatal(err)
	}

	plan := explain(t, pool, page)
	if !strings.Contains(plan, companion.Name) {
		t.Fatalf("nullable descending page did not use companion %s:\n%s", companion.Name, plan)
	}
	if strings.Contains(plan, "Sort Method") || strings.Contains(plan, "Sort Key") {
		t.Fatalf("nullable descending page still sorts:\n%s", plan)
	}
}

func explain(t *testing.T, pool *database.Pool, statement string) string {
	t.Helper()
	rows, err := pool.Query(context.Background(), "explain (analyze, buffers off, costs off) "+statement)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line)
		plan.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return plan.String()
}

// D2-4: adding a field to an existing collection may introduce a companion that
// nothing has built, so the change itself closes the gate.
func TestCatalogChangeDropsTheCompanionCapabilityToPending(t *testing.T) {
	ctx := context.Background()
	pool, service, administratorID, collection := companionRolloutFixture(t)
	activateCompanionRollout(t, service)
	if _, err := service.MaintainCompanionIndexes(ctx); err != nil {
		t.Fatal(err)
	}

	update := UpdateCollectionInput{Name: collection.Name, Fields: []FieldInput{
		{Name: "title", Type: TypeText, Required: true, Indexed: true},
		{Name: "nickname", Type: TypeText, Indexed: true},
		{Name: "handle", Type: TypeText, Indexed: true},
	}}
	plan, err := service.PlanUpdate(ctx, update)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyUpdate(ctx, update, plan.Checksum, plan.BaseVersion, administratorID); err != nil {
		t.Fatal(err)
	}

	status, err := service.CompanionIndexStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != CompanionIndexPending || status.Ready() ||
		status.CatalogVersion != nil || status.ReadyAt != nil {
		t.Fatalf("capability after a catalog change = %#v", status)
	}
	// Maintenance cannot promote a state nobody opened.
	if _, err := service.MaintainCompanionIndexes(ctx); !errors.Is(err, ErrCompanionIndexState) {
		t.Fatalf("maintenance on a pending capability error = %v", err)
	}

	// The rollout resumes from the new catalog and covers the added field.
	activateCompanionRollout(t, service)
	ready, err := service.MaintainCompanionIndexes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ready.State != CompanionIndexReady || ready.IndexCount != 2 || ready.Generation != 2 {
		t.Fatalf("second rollout receipt = %#v", ready)
	}
	assertCompanionIndexShape(t, pool, companionSpec(t, service, collection, "handle"))
}

func TestCompanionIndexActivationRequiresAProvenWriterFleet(t *testing.T) {
	ctx := context.Background()
	pool, service, _, _ := companionRolloutFixture(t)
	instanceID, err := service.instance()
	if err != nil {
		t.Fatal(err)
	}

	for name, proof := range map[string]SchemaWriterFleetProof{
		"empty proof":     {},
		"unknown witness": {Instances: []uuid.UUID{uuid.MustParse("0199c100-0000-7000-8000-000000000001")}},
		"extra witness": {Instances: []uuid.UUID{
			instanceID, uuid.MustParse("0199c100-0000-7000-8000-000000000002"),
		}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.ActivateCompanionIndexes(ctx, proof); !errors.Is(err, ErrCompanionIndexFleet) {
				t.Fatalf("activation error = %v, want ErrCompanionIndexFleet", err)
			}
		})
	}

	// A live writer that predates the companion contract blocks activation even
	// when the operator names it, which is what the separate lease exists for.
	if _, err := pool.Exec(ctx, `update pgbase_meta.schema_writer_leases
		set compiler_version = 1 where instance_id = $1`, instanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivateCompanionIndexes(ctx,
		SchemaWriterFleetProof{Instances: []uuid.UUID{instanceID}}); !errors.Is(err, ErrCompanionIndexFleet) {
		t.Fatalf("activation with an old schema writer error = %v, want ErrCompanionIndexFleet", err)
	}

	// An expired lease is not a live writer, but it is also not a witness.
	if _, err := pool.Exec(ctx, `update pgbase_meta.schema_writer_leases
		set compiler_version = 2,
			started_at = transaction_timestamp() - interval '10 minutes',
			heartbeat_at = transaction_timestamp() - interval '10 minutes',
			lease_expires_at = transaction_timestamp() - interval '9 minutes'
		where instance_id = $1`, instanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivateCompanionIndexes(ctx,
		SchemaWriterFleetProof{Instances: []uuid.UUID{instanceID}}); !errors.Is(err, ErrCompanionIndexFleet) {
		t.Fatalf("activation with only an expired lease error = %v, want ErrCompanionIndexFleet", err)
	}
}

// A downgrade turns the capability off and deliberately keeps the extra objects,
// so a forward fix does not have to rebuild them.
func TestCompanionIndexRollbackDisablesTheGateButKeepsTheIndex(t *testing.T) {
	ctx := context.Background()
	pool, service, _, collection := companionRolloutFixture(t)
	activateCompanionRollout(t, service)
	if _, err := service.MaintainCompanionIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	companion := companionSpec(t, service, collection, "nickname")

	receipt, err := service.RollbackCompanionIndexes(ctx)
	if err != nil {
		t.Fatalf("roll back companion index rollout: %v", err)
	}
	if receipt.State != CompanionIndexPending {
		t.Fatalf("rollback receipt = %#v", receipt)
	}
	status, err := service.CompanionIndexStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Ready() || status.ReadyAt != nil || status.CatalogVersion != nil {
		t.Fatalf("capability after rollback = %#v", status)
	}
	assertCompanionIndexShape(t, pool, companion)
}
