//go:build integration

package recordops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestTruncateCountsMultipleRelationsInRelationSchema(t *testing.T) {
	_, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	applyDeleteCollection(t, ctx, schemas, principal.ID, schema.CreateCollectionInput{Name: "truncate_targets", Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText, Required: true}}})
	maximum := 3
	applyDeleteCollection(t, ctx, schemas, principal.ID, schema.CreateCollectionInput{Name: "truncate_sources", Fields: []schema.FieldInput{
		{Name: "name", Type: schema.TypeText, Required: true},
		{Name: "links", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "truncate_targets", Cardinality: schema.RelationMultiple, MaxSelect: &maximum, OnDelete: schema.RelationSetNull}},
	}})
	target := createDeleteRecord(t, ctx, records, principal, "truncate_targets", "truncate-target", map[string]json.RawMessage{"name": json.RawMessage(`"target"`)})
	createDeleteRecord(t, ctx, records, principal, "truncate_sources", "truncate-source", map[string]json.RawMessage{"name": json.RawMessage(`"source"`), "links": relationUUIDArrayJSON(recordResultID(t, target))})
	result, err := records.Truncate(ctx, TruncateRequest{Principal: principal, RequestID: "truncate-multiple-relation", Collection: "truncate_sources"})
	if err != nil {
		t.Fatalf("truncate multiple relation: %v", err)
	}
	if !result.Truncated || result.RecordsDeleted != 1 || result.RelationsDeleted != 1 {
		t.Fatalf("truncate result = %#v", result)
	}
}

func TestTruncateUsesRecordAndDurableFileDeletionLifecycle(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	created := []uuid.UUID{}
	for i, title := range []string{"truncate-one", "truncate-two"} {
		result, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "truncate-create-request", Collection: "records", IdempotencyKey: "truncate-create-record-000" + string(rune('1'+i)), Input: map[string]json.RawMessage{"title": json.RawMessage(`"` + title + `"`)}})
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, uuid.MustParse(result.Record.Data["id"].(string)))
	}
	fileID, objectID := uuid.New(), uuid.New()
	objectKey := "objects/" + objectID.String()
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.record_files(id,collection_id,record_id,field_name,filename,content_type,size_bytes,sha256,object_key,status,created_at,ready_at) select $1,id,$2,'attachment','file.txt','text/plain',4,$3,$4,'ready',now(),now() from pgbase_meta.collections where name='records'`, fileID, created[0], bytes.Repeat([]byte{1}, 32), objectKey); err != nil {
		t.Fatal(err)
	}

	result, err := records.Truncate(ctx, TruncateRequest{Principal: principal, RequestID: "truncate-records-request", Collection: "records"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || result.RecordsDeleted != 2 || result.FilesDeleted != 1 {
		t.Fatalf("truncate result = %#v", result)
	}
	var rows, files, queued, deletes int
	if err := pool.QueryRow(ctx, `select (select count(*) from app.records),(select count(*) from pgbase_meta.record_files),(select count(*) from pgbase_meta.file_object_deletions where object_key=$1),(select count(*) from pgbase_meta.record_audit_events where collection_name='records' and operation='delete')`, objectKey).Scan(&rows, &files, &queued, &deletes); err != nil {
		t.Fatal(err)
	}
	if rows != 0 || files != 0 || queued != 1 || deletes != 2 {
		t.Fatalf("rows=%d files=%d queued=%d deletes=%d", rows, files, queued, deletes)
	}
}

func TestTruncateHookVetoRollsBackEveryRecord(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	for i, title := range []string{"veto-one", "veto-two"} {
		_, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "truncate-veto-create", Collection: "records", IdempotencyKey: "truncate-veto-record-000" + string(rune('1'+i)), Input: map[string]json.RawMessage{"title": json.RawMessage(`"` + title + `"`)}})
		if err != nil {
			t.Fatal(err)
		}
	}
	records.SetHooks([]MutationHook{&countingHook{fail: errors.New("veto truncate")}})
	if _, err := records.Truncate(ctx, TruncateRequest{Principal: principal, RequestID: "truncate-veto-request", Collection: "records"}); err == nil {
		t.Fatal("truncate succeeded despite hook veto")
	}
	var rows, deletes int
	if err := pool.QueryRow(ctx, `select (select count(*) from app.records),(select count(*) from pgbase_meta.record_audit_events where collection_name='records' and operation='delete')`).Scan(&rows, &deletes); err != nil {
		t.Fatal(err)
	}
	if rows != 2 || deletes != 0 {
		t.Fatalf("rows=%d deletes=%d", rows, deletes)
	}
}
