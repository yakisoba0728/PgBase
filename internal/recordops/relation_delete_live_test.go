//go:build integration

package recordops

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/query"
	"github.com/webisnoteasy/PgBase/internal/recordartifact"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func TestRecordOperationsDeleteGraphRestrictSingleAndMultipleRollback(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		cardinality schema.RelationCardinality
	}{
		{name: "single", cardinality: schema.RelationSingle},
		{name: "multiple", cardinality: schema.RelationMultiple},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			pool, schemas, records, principal := newRecordOperationsTest(t)
			ctx := context.Background()
			applyDeleteCollection(t, ctx, schemas, principal.ID, schema.CreateCollectionInput{
				Name: "delete_targets", Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText, Required: true}},
			})
			applyDeleteCollection(t, ctx, schemas, principal.ID, schema.CreateCollectionInput{
				Name: "delete_blockers", Fields: []schema.FieldInput{
					{Name: "name", Type: schema.TypeText, Required: true},
					deleteRelationField("target", "delete_targets", testCase.cardinality, schema.RelationRestrict),
				},
			})

			target := createDeleteRecord(t, ctx, records, principal, "delete_targets", "restrict-target", map[string]json.RawMessage{
				"name": json.RawMessage(`"target"`),
			})
			targetID := recordResultID(t, target)
			link := relationUUIDJSON(targetID)
			if testCase.cardinality == schema.RelationMultiple {
				link = relationUUIDArrayJSON(targetID)
			}
			blocker := createDeleteRecord(t, ctx, records, principal, "delete_blockers", "restrict-blocker", map[string]json.RawMessage{
				"name": json.RawMessage(`"blocker"`), "target": link,
			})
			blockerID := recordResultID(t, blocker)

			requestID := "delete-restrict-" + testCase.name
			_, err := records.Delete(ctx, DeleteRequest{
				Principal: principal, RequestID: requestID, Collection: "delete_targets", ID: targetID,
				IfMatch: EntityTagSet{Present: true, Values: []string{target.Record.ETag}},
			})
			assertDeleteGraphValidation(t, err, "relation_delete_restricted")

			if _, err := records.Get(ctx, GetRequest{Principal: principal, RequestID: "restrict-target-get", Collection: "delete_targets", ID: targetID}); err != nil {
				t.Fatalf("restricted target changed: %v", err)
			}
			if _, err := records.Get(ctx, GetRequest{Principal: principal, RequestID: "restrict-source-get", Collection: "delete_blockers", ID: blockerID}); err != nil {
				t.Fatalf("restricted source changed: %v", err)
			}
			assertNoDeleteRequestArtifacts(t, ctx, pool, requestID)
		})
	}
}

func TestRecordOperationsDeleteGraphBudgetValidationRollsBackClosure(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	collection := applyDeleteCollection(t, ctx, schemas, principal.ID, schema.CreateCollectionInput{
		Name: "budget_records", Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText, Required: true},
			deleteRelationField("parent", "budget_records", schema.RelationSingle, schema.RelationCascade),
		},
	})

	chain := make([]MutationResult, 34)
	for index := range chain {
		input := map[string]json.RawMessage{"name": json.RawMessage(`"node"`)}
		if index > 0 {
			input["parent"] = relationUUIDJSON(recordResultID(t, chain[index-1]))
		}
		chain[index] = createDeleteRecord(t, ctx, records, principal, "budget_records", uuid.NewString(), input)
	}
	rootID := recordResultID(t, chain[0])
	lastID := recordResultID(t, chain[len(chain)-1])
	requestID := "delete-budget-overflow"
	_, err := records.Delete(ctx, DeleteRequest{
		Principal: principal, RequestID: requestID, Collection: "budget_records", ID: rootID,
		IfMatch: EntityTagSet{Present: true, Values: []string{chain[0].Record.ETag}},
	})
	assertDeleteGraphValidation(t, err, "relation_delete_budget_exceeded")

	for _, check := range []struct {
		name string
		id   uuid.UUID
	}{{name: "root", id: rootID}, {name: "tail", id: lastID}} {
		if _, err := records.Get(ctx, GetRequest{
			Principal: principal, RequestID: "budget-get-" + check.name,
			Collection: "budget_records", ID: check.id,
		}); err != nil {
			t.Fatalf("budget failure changed %s record: %v", check.name, err)
		}
	}
	var rows, links, revisions int
	if err := pool.QueryRow(ctx, `SELECT count(*), count(parent) FROM app.budget_records`).Scan(&rows, &links); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pgbase_meta.record_revisions WHERE collection_id=$1`, collection.ID).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if rows != len(chain) || links != len(chain)-1 || revisions != len(chain) {
		t.Fatalf("budget rollback rows/links/revisions = %d/%d/%d, want %d/%d/%d",
			rows, links, revisions, len(chain), len(chain)-1, len(chain))
	}
	assertNoDeleteRequestArtifacts(t, ctx, pool, requestID)
}

func TestRecordOperationsDeleteGraphSetNullWritesOneDerivedArtifact(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	applyDeleteSetNullSchema(t, ctx, schemas, principal.ID)

	deletedTarget := createDeleteRecord(t, ctx, records, principal, "null_targets", "set-null-target", map[string]json.RawMessage{
		"name": json.RawMessage(`"deleted"`),
	})
	keptTarget := createDeleteRecord(t, ctx, records, principal, "null_targets", "set-null-kept", map[string]json.RawMessage{
		"name": json.RawMessage(`"kept"`),
	})
	deletedID := recordResultID(t, deletedTarget)
	keptID := recordResultID(t, keptTarget)
	survivor := createDeleteRecord(t, ctx, records, principal, "null_survivors", "set-null-source", map[string]json.RawMessage{
		"name":  json.RawMessage(`"survivor"`),
		"owner": relationUUIDJSON(deletedID),
		"links": relationUUIDArrayJSON(keptID, deletedID),
	})
	survivorID := recordResultID(t, survivor)
	before, err := records.Get(ctx, GetRequest{
		Principal: principal, RequestID: "set-null-before", Collection: "null_survivors", ID: survivorID,
	})
	if err != nil {
		t.Fatal(err)
	}

	requestID := "delete-set-null-0001"
	result, err := records.Delete(ctx, DeleteRequest{
		Principal: principal, RequestID: requestID, Collection: "null_targets", ID: deletedID,
	})
	if err != nil {
		t.Fatalf("delete set-null target: %v", err)
	}
	if result.Record != nil || result.EventID == uuid.Nil || result.Compatibility != CompatibilityLastWriteWins {
		t.Fatalf("delete result = %#v", result)
	}
	if _, err := records.Get(ctx, GetRequest{
		Principal: principal, RequestID: "set-null-deleted-get", Collection: "null_targets", ID: deletedID,
	}); !errors.Is(err, query.ErrRecordNotFound) {
		t.Fatalf("deleted target Get error = %v", err)
	}
	after, err := records.Get(ctx, GetRequest{
		Principal: principal, RequestID: "set-null-after", Collection: "null_survivors", ID: survivorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if after.Data["owner"] != nil || !reflect.DeepEqual(after.Data["links"], []string{keptID.String()}) {
		t.Fatalf("surviving relation values = owner:%#v links:%#v", after.Data["owner"], after.Data["links"])
	}
	if after.Version != before.Version+1 || after.ETag == before.ETag || after.Data["updated_at"] == before.Data["updated_at"] {
		t.Fatalf("survivor transition before=%#v after=%#v", before, after)
	}

	artifacts := readDeleteRequestArtifacts(t, ctx, pool, requestID)
	if len(artifacts) != 2 {
		t.Fatalf("delete graph artifacts = %#v, want 2", artifacts)
	}
	update := artifacts[0]
	deleted := artifacts[1]
	if update.recordID != survivorID || update.operation != "update" ||
		!reflect.DeepEqual(update.changedFields, []string{"links", "owner"}) ||
		update.previousVersion != before.Version || update.newVersion != after.Version ||
		update.previousETag != before.ETag || update.newETag != after.ETag ||
		update.compatibility != "none" {
		t.Fatalf("derived update artifact = %#v", update)
	}
	if deleted.recordID != deletedID || deleted.operation != "delete" || len(deleted.changedFields) != 0 ||
		deleted.eventID != result.EventID || deleted.eventID == update.eventID ||
		deleted.previousETag != deletedTarget.Record.ETag || deleted.newETag != "" ||
		deleted.compatibility != string(CompatibilityLastWriteWins) {
		t.Fatalf("root delete artifact = %#v result=%#v", deleted, result)
	}
	for _, artifact := range artifacts {
		if artifact.actorID != principal.ID || artifact.requestID != requestID {
			t.Fatalf("artifact lost root cause context: %#v", artifact)
		}
	}
	outboxes := readDeleteRequestOutboxes(t, ctx, pool, requestID)
	if len(outboxes) != 2 {
		t.Fatalf("delete graph outboxes = %#v, want 2", outboxes)
	}
	if outboxes[0].EventID != update.eventID || outboxes[0].Operation != "update" ||
		outboxes[0].RecordID != survivorID || outboxes[0].RecordVersion != after.Version || outboxes[0].Tombstone ||
		!reflect.DeepEqual(outboxes[0].Changes, map[string]any{
			"links": []any{keptID.String()}, "owner": nil,
		}) {
		t.Fatalf("derived update outbox = %#v", outboxes[0])
	}
	if outboxes[1].EventID != result.EventID || outboxes[1].Operation != "delete" ||
		outboxes[1].RecordID != deletedID || outboxes[1].RecordVersion != deleted.newVersion ||
		!outboxes[1].Tombstone || len(outboxes[1].Changes) != 0 {
		t.Fatalf("root delete outbox = %#v", outboxes[1])
	}

	var outboxCount, survivorRevision, rootRevision, position int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pgbase_meta.record_outbox o
		JOIN pgbase_meta.record_audit_events a USING (event_id) WHERE a.request_id = $1`, requestID).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE record_id=$1), count(*) FILTER (WHERE record_id=$2)
		FROM pgbase_meta.record_revisions`, survivorID, deletedID).Scan(&survivorRevision, &rootRevision); err != nil {
		t.Fatal(err)
	}
	names := schema.NewRelationPhysicalNames("null_survivors", "links")
	if err := pool.QueryRow(ctx, `SELECT position FROM "pgbase_relation".`+quoteDeleteIdentifier(names.MultiTable)+`
		WHERE source_id=$1 AND target_id=$2`, survivorID, keptID).Scan(&position); err != nil {
		t.Fatal(err)
	}
	if outboxCount != 2 || survivorRevision != 1 || rootRevision != 0 || position != 0 {
		t.Fatalf("outbox/revisions/position = %d/%d/%d/%d", outboxCount, survivorRevision, rootRevision, position)
	}
}

func TestRecordOperationsDeleteGraphSetNullArtifactFailureRollsBackEverything(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	applyDeleteSetNullSchema(t, ctx, schemas, principal.ID)
	target := createDeleteRecord(t, ctx, records, principal, "null_targets", "rollback-target", map[string]json.RawMessage{
		"name": json.RawMessage(`"target"`),
	})
	targetID := recordResultID(t, target)
	survivor := createDeleteRecord(t, ctx, records, principal, "null_survivors", "rollback-source", map[string]json.RawMessage{
		"name": json.RawMessage(`"survivor"`), "owner": relationUUIDJSON(targetID), "links": relationUUIDArrayJSON(targetID),
	})
	survivorID := recordResultID(t, survivor)
	before, err := records.Get(ctx, GetRequest{
		Principal: principal, RequestID: "rollback-before", Collection: "null_survivors", ID: survivorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION pgbase_meta.fail_relation_delete_outbox()
		RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected'; END $$;
		CREATE TRIGGER fail_relation_delete_outbox BEFORE INSERT ON pgbase_meta.record_outbox
		FOR EACH ROW WHEN (NEW.operation = 'update') EXECUTE FUNCTION pgbase_meta.fail_relation_delete_outbox()`); err != nil {
		t.Fatal(err)
	}

	requestID := "delete-set-null-rollback"
	if _, err := records.Delete(ctx, DeleteRequest{
		Principal: principal, RequestID: requestID, Collection: "null_targets", ID: targetID,
		IfMatch: EntityTagSet{Present: true, Values: []string{target.Record.ETag}},
	}); err == nil {
		t.Fatal("injected artifact failure did not fail delete")
	}
	if _, err := records.Get(ctx, GetRequest{
		Principal: principal, RequestID: "rollback-target-get", Collection: "null_targets", ID: targetID,
	}); err != nil {
		t.Fatalf("target was not rolled back: %v", err)
	}
	after, err := records.Get(ctx, GetRequest{
		Principal: principal, RequestID: "rollback-after", Collection: "null_survivors", ID: survivorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if after.ETag != before.ETag || after.Version != before.Version ||
		after.Data["owner"] != targetID.String() || !reflect.DeepEqual(after.Data["links"], []string{targetID.String()}) {
		t.Fatalf("rollback survivor before=%#v after=%#v", before, after)
	}
	assertNoDeleteRequestArtifacts(t, ctx, pool, requestID)
}

func TestRecordOperationsDeleteGraphChecksOnlyRootIfMatch(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	applyDeleteSetNullSchema(t, ctx, schemas, principal.ID)
	target := createDeleteRecord(t, ctx, records, principal, "null_targets", "if-match-target", map[string]json.RawMessage{
		"name": json.RawMessage(`"target"`),
	})
	targetID := recordResultID(t, target)
	survivor := createDeleteRecord(t, ctx, records, principal, "null_survivors", "if-match-survivor", map[string]json.RawMessage{
		"name": json.RawMessage(`"survivor"`), "owner": relationUUIDJSON(targetID),
	})
	survivorID := recordResultID(t, survivor)
	if _, err := records.Update(ctx, UpdateRequest{
		Principal: principal, RequestID: "if-match-source-update", Collection: "null_survivors", ID: survivorID,
		IfMatch: EntityTagSet{Present: true, Values: []string{survivor.Record.ETag}},
		Input:   map[string]json.RawMessage{"name": json.RawMessage(`"changed"`)},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := records.Delete(ctx, DeleteRequest{
		Principal: principal, RequestID: "if-match-root-delete", Collection: "null_targets", ID: targetID,
		IfMatch: EntityTagSet{Present: true, Values: []string{target.Record.ETag}},
	}); err != nil {
		t.Fatalf("derived source ETag blocked root delete: %v", err)
	}

	staleTarget := createDeleteRecord(t, ctx, records, principal, "null_targets", "stale-root-target", map[string]json.RawMessage{
		"name": json.RawMessage(`"before"`),
	})
	staleTargetID := recordResultID(t, staleTarget)
	blockedSource := createDeleteRecord(t, ctx, records, principal, "null_survivors", "stale-root-source", map[string]json.RawMessage{
		"name": json.RawMessage(`"blocked"`), "owner": relationUUIDJSON(staleTargetID),
	})
	blockedSourceID := recordResultID(t, blockedSource)
	currentTarget, err := records.Update(ctx, UpdateRequest{
		Principal: principal, RequestID: "stale-root-update", Collection: "null_targets", ID: staleTargetID,
		IfMatch: EntityTagSet{Present: true, Values: []string{staleTarget.Record.ETag}},
		Input:   map[string]json.RawMessage{"name": json.RawMessage(`"after"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	requestID := "stale-root-delete"
	_, err = records.Delete(ctx, DeleteRequest{
		Principal: principal, RequestID: requestID, Collection: "null_targets", ID: staleTargetID,
		IfMatch: EntityTagSet{Present: true, Values: []string{staleTarget.Record.ETag}},
	})
	var precondition *Error
	if !errors.As(err, &precondition) || precondition.Code != CodePreconditionFailed ||
		precondition.CurrentETag != currentTarget.Record.ETag {
		t.Fatalf("stale root delete error = %#v", err)
	}
	blockedAfter, err := records.Get(ctx, GetRequest{
		Principal: principal, RequestID: "stale-root-source-get", Collection: "null_survivors", ID: blockedSourceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if blockedAfter.ETag != blockedSource.Record.ETag || blockedAfter.Data["owner"] != staleTargetID.String() {
		t.Fatalf("stale root precondition changed source: before=%#v after=%#v", blockedSource.Record, blockedAfter)
	}
	if _, err := records.Get(ctx, GetRequest{
		Principal: principal, RequestID: "stale-root-target-get", Collection: "null_targets", ID: staleTargetID,
	}); err != nil {
		t.Fatalf("stale root precondition changed root: %v", err)
	}
	assertNoDeleteRequestArtifacts(t, ctx, pool, requestID)
}

func TestRecordOperationsDeleteGraphCascadeSelfCycleDiamondAndInternalRestrict(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	applyDeleteCascadeGraphSchema(t, ctx, schemas, principal.ID)

	root := createDeleteRecord(t, ctx, records, principal, "graph_roots", "graph-root", map[string]json.RawMessage{
		"name": json.RawMessage(`"root"`),
	})
	rootID := recordResultID(t, root)
	left := createDeleteRecord(t, ctx, records, principal, "graph_left", "graph-left", map[string]json.RawMessage{
		"name": json.RawMessage(`"left"`), "root": relationUUIDJSON(rootID),
	})
	right := createDeleteRecord(t, ctx, records, principal, "graph_right", "graph-right", map[string]json.RawMessage{
		"name": json.RawMessage(`"right"`), "root": relationUUIDJSON(rootID),
	})
	leftID := recordResultID(t, left)
	rightID := recordResultID(t, right)
	diamond := createDeleteRecord(t, ctx, records, principal, "graph_diamond", "graph-diamond", map[string]json.RawMessage{
		"name": json.RawMessage(`"diamond"`), "left": relationUUIDJSON(leftID),
		"right": relationUUIDJSON(rightID), "root_guard": relationUUIDJSON(rootID),
	})
	diamondID := recordResultID(t, diamond)
	rootUpdated, err := records.Update(ctx, UpdateRequest{
		Principal: principal, RequestID: "graph-cycle-link", Collection: "graph_roots", ID: rootID,
		IfMatch: EntityTagSet{Present: true, Values: []string{root.Record.ETag}},
		Input:   map[string]json.RawMessage{"self": relationUUIDJSON(rootID), "cycle": relationUUIDJSON(diamondID)},
	})
	if err != nil {
		t.Fatalf("link graph cycle: %v", err)
	}

	requestID := "delete-cascade-graph"
	result, err := records.Delete(ctx, DeleteRequest{
		Principal: principal, RequestID: requestID, Collection: "graph_roots", ID: rootID,
		IfMatch: EntityTagSet{Present: true, Values: []string{rootUpdated.Record.ETag}},
	})
	if err != nil {
		t.Fatalf("delete cascade graph: %v", err)
	}
	artifacts := readDeleteRequestArtifacts(t, ctx, pool, requestID)
	if len(artifacts) != 4 {
		t.Fatalf("cascade artifacts = %#v, want four deletes", artifacts)
	}
	wantRecords := map[uuid.UUID]struct{}{rootID: {}, leftID: {}, rightID: {}, diamondID: {}}
	for _, artifact := range artifacts {
		if artifact.operation != "delete" || len(artifact.changedFields) != 0 {
			t.Fatalf("non-delete cascade artifact: %#v", artifact)
		}
		if _, ok := wantRecords[artifact.recordID]; !ok {
			t.Fatalf("unexpected cascade record: %#v", artifact)
		}
		delete(wantRecords, artifact.recordID)
		if artifact.recordID == rootID && artifact.eventID != result.EventID {
			t.Fatalf("root event = %s, result = %s", artifact.eventID, result.EventID)
		}
	}
	if len(wantRecords) != 0 {
		t.Fatalf("missing cascade artifacts for %#v", wantRecords)
	}
	var outbox, revisions, remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pgbase_meta.record_outbox o
		JOIN pgbase_meta.record_audit_events a USING (event_id) WHERE a.request_id=$1`, requestID).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pgbase_meta.record_revisions
		WHERE record_id = ANY($1::uuid[])`, []uuid.UUID{rootID, leftID, rightID, diamondID}).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM app.graph_roots) + (SELECT count(*) FROM app.graph_left) +
		(SELECT count(*) FROM app.graph_right) + (SELECT count(*) FROM app.graph_diamond)`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if outbox != 4 || revisions != 0 || remaining != 0 {
		t.Fatalf("cascade outbox/revisions/remaining = %d/%d/%d", outbox, revisions, remaining)
	}
}

func TestRecordOperationsDeleteGraphReadCommittedSeesEdgeCommittedWhileWaiting(t *testing.T) {
	databaseURL, pool, schemas, records, principal := newRecordOperationsTestWithURL(t)
	ctx := context.Background()
	applyDeleteCollection(t, ctx, schemas, principal.ID, schema.CreateCollectionInput{
		Name: "phantom_targets", Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText, Required: true}},
	})
	applyDeleteCollection(t, ctx, schemas, principal.ID, schema.CreateCollectionInput{
		Name: "phantom_blockers", Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText, Required: true},
			deleteRelationField("target", "phantom_targets", schema.RelationSingle, schema.RelationRestrict),
		},
	})
	target := createDeleteRecord(t, ctx, records, principal, "phantom_targets", "phantom-target", map[string]json.RawMessage{
		"name": json.RawMessage(`"target"`),
	})
	targetID := recordResultID(t, target)
	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	targetCollection := catalog.Collections["phantom_targets"]
	blockerCollection := catalog.Collections["phantom_blockers"]
	collectionIDs := []uuid.UUID{targetCollection.ID, blockerCollection.ID}
	sort.Slice(collectionIDs, func(i, j int) bool { return collectionIDs[i].String() < collectionIDs[j].String() })
	firstCollectionLock := deleteTestCollectionLockKey(collectionIDs[0])

	writerPool, err := database.Open(ctx, databaseURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer writerPool.Close()
	writerSchemas := schema.NewService(writerPool, &schema.Cache{}, schema.Planner{})
	writerCtx, cancelWriter := context.WithCancel(ctx)
	defer cancelWriter()
	writerReady := make(chan deleteWriterPreparation, 1)
	releaseWriter := make(chan struct{})
	writerDone := make(chan error, 1)
	blockerID := uuid.MustParse("019b0000-0000-7000-8000-000000000901")
	go func() {
		readySent := false
		err := writerPool.InTx(writerCtx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
			if _, err := writerSchemas.EnsureCurrent(writerCtx, tx); err != nil {
				return err
			}
			var backendPID int32
			if err := tx.QueryRow(writerCtx, "SELECT pg_backend_pid()").Scan(&backendPID); err != nil {
				return err
			}
			for _, collectionID := range collectionIDs {
				if _, err := tx.Exec(writerCtx, "SELECT pg_advisory_xact_lock($1)", deleteTestCollectionLockKey(collectionID)); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(writerCtx, `INSERT INTO app.phantom_blockers
				(id, created_at, updated_at, name, target)
				VALUES ($1, transaction_timestamp(), transaction_timestamp(), 'blocker', $2)`, blockerID, targetID); err != nil {
				return err
			}
			writerReady <- deleteWriterPreparation{backendPID: backendPID}
			readySent = true
			select {
			case <-releaseWriter:
				return nil
			case <-writerCtx.Done():
				return writerCtx.Err()
			}
		})
		if !readySent {
			writerReady <- deleteWriterPreparation{err: err}
		}
		writerDone <- err
	}()
	var preparation deleteWriterPreparation
	select {
	case preparation = <-writerReady:
	case <-time.After(5 * time.Second):
		cancelWriter()
		t.Fatal("timed out preparing relation writer")
	}
	if preparation.err != nil {
		t.Fatalf("prepare relation writer: %v", preparation.err)
	}

	requestID := "delete-phantom-wait"
	deleteDone := make(chan error, 1)
	go func() {
		_, err := records.Delete(ctx, DeleteRequest{
			Principal: principal, RequestID: requestID, Collection: "phantom_targets", ID: targetID,
			IfMatch: EntityTagSet{Present: true, Values: []string{target.Record.ETag}},
		})
		deleteDone <- err
	}()
	if err := waitForDeleteAdvisoryWaiter(ctx, pool, preparation.backendPID, firstCollectionLock); err != nil {
		close(releaseWriter)
		writerErr := <-writerDone
		deleteErr := <-deleteDone
		t.Fatalf("%v (writer=%v delete=%v)", err, writerErr, deleteErr)
	}
	close(releaseWriter)
	if err := <-writerDone; err != nil {
		t.Fatalf("commit relation writer: %v", err)
	}
	assertDeleteGraphValidation(t, <-deleteDone, "relation_delete_restricted")
	if _, err := records.Get(ctx, GetRequest{
		Principal: principal, RequestID: "phantom-target-get", Collection: "phantom_targets", ID: targetID,
	}); err != nil {
		t.Fatalf("phantom-safe delete changed target: %v", err)
	}
	assertNoDeleteRequestArtifacts(t, ctx, pool, requestID)
}

func TestRecordOperationsDeleteGraphRetryKeepsDerivedIdentity(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	applyDeleteSetNullSchema(t, ctx, schemas, principal.ID)
	target := createDeleteRecord(t, ctx, records, principal, "null_targets", "retry-target", map[string]json.RawMessage{
		"name": json.RawMessage(`"target"`),
	})
	targetID := recordResultID(t, target)
	survivor := createDeleteRecord(t, ctx, records, principal, "null_survivors", "retry-survivor", map[string]json.RawMessage{
		"name": json.RawMessage(`"survivor"`), "owner": relationUUIDJSON(targetID),
	})
	survivorID := recordResultID(t, survivor)

	if _, err := pool.Exec(ctx, `CREATE SEQUENCE pgbase_meta.delete_retry_attempt_seq;
		CREATE SEQUENCE pgbase_meta.delete_retry_event_seq;
		CREATE FUNCTION pgbase_meta.fail_first_derived_delete_artifact()
		RETURNS trigger LANGUAGE plpgsql AS $$
		DECLARE attempt bigint;
		BEGIN
			IF NEW.request_id <> 'delete-retry-identity' OR NEW.operation <> 'update' THEN
				RETURN NEW;
			END IF;
			attempt := nextval('pgbase_meta.delete_retry_attempt_seq');
			IF attempt = 1 THEN
				PERFORM setval('pgbase_meta.delete_retry_event_seq',
					(hashtextextended(NEW.event_id::text, 0) & 4611686018427387903) + 1);
				RAISE EXCEPTION 'retry derived delete artifact' USING ERRCODE = '40001';
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER fail_first_derived_delete_artifact
		BEFORE INSERT ON pgbase_meta.record_audit_events
		FOR EACH ROW EXECUTE FUNCTION pgbase_meta.fail_first_derived_delete_artifact()`); err != nil {
		t.Fatal(err)
	}
	firstValidator := bytes.Repeat([]byte{0x11}, 32)
	secondValidator := bytes.Repeat([]byte{0x22}, 32)
	records.random = bytes.NewReader(append(append([]byte(nil), firstValidator...), secondValidator...))

	requestID := "delete-retry-identity"
	result, err := records.Delete(ctx, DeleteRequest{
		Principal: principal, RequestID: requestID, Collection: "null_targets", ID: targetID,
		IfMatch: EntityTagSet{Present: true, Values: []string{target.Record.ETag}},
	})
	if err != nil {
		t.Fatalf("delete after retry: %v", err)
	}
	if result.EventID == uuid.Nil {
		t.Fatal("delete returned a nil root event")
	}

	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sourceCollection := catalog.Collections["null_survivors"]
	digest, err := recordartifact.SchemaDigest(sourceCollection)
	if err != nil {
		t.Fatal(err)
	}
	wantETag := recordartifact.RevisionETag(sourceCollection.ID, survivorID, digest, firstValidator)
	var derivedEvent uuid.UUID
	var gotETag string
	if err := pool.QueryRow(ctx, `SELECT event_id, new_etag
		FROM pgbase_meta.record_audit_events
		WHERE request_id=$1 AND operation='update'`, requestID).Scan(&derivedEvent, &gotETag); err != nil {
		t.Fatal(err)
	}
	if gotETag != wantETag {
		t.Fatalf("derived ETag drifted across retry: got %q want %q", gotETag, wantETag)
	}
	var attempts, firstEventFingerprint, committedEventFingerprint int64
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT last_value FROM pgbase_meta.delete_retry_attempt_seq),
		(SELECT last_value FROM pgbase_meta.delete_retry_event_seq),
		(hashtextextended($1::text, 0) & 4611686018427387903) + 1`, derivedEvent).
		Scan(&attempts, &firstEventFingerprint, &committedEventFingerprint); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || committedEventFingerprint != firstEventFingerprint {
		t.Fatalf("derived event identity across %d attempts = first:%d committed:%d",
			attempts, firstEventFingerprint, committedEventFingerprint)
	}
}

func TestRecordOperationsDeleteGraphRetryAllocatesOnlyCurrentClosureIdentities(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	applyDeleteSetNullSchema(t, ctx, schemas, principal.ID)
	target := createDeleteRecord(t, ctx, records, principal, "null_targets", "retry-switch-target", map[string]json.RawMessage{
		"name": json.RawMessage(`"target"`),
	})
	targetID := recordResultID(t, target)
	stale := createDeleteRecord(t, ctx, records, principal, "null_survivors", "retry-switch-stale", map[string]json.RawMessage{
		"name": json.RawMessage(`"stale"`), "owner": relationUUIDJSON(targetID),
	})
	staleID := recordResultID(t, stale)

	if _, err := pool.Exec(ctx, `CREATE SEQUENCE pgbase_meta.delete_retry_switch_attempt_seq;
		CREATE SEQUENCE pgbase_meta.delete_retry_switch_event_seq;
		CREATE FUNCTION pgbase_meta.fail_first_switched_delete_artifact()
		RETURNS trigger LANGUAGE plpgsql AS $$
		DECLARE attempt bigint;
		BEGIN
			IF NEW.request_id <> 'delete-retry-closure' OR NEW.operation <> 'update' THEN
				RETURN NEW;
			END IF;
			attempt := nextval('pgbase_meta.delete_retry_switch_attempt_seq');
			IF attempt = 1 THEN
				PERFORM setval('pgbase_meta.delete_retry_switch_event_seq',
					(hashtextextended(NEW.event_id::text, 0) & 4611686018427387903) + 1);
				RAISE EXCEPTION 'retry switched delete artifact' USING ERRCODE = '40001';
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER fail_first_switched_delete_artifact
		BEFORE INSERT ON pgbase_meta.record_audit_events
		FOR EACH ROW EXECUTE FUNCTION pgbase_meta.fail_first_switched_delete_artifact()`); err != nil {
		t.Fatal(err)
	}

	var replacement MutationResult
	ensurer := &deleteRetryCatalogEnsurer{inner: schemas}
	ensurer.beforeSecond = func() error {
		if _, err := records.Update(ctx, UpdateRequest{
			Principal: principal, RequestID: "retry-closure-remove", Collection: "null_survivors", ID: staleID,
			IfMatch: EntityTagSet{Present: true, Values: []string{stale.Record.ETag}},
			Input:   map[string]json.RawMessage{"owner": json.RawMessage(`null`)},
		}); err != nil {
			return err
		}
		var err error
		replacement, err = records.Create(ctx, CreateRequest{
			Principal: principal, RequestID: "retry-closure-add", Collection: "null_survivors",
			Input: map[string]json.RawMessage{
				"name": json.RawMessage(`"replacement"`), "owner": relationUUIDJSON(targetID),
			},
		})
		return err
	}
	retrying := NewService(pool, schemas, []byte("0123456789abcdef0123456789abcdef"))
	retrying.schemas = ensurer
	firstValidator := bytes.Repeat([]byte{0x31}, 32)
	secondValidator := bytes.Repeat([]byte{0x32}, 32)
	retrying.random = bytes.NewReader(append(append([]byte(nil), firstValidator...), secondValidator...))

	requestID := "delete-retry-closure"
	if _, err := retrying.Delete(ctx, DeleteRequest{
		Principal: principal, RequestID: requestID, Collection: "null_targets", ID: targetID,
		IfMatch: EntityTagSet{Present: true, Values: []string{target.Record.ETag}},
	}); err != nil {
		t.Fatalf("delete changing retry closure: %v", err)
	}
	if replacement.Record == nil {
		t.Fatal("retry hook did not create the replacement source")
	}
	replacementID := recordResultID(t, replacement)
	staleAfter, err := records.Get(ctx, GetRequest{
		Principal: principal, RequestID: "retry-stale-after", Collection: "null_survivors", ID: staleID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if staleAfter.Data["owner"] != nil || staleAfter.Version != stale.Record.Version+1 {
		t.Fatalf("stale source received a delete-derived mutation: before=%#v after=%#v", stale.Record, staleAfter)
	}
	replacementAfter, err := records.Get(ctx, GetRequest{
		Principal: principal, RequestID: "retry-replacement-after", Collection: "null_survivors", ID: replacementID,
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sourceCollection := catalog.Collections["null_survivors"]
	digest, err := recordartifact.SchemaDigest(sourceCollection)
	if err != nil {
		t.Fatal(err)
	}
	wantReplacementETag := recordartifact.RevisionETag(sourceCollection.ID, replacementID, digest, secondValidator)
	if replacementAfter.Data["owner"] != nil || replacementAfter.ETag != wantReplacementETag {
		t.Fatalf("replacement source identity/value = %#v", replacementAfter)
	}

	artifacts := readDeleteRequestArtifacts(t, ctx, pool, requestID)
	if len(artifacts) != 2 || artifacts[0].recordID != replacementID || artifacts[1].recordID != targetID {
		t.Fatalf("current closure artifacts = %#v", artifacts)
	}
	var attempts, staleEventFingerprint, committedEventFingerprint int64
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT last_value FROM pgbase_meta.delete_retry_switch_attempt_seq),
		(SELECT last_value FROM pgbase_meta.delete_retry_switch_event_seq),
		(hashtextextended($1::text, 0) & 4611686018427387903) + 1`, artifacts[0].eventID).
		Scan(&attempts, &staleEventFingerprint, &committedEventFingerprint); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || staleEventFingerprint == committedEventFingerprint {
		t.Fatalf("retry closure events across %d attempts = stale:%d committed:%d",
			attempts, staleEventFingerprint, committedEventFingerprint)
	}
}

type deleteRetryCatalogEnsurer struct {
	inner        CatalogEnsurer
	calls        int
	beforeSecond func() error
}

func (e *deleteRetryCatalogEnsurer) EnsureCurrent(ctx context.Context, tx database.DBTX) (schema.Catalog, error) {
	catalog, err := e.inner.EnsureCurrent(ctx, tx)
	if err != nil {
		return schema.Catalog{}, err
	}
	e.calls++
	if e.calls == 2 && e.beforeSecond != nil {
		if err := e.beforeSecond(); err != nil {
			return schema.Catalog{}, err
		}
	}
	return catalog, nil
}

type deleteRequestArtifact struct {
	eventID         uuid.UUID
	recordID        uuid.UUID
	actorID         uuid.UUID
	requestID       string
	operation       string
	changedFields   []string
	previousVersion int64
	newVersion      int64
	previousETag    string
	newETag         string
	compatibility   string
}

func readDeleteRequestArtifacts(t *testing.T, ctx context.Context, pool *database.Pool, requestID string) []deleteRequestArtifact {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT event_id, record_id, actor_id, request_id, operation,
		changed_fields, previous_version, new_version,
		coalesce(previous_etag, ''), coalesce(new_etag, ''), compatibility
		FROM pgbase_meta.record_audit_events WHERE request_id=$1 ORDER BY sequence`, requestID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	artifacts := make([]deleteRequestArtifact, 0)
	for rows.Next() {
		var artifact deleteRequestArtifact
		if err := rows.Scan(
			&artifact.eventID, &artifact.recordID, &artifact.actorID, &artifact.requestID,
			&artifact.operation, &artifact.changedFields, &artifact.previousVersion, &artifact.newVersion,
			&artifact.previousETag, &artifact.newETag, &artifact.compatibility,
		); err != nil {
			t.Fatal(err)
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return artifacts
}

func readDeleteRequestOutboxes(t *testing.T, ctx context.Context, pool *database.Pool, requestID string) []outboxEnvelope {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT o.envelope
		FROM pgbase_meta.record_outbox o
		JOIN pgbase_meta.record_audit_events a USING (event_id)
		WHERE a.request_id=$1 ORDER BY a.sequence`, requestID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := make([]outboxEnvelope, 0)
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			t.Fatal(err)
		}
		var envelope outboxEnvelope
		if err := json.Unmarshal(encoded, &envelope); err != nil {
			t.Fatal(err)
		}
		result = append(result, envelope)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func applyDeleteSetNullSchema(t *testing.T, ctx context.Context, schemas *schema.Service, administratorID uuid.UUID) {
	t.Helper()
	applyDeleteCollection(t, ctx, schemas, administratorID, schema.CreateCollectionInput{
		Name: "null_targets", Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText, Required: true}},
	})
	applyDeleteCollection(t, ctx, schemas, administratorID, schema.CreateCollectionInput{
		Name: "null_survivors", Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText, Required: true},
			deleteRelationField("owner", "null_targets", schema.RelationSingle, schema.RelationSetNull),
			deleteRelationField("links", "null_targets", schema.RelationMultiple, schema.RelationSetNull),
		},
	})
}

func applyDeleteCascadeGraphSchema(t *testing.T, ctx context.Context, schemas *schema.Service, administratorID uuid.UUID) {
	t.Helper()
	rootFields := []schema.FieldInput{
		{Name: "name", Type: schema.TypeText, Required: true},
		deleteRelationField("self", "graph_roots", schema.RelationSingle, schema.RelationCascade),
	}
	applyDeleteCollection(t, ctx, schemas, administratorID, schema.CreateCollectionInput{Name: "graph_roots", Fields: rootFields})
	applyDeleteCollection(t, ctx, schemas, administratorID, schema.CreateCollectionInput{Name: "graph_left", Fields: []schema.FieldInput{
		{Name: "name", Type: schema.TypeText, Required: true},
		deleteRelationField("root", "graph_roots", schema.RelationSingle, schema.RelationCascade),
	}})
	applyDeleteCollection(t, ctx, schemas, administratorID, schema.CreateCollectionInput{Name: "graph_right", Fields: []schema.FieldInput{
		{Name: "name", Type: schema.TypeText, Required: true},
		deleteRelationField("root", "graph_roots", schema.RelationSingle, schema.RelationCascade),
	}})
	applyDeleteCollection(t, ctx, schemas, administratorID, schema.CreateCollectionInput{Name: "graph_diamond", Fields: []schema.FieldInput{
		{Name: "name", Type: schema.TypeText, Required: true},
		deleteRelationField("left", "graph_left", schema.RelationSingle, schema.RelationCascade),
		deleteRelationField("right", "graph_right", schema.RelationSingle, schema.RelationCascade),
		deleteRelationField("root_guard", "graph_roots", schema.RelationSingle, schema.RelationRestrict),
	}})
	rootFields = append(rootFields, deleteRelationField("cycle", "graph_diamond", schema.RelationSingle, schema.RelationCascade))
	input := schema.UpdateCollectionInput{Name: "graph_roots", Fields: rootFields}
	plan, err := schemas.PlanUpdate(ctx, input)
	if err != nil {
		t.Fatalf("plan graph cycle field: %v", err)
	}
	if _, err := schemas.ApplyUpdate(ctx, input, plan.Checksum, plan.BaseVersion, administratorID); err != nil {
		t.Fatalf("apply graph cycle field: %v", err)
	}
}

func applyDeleteCollection(t *testing.T, ctx context.Context, schemas *schema.Service, administratorID uuid.UUID, input schema.CreateCollectionInput) schema.Collection {
	t.Helper()
	plan, err := schemas.PlanCreate(ctx, input)
	if err != nil {
		t.Fatalf("plan collection %s: %v", input.Name, err)
	}
	collection, err := schemas.ApplyCreate(ctx, input, plan.Checksum, plan.BaseVersion, administratorID)
	if err != nil {
		t.Fatalf("apply collection %s: %v", input.Name, err)
	}
	return collection
}

func deleteRelationField(name, target string, cardinality schema.RelationCardinality, action schema.RelationDeleteAction) schema.FieldInput {
	input := schema.FieldInput{Name: name, Type: schema.TypeRelation, Relation: &schema.RelationInput{
		TargetCollection: target, Cardinality: cardinality, OnDelete: action,
	}}
	if cardinality == schema.RelationMultiple {
		maximum := 10
		input.Relation.MaxSelect = &maximum
	}
	return input
}

func createDeleteRecord(t *testing.T, ctx context.Context, records *Service, principal Principal, collection, suffix string, input map[string]json.RawMessage) MutationResult {
	t.Helper()
	result, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "create-" + suffix, Collection: collection, Input: input,
	})
	if err != nil {
		t.Fatalf("create %s/%s: %v", collection, suffix, err)
	}
	return result
}

func recordResultID(t *testing.T, result MutationResult) uuid.UUID {
	t.Helper()
	if result.Record == nil {
		t.Fatal("mutation result has no record")
	}
	value, ok := result.Record.Data["id"].(string)
	if !ok {
		t.Fatalf("record id = %#v", result.Record.Data["id"])
	}
	return uuid.MustParse(value)
}

func assertDeleteGraphValidation(t *testing.T, err error, code string) {
	t.Helper()
	var validation *query.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("delete error = %v, want validation", err)
	}
	want := []query.ValidationDetail{{Field: "id", Code: code}}
	if !reflect.DeepEqual(validation.Details, want) {
		t.Fatalf("delete validation = %#v, want %#v", validation.Details, want)
	}
}

func assertNoDeleteRequestArtifacts(t *testing.T, ctx context.Context, pool *database.Pool, requestID string) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pgbase_meta.record_audit_events WHERE request_id=$1`, requestID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("request %q wrote %d audit events", requestID, count)
	}
}

func quoteDeleteIdentifier(value string) string {
	return `"` + value + `"`
}

func deleteTestCollectionLockKey(id uuid.UUID) int64 {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("pgbase:relation-collection-lock:v1"))
	_, _ = hasher.Write(id[:])
	return int64(binary.BigEndian.Uint64(hasher.Sum(nil)[:8]))
}

type deleteWriterPreparation struct {
	backendPID int32
	err        error
}

func waitForDeleteAdvisoryWaiter(ctx context.Context, pool *database.Pool, writerPID int32, lockKey int64) error {
	unsigned := uint64(lockKey)
	classID := int64(uint32(unsigned >> 32))
	objectID := int64(uint32(unsigned))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1
			FROM pg_locks AS waiter
			JOIN pg_locks AS holder
			  ON holder.locktype=waiter.locktype AND holder.database=waiter.database
			 AND holder.classid=waiter.classid AND holder.objid=waiter.objid
			 AND holder.objsubid=waiter.objsubid AND holder.mode=waiter.mode
			WHERE waiter.locktype='advisory' AND NOT waiter.granted AND holder.granted
			  AND holder.pid=$1 AND waiter.objsubid=1
			  AND waiter.database=(SELECT oid FROM pg_database WHERE datname=current_database())
			  AND waiter.classid::bigint=$2 AND waiter.objid::bigint=$3
		)`, writerPID, classID, objectID).Scan(&waiting); err != nil {
			return err
		}
		if waiting {
			return nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		}
	}
	return errors.New("delete did not wait on the relation advisory lock")
}
