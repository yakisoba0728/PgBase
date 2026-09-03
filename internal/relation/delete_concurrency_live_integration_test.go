//go:build integration

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
		input, err := cross.bindings.decode(PatchWrite, map[string]json.RawMessage{
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
		if err := cross.bindings.lockCollections(ctx, writerTx, input); err != nil {
			t.Fatal(err)
		}
		if err := cross.bindings.lockRows(ctx, writerTx, cross.sourceRecord, sourceStored, input); err != nil {
			t.Fatal(err)
		}
		if err := cross.bindings.replaceMulti(ctx, writerTx, cross.sourceRecord, input); err != nil {
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
