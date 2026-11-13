//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestClosureTextBatchDeleteAndRelationModifiers(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	definition := schema.CreateCollectionInput{Name: "closure_text", RecordID: &schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 64, Pattern: `^[A-Za-z0-9-]+$`}, Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}}}
	plan, err := schemas.Preview(ctx, schema.NewCreateChange(definition))
	if err != nil {
		t.Fatal(err)
	}
	_, err = schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(definition), plan, principal.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, update := range []bool{false, true} {
		name := "created"
		if update {
			name = "updated"
			if _, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "closure-seed", IdempotencyKey: "closure-deleted-replay-key", Collection: definition.Name, Input: map[string]json.RawMessage{"id": json.RawMessage(`"updated"`)}}); err != nil {
				t.Fatal(err)
			}
		}
		op := BatchOperation{Create: &CreateRequest{Collection: definition.Name, Input: map[string]json.RawMessage{"id": json.RawMessage(`"created"`)}}}
		if update {
			op = BatchOperation{Update: &UpdateRequest{Collection: definition.Name, PublicID: name, Input: map[string]json.RawMessage{"title": json.RawMessage(`"after"`)}}}
		}
		results, err := records.Batch(ctx, BatchRequest{Principal: principal, RequestID: "closure-batch", Operations: []BatchOperation{op, {Delete: &DeleteRequest{Collection: definition.Name, PublicID: name}}}})
		if err != nil {
			t.Fatal(err)
		}
		if results[0].Record.Data["id"] != name {
			t.Fatalf("public id=%v", results[0].Record.Data["id"])
		}
		if update {
			replayed, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "closure-replay", IdempotencyKey: "closure-deleted-replay-key", Collection: definition.Name, Input: map[string]json.RawMessage{"id": json.RawMessage(`"updated"`)}})
			if err != nil || !replayed.Replayed || replayed.Record.Data["id"] != name {
				t.Fatalf("replay=%+v err=%v", replayed, err)
			}
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `select count(*) from app.closure_text`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("remaining=%d err=%v", count, err)
	}
	for _, id := range []string{"TargetOne", "TargetTwo"} {
		raw, _ := json.Marshal(id)
		if _, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: id, Collection: definition.Name, Input: map[string]json.RawMessage{"id": raw}}); err != nil {
			t.Fatal(err)
		}
	}
	maximum := 5
	links := schema.CreateCollectionInput{Name: "closure_links", Fields: []schema.FieldInput{{Name: "links", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: definition.Name, Cardinality: schema.RelationMultiple, MaxSelect: &maximum}}}}
	plan, err = schemas.Preview(ctx, schema.NewCreateChange(links))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(links), plan, principal.ID)); err != nil {
		t.Fatal(err)
	}
	source, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "closure-links", Collection: links.Name, Input: map[string]json.RawMessage{"links": json.RawMessage(`["TargetOne"]`)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		key, raw string
		want     int
	}{{"links+", `"TargetTwo"`, 2}, {"links-", `"TargetOne"`, 1}} {
		result, err := records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "closure-modify", Collection: links.Name, ID: source.InternalID, Input: map[string]json.RawMessage{step.key: json.RawMessage(step.raw)}})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(result.Record.Data["links"])
		var ids []string
		if json.Unmarshal(encoded, &ids) != nil || len(ids) != step.want {
			t.Fatalf("links=%s", encoded)
		}
	}
}

func TestClosureCompoundReusesMembershipReadiness(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	if _, err := records.members.writer(ctx); err != nil {
		t.Fatal(err)
	}
	held := int(pool.Stats().MaxConns) - 1
	for i := 0; i < held; i++ {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := records.Batch(bounded, BatchRequest{Principal: principal, RequestID: "pool-batch", Operations: []BatchOperation{{Create: &CreateRequest{Collection: "records", Input: batchInput("one")}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := records.Truncate(bounded, TruncateRequest{Principal: principal, RequestID: "pool-truncate", Collection: "records"}); err != nil {
		t.Fatal(err)
	}
}
