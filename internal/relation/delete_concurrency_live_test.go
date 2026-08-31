package relation

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func TestDeleteGraphSeparateConnectionConcurrencyPostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	fixture := newDeleteLiveFixture(t, ctx)
	defer fixture.conn.Close(ctx)

	t.Run("read committed sees edge committed while waiting for collection lock", func(t *testing.T) {
		cross := fixture.createCrossCollectionRestrict(t, ctx)
		input, err := cross.bindings.Decode(PatchInput, map[string]json.RawMessage{
			cross.field.Name: json.RawMessage(`["` + cross.root.String() + `"]`),
		})
		if err != nil {
			t.Fatal(err)
		}
		wantCollectionOrder := []uuid.UUID{cross.source.ID, cross.target.ID}
		if got := cross.bindings.collectionLockIDs(input); !reflect.DeepEqual(got, wantCollectionOrder) {
			t.Fatalf("writer collection order=%v, want UUID order %v", got, wantCollectionOrder)
		}
		if got := cross.graph.participatingCollections(cross.target.ID); !reflect.DeepEqual(got, wantCollectionOrder) {
			t.Fatalf("delete collection order=%v, want writer order %v", got, wantCollectionOrder)
		}

		writer, err := pgx.Connect(ctx, fixture.url)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close(ctx)
		writerTx, err := writer.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer writerTx.Rollback(ctx)
		if err := cross.bindings.LockCollections(ctx, writerTx, input); err != nil {
			t.Fatal(err)
		}
		if err := cross.bindings.LockRows(ctx, writerTx, cross.sourceRecord, SourceStored, input); err != nil {
			t.Fatal(err)
		}
		if err := cross.bindings.ReplaceMulti(ctx, writerTx, cross.sourceRecord, input); err != nil {
			t.Fatal(err)
		}

		deleter, err := pgx.Connect(ctx, fixture.url)
		if err != nil {
			t.Fatal(err)
		}
		defer deleter.Close(ctx)
		deleterTx, err := deleter.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer deleterTx.Rollback(ctx)
		// Establish a pre-wait Read Committed snapshot. Plan must use a new
		// statement snapshot after the advisory lock becomes available.
		var before int
		if err := deleterTx.QueryRow(ctx, `select count(*) from app.phantom_targets`).Scan(&before); err != nil || before != 1 {
			t.Fatalf("establish deleter snapshot: count=%d err=%v", before, err)
		}
		type planResult struct {
			plan DeletePlan
			err  error
		}
		result := make(chan planResult, 1)
		go func() {
			plan, err := cross.graph.Plan(ctx, deleterTx, RecordRef{
				CollectionID: cross.target.ID,
				RecordID:     cross.root,
			})
			result <- planResult{plan: plan, err: err}
		}()

		observer, err := pgx.Connect(ctx, fixture.url)
		if err != nil {
			t.Fatal(err)
		}
		defer observer.Close(ctx)
		waitForAdvisoryWait(t, ctx, observer, deleter.PgConn().PID())
		if err := writerTx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case outcome := <-result:
			assertValidation(t, outcome.err, "id", "relation_delete_restricted")
			if len(outcome.plan.Deletes()) != 0 {
				t.Fatalf("restricted plan exposed a partial closure: %v", outcome.plan.Deletes())
			}
		case <-time.After(4 * time.Second):
			t.Fatal("deleter did not resume after writer commit")
		}
		var edgeCount int
		if err := fixture.conn.QueryRow(ctx, "select count(*) from "+
			pgx.Identifier{"pgbase_relation", cross.field.Relation.Storage.Name}.Sanitize()).Scan(&edgeCount); err != nil {
			t.Fatal(err)
		}
		if edgeCount != 1 {
			t.Fatalf("writer edge was not committed: %d", edgeCount)
		}
	})

	t.Run("opposite roots share one global collection order", func(t *testing.T) {
		graph, alpha, beta, alphaRecord, betaRecord := fixture.createRequiredCycle(t, ctx)
		firstConn, err := pgx.Connect(ctx, fixture.url)
		if err != nil {
			t.Fatal(err)
		}
		defer firstConn.Close(ctx)
		secondConn, err := pgx.Connect(ctx, fixture.url)
		if err != nil {
			t.Fatal(err)
		}
		defer secondConn.Close(ctx)
		firstTx, err := firstConn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer firstTx.Rollback(ctx)
		secondTx, err := secondConn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer secondTx.Rollback(ctx)
		if _, err := firstTx.Exec(ctx, `set local lock_timeout = '4s'`); err != nil {
			t.Fatal(err)
		}
		if _, err := secondTx.Exec(ctx, `set local lock_timeout = '4s'`); err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		results := make(chan deleteConcurrencyOutcome, 2)
		go func() {
			<-start
			plan, err := graph.Plan(ctx, firstTx, RecordRef{CollectionID: alpha.ID, RecordID: alphaRecord})
			results <- deleteConcurrencyOutcome{first: true, plan: plan, err: err}
		}()
		go func() {
			<-start
			plan, err := graph.Plan(ctx, secondTx, RecordRef{CollectionID: beta.ID, RecordID: betaRecord})
			results <- deleteConcurrencyOutcome{first: false, plan: plan, err: err}
		}()
		close(start)
		winner := waitDeleteOutcome(t, results)
		if winner.err != nil || len(winner.plan.Deletes()) != 2 {
			t.Fatalf("first opposite plan failed: plan=%v err=%v", winner.plan.Deletes(), winner.err)
		}
		if winner.first {
			if err := firstTx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
		} else if err := secondTx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		loser := waitDeleteOutcome(t, results)
		if loser.err != nil || len(loser.plan.Deletes()) != 2 {
			t.Fatalf("second opposite plan failed: plan=%v err=%v", loser.plan.Deletes(), loser.err)
		}
		if loser.first {
			if err := firstTx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
		} else if err := secondTx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}

		// Required cross-table cycles cannot be nulled. Deferred NO ACTION
		// permits the caller to delete the complete plan before commit.
		deleteTx, err := fixture.conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := graph.Plan(ctx, deleteTx, RecordRef{CollectionID: alpha.ID, RecordID: alphaRecord})
		if err != nil {
			deleteTx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := plan.ExecuteStorage(ctx); err != nil {
			deleteTx.Rollback(ctx)
			t.Fatal(err)
		}
		collections := map[uuid.UUID]schema.Collection{alpha.ID: alpha, beta.ID: beta}
		for _, record := range plan.Deletes() {
			collection := collections[record.CollectionID]
			if _, err := deleteTx.Exec(ctx, "delete from "+pgx.Identifier{"app", collection.TableName}.Sanitize()+" where id = $1", record.RecordID); err != nil {
				deleteTx.Rollback(ctx)
				t.Fatalf("delete required cycle record: %v", err)
			}
		}
		if err := deleteTx.Commit(ctx); err != nil {
			t.Fatalf("commit complete required cycle: %v", err)
		}
		var remaining int
		if err := fixture.conn.QueryRow(ctx, `select
			(select count(*) from app.alpha) + (select count(*) from app.beta)`).Scan(&remaining); err != nil || remaining != 0 {
			t.Fatalf("required cycle remains: count=%d err=%v", remaining, err)
		}
	})
}

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

func TestDeleteParticipantOrderMatchesMutationLockDomain(t *testing.T) {
	catalog, collection := deleteCatalogFixture()
	graph, err := BindDeleteGraph(catalog)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := Bind(catalog, collection)
	if err != nil {
		t.Fatal(err)
	}
	input, err := bindings.Decode(PatchInput, map[string]json.RawMessage{
		"cascade_links": json.RawMessage(`[]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := graph.participatingCollections(collection.ID), bindings.collectionLockIDs(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("delete/mutation lock domains differ: delete=%v mutation=%v", got, want)
	}
}
