//go:build integration

package doctor

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

// doctorSnapshotQueryCeiling bounds the statements one diagnostic snapshot may
// issue. It exists so an operator can reason about doctor's catalog cost
// without knowing how many collections, fields or relations a project has: the
// count must be identical for a two-collection project and a twenty-collection
// one. The sidecar scans below it stay O(total records) by design, but they
// are a fixed number of statements.
//
// Raised from 28 to 31 by the companion index rollout, which adds exactly three
// fixed reads: whether the capability is installed, the capability row, and the
// observed shape of every companion in one statement. The manifest itself is
// reused from the physical comparison rather than recompiled. Raise this only
// alongside statements that are likewise independent of catalog size.
// Compound indexes add two fixed reads: metadata availability and all definitions.
// Views add another fixed availability probe and bulk definition read.
// Field contracts add one fixed column-capability probe to support pre-v22 upgrades.
// P6 adds one fixed SQL-view metadata capability probe, independent of catalog size.
// N1 adds one fixed user-migration-version-table capability probe so doctor can
// inspect both compatible pre-v40 histories and v40 user migration revisions.
// Record identity and advanced index metadata add one fixed column-capability
// probe each; both definitions are still loaded in their existing bulk reads.
const doctorSnapshotQueryCeiling = 41

type doctorQueryTracer struct {
	mu         sync.Mutex
	enabled    bool
	statements []string
}

func (t *doctorQueryTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.enabled {
		t.statements = append(t.statements, strings.Join(strings.Fields(data.SQL), " "))
	}
	return ctx
}

func (*doctorQueryTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (t *doctorQueryTracer) start() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.statements = nil
	t.enabled = true
}

func (t *doctorQueryTracer) stop() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enabled = false
	return append([]string(nil), t.statements...)
}

func TestSnapshotQueryCountIsIndependentOfCatalogSize(t *testing.T) {
	for _, state := range []string{"off", "dual_write", "ready"} {
		t.Run(state, func(t *testing.T) {
			small := measureDoctorSnapshotQueries(t, 2, state)
			large := measureDoctorSnapshotQueries(t, 20, state)
			if len(small.statements) != len(large.statements) {
				t.Fatalf("doctor snapshot query count scaled with catalog size: 2 collections=%d, 20 collections=%d\nsmall:\n%s\nlarge:\n%s",
					len(small.statements), len(large.statements),
					formatDoctorQueryTrace(small.statements), formatDoctorQueryTrace(large.statements))
			}
			if len(large.statements) > doctorSnapshotQueryCeiling {
				t.Fatalf("doctor snapshot issued %d statements, ceiling is %d\n%s",
					len(large.statements), doctorSnapshotQueryCeiling, formatDoctorQueryTrace(large.statements))
			}
			if state == "off" {
				want := finding(
					"record_membership_rollout_not_started",
					"Record membership rollout has not started for a database that contains records; follow the membership rollout runbook",
				)
				if small.report.OK || large.report.OK || len(small.report.Findings) != 1 ||
					len(large.report.Findings) != 1 || small.report.Findings[0] != want || large.report.Findings[0] != want {
					t.Fatalf("query-count off fixture did not report the rollout: small=%#v large=%#v", small.report, large.report)
				}
				return
			}
			if !small.report.OK || !large.report.OK {
				t.Fatalf("query-count fixture is not healthy: small=%#v large=%#v", small.report, large.report)
			}
		})
	}
}

type measuredDoctorSnapshot struct {
	statements []string
	report     Report
}

func measureDoctorSnapshotQueries(t *testing.T, collections int, membershipState string) measuredDoctorSnapshot {
	t.Helper()
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	pool, err := database.Open(ctx, databaseURL, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	seedQueryCountCatalog(t, ctx, pool, collections, membershipState)

	status, err := migrate.New(pool, nil).Status(ctx)
	if err != nil {
		t.Fatal(err)
	}

	tracer := &doctorQueryTracer{}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.Tracer = tracer
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(ctx); err != nil {
			t.Errorf("close doctor query-count connection: %v", err)
		}
	})
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	report := Report{Findings: []Finding{}}
	tracer.start()
	inspectErr := inspectSnapshot(ctx, tx, status, &report)
	statements := tracer.stop()
	if inspectErr != nil {
		t.Fatalf("inspect doctor snapshot: %v", inspectErr)
	}
	report.OK = len(report.Findings) == 0
	return measuredDoctorSnapshot{statements: statements, report: report}
}

// seedQueryCountCatalog builds a planner-created catalog whose size is the only
// variable between measurements: every collection carries the same scalar
// fields, a single relation and a multiple relation, and every collection holds
// records so the membership scans have rows to visit.
func seedQueryCountCatalog(t *testing.T, ctx context.Context, pool *database.Pool, collections int, membershipState string) {
	t.Helper()
	store := schema.NewStore(pool)
	now := time.Now().UTC()
	target := createEmptyDoctorCollection(t, ctx, pool, store, "query_count_target", now)
	catalog := schema.Catalog{Collections: map[string]schema.Collection{target.Name: target}}
	type seededCollection struct {
		id       uuid.UUID
		recordID *uuid.UUID
	}
	seeded := []seededCollection{{id: target.ID}}

	for index := 0; index < collections; index++ {
		name := fmt.Sprintf("query_count_%02d", index)
		maximum := 5
		plan, err := schema.CompilePlan(catalog, schema.NewCreateChange(schema.CreateCollectionInput{
			Name: name,
			Fields: []schema.FieldInput{
				{Name: "title", Type: schema.TypeText, Required: true, Indexed: true},
				{Name: "count", Type: schema.TypeInteger},
				{
					Name: "owner", Type: schema.TypeRelation,
					Relation: &schema.RelationInput{
						TargetCollection: target.Name, Cardinality: schema.RelationSingle,
						OnDelete: schema.RelationCascade,
					},
				},
				{
					Name: "labels", Type: schema.TypeRelation,
					Relation: &schema.RelationInput{
						TargetCollection: target.Name, Cardinality: schema.RelationMultiple,
						MaxSelect: &maximum, OnDelete: schema.RelationSetNull,
					},
				},
			},
		}))
		if err != nil {
			t.Fatalf("plan query-count collection %s: %v", name, err)
		}
		for statementIndex, statement := range plan.Statements {
			if _, err := pool.Exec(ctx, statement); err != nil {
				t.Fatalf("execute query-count DDL %d for %s: %v\n%s", statementIndex, name, err, statement)
			}
		}
		collection := schema.Collection{ID: uuid.New(), Name: name, TableName: name, CreatedAt: now}
		for _, planned := range plan.Definition.Fields {
			collection.Fields = append(collection.Fields, schema.Field{
				ID: uuid.New(), Name: planned.Name, ColumnName: planned.Name, Type: planned.Type,
				Required: planned.Required, Unique: planned.Unique, Indexed: planned.Indexed,
				Ordinal: planned.Ordinal,
			})
		}
		if err := store.InsertCollection(ctx, collection, plan.Definition, now); err != nil {
			t.Fatalf("insert query-count metadata for %s: %v", name, err)
		}
		recordID := uuid.New()
		if _, err := pool.Exec(ctx, `insert into app.`+pgx.Identifier{name}.Sanitize()+
			` (id, created_at, updated_at, title) values ($1, $2, $2, 'row')`, recordID, now); err != nil {
			t.Fatalf("seed query-count record for %s: %v", name, err)
		}
		catalog.Collections[collection.Name] = collection
		seeded = append(seeded, seededCollection{id: collection.ID, recordID: &recordID})
	}
	for _, collection := range seeded {
		seedQueryCountMembershipCollection(t, ctx, pool, collection.id, collection.recordID, membershipState, now)
	}
	seedQueryCountCapability(t, ctx, pool, len(seeded), membershipState, now)
}

func seedQueryCountMembershipCollection(
	t *testing.T,
	ctx context.Context,
	pool *database.Pool,
	collectionID uuid.UUID,
	recordID *uuid.UUID,
	state string,
	now time.Time,
) {
	t.Helper()
	lastOrdinal := 0
	if state == "ready" && recordID != nil {
		lastOrdinal = 1
	}
	generation := 1
	if state == "off" {
		generation = 0
	}
	// Creating a collection already installs generation-0 counter and backfill
	// rows, and the backfill references the counter, so the child row is removed
	// before the counter generation moves and reinserted afterwards.
	if _, err := pool.Exec(ctx, `delete from pgbase_meta.record_membership_backfills where collection_id = $1`, collectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collection_creation_counters
		(collection_id, generation, last_ordinal, updated_at) values ($1, $2, $3, $4)
		on conflict (collection_id) do update
		set generation = excluded.generation, last_ordinal = excluded.last_ordinal, updated_at = excluded.updated_at`,
		collectionID, generation, lastOrdinal, now); err != nil {
		t.Fatal(err)
	}
	if state == "off" {
		if _, err := pool.Exec(ctx, `insert into pgbase_meta.record_membership_backfills
			(collection_id, generation, state, updated_at) values ($1, 0, 'off', $2)`, collectionID, now); err != nil {
			t.Fatal(err)
		}
		return
	}
	if state == "ready" {
		if _, err := pool.Exec(ctx, `insert into pgbase_meta.record_membership_backfills
			(collection_id, generation, state, cutover_count, last_assigned_ordinal,
			 validated_base_count, validated_sidecar_count, updated_at, completed_at)
			values ($1, 1, 'ready', 0, 0, 0, 0, $2, $2)
			on conflict (collection_id) do update
			set generation = excluded.generation, state = excluded.state,
				cutover_count = excluded.cutover_count, last_assigned_ordinal = excluded.last_assigned_ordinal,
				validated_base_count = excluded.validated_base_count,
				validated_sidecar_count = excluded.validated_sidecar_count,
				updated_at = excluded.updated_at, completed_at = excluded.completed_at`, collectionID, now); err != nil {
			t.Fatal(err)
		}
		if recordID != nil {
			if _, err := pool.Exec(ctx, `insert into pgbase_meta.record_creation_ordinals
				(collection_id, record_id, ordinal) values ($1, $2, 1)`, collectionID, *recordID); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.record_membership_backfills
		(collection_id, generation, state, cutover_count, last_assigned_ordinal, updated_at)
		values ($1, 1, 'backfilling', 0, 0, $2)
		on conflict (collection_id) do update
		set generation = excluded.generation, state = excluded.state,
			cutover_count = excluded.cutover_count, last_assigned_ordinal = excluded.last_assigned_ordinal,
			updated_at = excluded.updated_at`, collectionID, now); err != nil {
		t.Fatal(err)
	}
}

func seedQueryCountCapability(t *testing.T, ctx context.Context, pool *database.Pool, collections int, state string, now time.Time) {
	t.Helper()
	if state == "off" {
		return
	}
	capability := `update pgbase_meta.record_membership_capability set
		state = 'dual_write', generation = 1, minimum_writer_version = 1,
		cutover_at = $1, cutover_collection_count = $2, cutover_record_count = 0,
		updated_at = $1 where singleton`
	if state == "ready" {
		capability = `update pgbase_meta.record_membership_capability set
			state = 'ready', generation = 1, minimum_writer_version = 1,
			cutover_at = $1, cutover_collection_count = $2, cutover_record_count = 0,
			ready_at = $1, ready_collection_count = $2, updated_at = $1 where singleton`
	}
	if _, err := pool.Exec(ctx, capability, now, collections); err != nil {
		t.Fatal(err)
	}
}

func formatDoctorQueryTrace(statements []string) string {
	var builder strings.Builder
	for index, statement := range statements {
		fmt.Fprintf(&builder, "  %2d: %s\n", index+1, statement)
	}
	return builder.String()
}
