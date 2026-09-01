//go:build integration

package relation

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

type deleteCrossCollectionFixture struct {
	graph        *DeleteGraph
	bindings     *Bindings
	source       schema.Collection
	target       schema.Collection
	field        schema.Field
	root         uuid.UUID
	sourceRecord uuid.UUID
}

func (f *deleteLiveFixture) createCrossCollectionRestrict(
	t *testing.T,
	ctx context.Context,
) deleteCrossCollectionFixture {
	t.Helper()
	sourceID := uuid.MustParse("0199aa24-0000-7000-8000-000000000010")
	targetID := uuid.MustParse("0199aa24-0000-7000-8000-000000000020")
	root := uuid.MustParse("0199aa24-0000-7000-8000-000000000021")
	sourceRecord := uuid.MustParse("0199aa24-0000-7000-8000-000000000011")
	storage := schema.NewRelationPhysicalNames("phantom_sources", "restrict_targets").MultiTable
	field := schema.Field{
		ID:         uuid.MustParse("0199aa24-0000-7000-8000-000000000012"),
		Name:       "restrict_targets",
		ColumnName: "restrict_targets",
		Type:       schema.TypeRelation,
		Indexed:    true,
		Ordinal:    0,
		Relation: deleteSpec(
			targetID,
			schema.RelationMultiple,
			0,
			100,
			schema.RelationRestrict,
			schema.RelationStorageEdgeTableV1,
			storage,
		),
	}
	source := schema.Collection{
		ID: sourceID, Name: "phantom_sources", TableName: "phantom_sources", Fields: []schema.Field{field},
	}
	target := schema.Collection{
		ID: targetID, Name: "phantom_targets", TableName: "phantom_targets",
	}
	catalog := schema.Catalog{
		Version: 9,
		Collections: map[string]schema.Collection{
			source.Name: source,
			target.Name: target,
		},
		Incoming: map[uuid.UUID][]schema.RelationRef{
			targetID: {{SourceCollectionID: sourceID, SourceFieldID: field.ID, Spec: *field.Relation}},
		},
	}
	graph, err := BindDeleteGraph(catalog)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := Bind(catalog, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, `create table app.phantom_targets (id uuid primary key);
		create table app.phantom_sources (id uuid primary key)`); err != nil {
		t.Fatalf("create cross-collection phantom tables: %v", err)
	}
	if _, err := f.conn.Exec(ctx, createDeleteEdgeTableSQL(
		field.Relation.Storage.Name,
		source.TableName,
		target.TableName,
	)); err != nil {
		t.Fatalf("create cross-collection phantom edge table: %v", err)
	}
	if _, err := f.conn.Exec(ctx, `insert into app.phantom_targets (id) values ($1)`, root); err != nil {
		t.Fatalf("seed cross-collection phantom target: %v", err)
	}
	if _, err := f.conn.Exec(ctx, `insert into app.phantom_sources (id) values ($1)`, sourceRecord); err != nil {
		t.Fatalf("seed cross-collection phantom source: %v", err)
	}
	return deleteCrossCollectionFixture{
		graph: graph, bindings: bindings, source: source, target: target,
		field: field, root: root, sourceRecord: sourceRecord,
	}
}

func waitForAdvisoryWait(t *testing.T, ctx context.Context, observer *pgx.Conn, pid uint32) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		err := observer.QueryRow(ctx, `select coalesce((
			select wait_event_type = 'Lock' and wait_event = 'advisory'
			from pg_stat_activity where pid = $1
		), false)`, int32(pid)).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("connection did not wait on the relation collection advisory lock")
}

type deleteConcurrencyOutcome struct {
	first bool
	plan  DeletePlan
	err   error
}

func waitDeleteOutcome(t *testing.T, results <-chan deleteConcurrencyOutcome) deleteConcurrencyOutcome {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("opposite delete plans did not make progress")
		return deleteConcurrencyOutcome{}
	}
}

func (f *deleteLiveFixture) createRequiredCycle(
	t *testing.T,
	ctx context.Context,
) (*DeleteGraph, schema.Collection, schema.Collection, uuid.UUID, uuid.UUID) {
	t.Helper()
	alphaID := uuid.MustParse("0199aa24-0000-7000-8000-000000000200")
	betaID := uuid.MustParse("0199aa24-0000-7000-8000-000000000100")
	alphaRecord := uuid.MustParse("0199aa24-0000-7000-8000-000000000201")
	betaRecord := uuid.MustParse("0199aa24-0000-7000-8000-000000000101")
	alphaField := deleteSingleField(betaID, "beta", 0, schema.RelationCascade, true)
	betaField := deleteSingleField(alphaID, "alpha", 0, schema.RelationCascade, true)
	alpha := schema.Collection{ID: alphaID, Name: "alpha", TableName: "alpha", Fields: []schema.Field{alphaField}}
	beta := schema.Collection{ID: betaID, Name: "beta", TableName: "beta", Fields: []schema.Field{betaField}}
	catalog := schema.Catalog{
		Version:     8,
		Collections: map[string]schema.Collection{"alpha": alpha, "beta": beta},
		Incoming: map[uuid.UUID][]schema.RelationRef{
			betaID:  {{SourceCollectionID: alphaID, SourceFieldID: alphaField.ID, Spec: *alphaField.Relation}},
			alphaID: {{SourceCollectionID: betaID, SourceFieldID: betaField.ID, Spec: *betaField.Relation}},
		},
	}
	graph, err := BindDeleteGraph(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, `create table app.alpha (id uuid primary key, beta uuid not null);
		create table app.beta (id uuid primary key, alpha uuid not null);
		alter table app.alpha add foreign key (beta) references app.beta(id) on delete no action deferrable initially deferred;
		alter table app.beta add foreign key (alpha) references app.alpha(id) on delete no action deferrable initially deferred`); err != nil {
		t.Fatalf("create required cycle tables: %v", err)
	}
	tx, err := f.conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `insert into app.alpha (id, beta) values ($1, $2)`, alphaRecord, betaRecord); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `insert into app.beta (id, alpha) values ($1, $2)`, betaRecord, alphaRecord); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return graph, alpha, beta, alphaRecord, betaRecord
}
