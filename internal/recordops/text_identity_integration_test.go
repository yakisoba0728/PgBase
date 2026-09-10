//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestTextRecordIdentityCRUDReplayAndExactCase(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	definition := schema.CreateCollectionInput{
		Name:     "text_identity_records",
		RecordID: &schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 32, Pattern: `^[A-Za-z0-9]+$`},
		Fields:   []schema.FieldInput{{Name: "title", Type: schema.TypeText, Required: true}},
	}
	plan, err := schemas.Preview(ctx, schema.NewCreateChange(definition))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(definition), plan, principal.ID)); err != nil {
		t.Fatal(err)
	}

	created, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "text-id-create", Collection: definition.Name, IdempotencyKey: "text-id-create-key-0001", Input: map[string]json.RawMessage{"id": json.RawMessage(`"CaseSensitive01"`), "title": json.RawMessage(`"first"`)}})
	if err != nil || created.Record == nil || created.Record.Data["id"] != "CaseSensitive01" || created.InternalID == uuid.Nil {
		t.Fatalf("create=%#v err=%v", created, err)
	}
	replayed, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "text-id-replay", Collection: definition.Name, IdempotencyKey: "text-id-create-key-0001", Input: map[string]json.RawMessage{"id": json.RawMessage(`"CaseSensitive01"`), "title": json.RawMessage(`"first"`)}})
	if err != nil || !replayed.Replayed || replayed.Record.Data["id"] != "CaseSensitive01" || replayed.InternalID != created.InternalID {
		t.Fatalf("replay=%#v err=%v", replayed, err)
	}

	got, err := records.Get(ctx, GetRequest{Principal: principal, RequestID: "text-id-get", Collection: definition.Name, PublicID: "CaseSensitive01"})
	if err != nil || got.Data["id"] != "CaseSensitive01" {
		t.Fatalf("get=%#v err=%v", got, err)
	}
	updated, err := records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "text-id-update", Collection: definition.Name, PublicID: "CaseSensitive01", Input: map[string]json.RawMessage{"title": json.RawMessage(`"updated"`)}})
	if err != nil || updated.Record.Data["id"] != "CaseSensitive01" || updated.Record.Data["title"] != "updated" {
		t.Fatalf("update=%#v err=%v", updated, err)
	}

	_, err = records.Create(ctx, CreateRequest{Principal: principal, RequestID: "text-id-collision", Collection: definition.Name, Input: map[string]json.RawMessage{"id": json.RawMessage(`"CaseSensitive01"`), "title": json.RawMessage(`"collision"`)}})
	var operationError *Error
	if !errors.As(err, &operationError) || operationError.Code != CodeRecordIDConflict {
		t.Fatalf("collision error=%v", err)
	}
	caseDistinct, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "text-id-case", Collection: definition.Name, Input: map[string]json.RawMessage{"id": json.RawMessage(`"casesensitive01"`), "title": json.RawMessage(`"distinct"`)}})
	if err != nil || caseDistinct.Record.Data["id"] != "casesensitive01" {
		t.Fatalf("case-distinct create=%#v err=%v", caseDistinct, err)
	}

	if _, err := records.Get(ctx, GetRequest{Principal: principal, RequestID: "text-id-no-fallback", Collection: definition.Name, ID: created.InternalID}); !errors.As(err, &operationError) || operationError.Code != CodeInvalidRecordID {
		t.Fatalf("internal UUID fallback error=%v", err)
	}
	if _, err := records.Delete(ctx, DeleteRequest{Principal: principal, RequestID: "text-id-delete", Collection: definition.Name, PublicID: "CaseSensitive01"}); err != nil {
		t.Fatal(err)
	}
	if _, err := records.Get(ctx, GetRequest{Principal: principal, RequestID: "text-id-gone", Collection: definition.Name, PublicID: "CaseSensitive01"}); !errors.Is(err, query.ErrRecordNotFound) {
		t.Fatalf("deleted get error=%v", err)
	}
	var aliases int
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.record_identities where collection_id=(select id from pgbase_meta.collections where name=$1) and external_id=$2`, definition.Name, "CaseSensitive01").Scan(&aliases); err != nil || aliases != 0 {
		t.Fatalf("deleted aliases=%d err=%v", aliases, err)
	}
}

func TestTextRecordIdentityRelationsAndOrderedBatchResolution(t *testing.T) {
	_, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	apply := func(definition schema.CreateCollectionInput) {
		t.Helper()
		plan, err := schemas.Preview(ctx, schema.NewCreateChange(definition))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(definition), plan, principal.ID)); err != nil {
			t.Fatal(err)
		}
	}
	policy := &schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 32, Pattern: `^[A-Za-z0-9]+$`}
	maximumTargets := 10
	apply(schema.CreateCollectionInput{Name: "text_identity_targets", RecordID: policy, Fields: []schema.FieldInput{{Name: "label", Type: schema.TypeText, Required: true}}})
	apply(schema.CreateCollectionInput{Name: "text_identity_sources", RecordID: policy, Fields: []schema.FieldInput{
		{Name: "label", Type: schema.TypeText, Required: true},
		{Name: "primary_target", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "text_identity_targets", Cardinality: schema.RelationSingle}},
		{Name: "other_targets", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "text_identity_targets", Cardinality: schema.RelationMultiple, MaxSelect: &maximumTargets}},
	}})

	results, err := records.Batch(ctx, BatchRequest{Principal: principal, RequestID: "text-id-relation-batch", Operations: []BatchOperation{
		{Create: &CreateRequest{Collection: "text_identity_targets", Input: map[string]json.RawMessage{"id": json.RawMessage(`"TargetOne"`), "label": json.RawMessage(`"one"`)}}},
		{Create: &CreateRequest{Collection: "text_identity_sources", Input: map[string]json.RawMessage{"id": json.RawMessage(`"SourceOne"`), "label": json.RawMessage(`"source"`), "primary_target": json.RawMessage(`"TargetOne"`), "other_targets": json.RawMessage(`["TargetOne"]`)}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := results[1].Record.Data; got["id"] != "SourceOne" || got["primary_target"] != "TargetOne" {
		t.Fatalf("batch relation projection = %#v", got)
	}
	values, ok := results[1].Record.Data["other_targets"].([]string)
	if !ok || len(values) != 1 || values[0] != "TargetOne" {
		t.Fatalf("batch multiple relation projection = %#v", results[1].Record.Data["other_targets"])
	}

	got, err := records.Get(ctx, GetRequest{Principal: principal, RequestID: "text-id-relation-get", Collection: "text_identity_sources", PublicID: "SourceOne"})
	if err != nil || got.Data["primary_target"] != "TargetOne" {
		t.Fatalf("get relation = %#v, %v", got, err)
	}
	updated, err := records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "text-id-relation-update", Collection: "text_identity_sources", PublicID: "SourceOne", Input: map[string]json.RawMessage{"primary_target": json.RawMessage(`"TargetOne"`)}})
	if err != nil || updated.Record.Data["primary_target"] != "TargetOne" {
		t.Fatalf("update relation = %#v, %v", updated, err)
	}
}
