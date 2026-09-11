//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/schema"

	"github.com/google/uuid"
)

func TestConcurrentNumericModifiersDoNotLoseIncrements(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	created, err := records.Create(context.Background(), CreateRequest{
		Principal: principal, RequestID: "modifier_create", Collection: "records",
		Input: map[string]json.RawMessage{"title": json.RawMessage(`"counter"`), "count": json.RawMessage(`0`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	id, err := uuid.Parse(created.Record.Data["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	const writers = 12
	errors := make(chan error, writers)
	var wait sync.WaitGroup
	for index := range writers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, updateErr := records.Update(context.Background(), UpdateRequest{
				Principal: principal, RequestID: fmt.Sprintf("modifier_%02d", index), Collection: "records", ID: id,
				Input: map[string]json.RawMessage{"count+": json.RawMessage(`1`)},
			})
			errors <- updateErr
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := pool.QueryRow(context.Background(), `select count from app.records where id=$1`, id).Scan(&count); err != nil || count != writers {
		t.Fatalf("count=%d err=%v, want %d", count, err, writers)
	}
}

func TestSelectRelationAndMixedDirectModifiersUseStoredPreimage(t *testing.T) {
	_, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	applyModifierCollection := func(input schema.CreateCollectionInput) {
		t.Helper()
		change := schema.NewCreateChange(input)
		plan, err := schemas.Preview(ctx, change)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := schemas.Apply(ctx, schema.LegacyApply(change, plan, principal.ID)); err != nil {
			t.Fatal(err)
		}
	}
	applyModifierCollection(schema.CreateCollectionInput{Name: "modifier_tags", Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText, Required: true}}})
	maximum := 4
	maximumCount := "60"
	applyModifierCollection(schema.CreateCollectionInput{Name: "modifier_items", Fields: []schema.FieldInput{
		{Name: "title", Type: schema.TypeText, Required: true},
		{Name: "count", Type: schema.TypeInteger, Options: &schema.FieldOptions{Max: &maximumCount}},
		{Name: "roles", Type: schema.TypeJSONB, Options: &schema.FieldOptions{AllowedValues: []string{"a", "b", "c", "d"}, MaxSelect: &maximum}},
		{Name: "tags", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "modifier_tags", Cardinality: schema.RelationMultiple, MaxSelect: &maximum}},
	}})

	createTag := func(name, requestID string) uuid.UUID {
		result, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: requestID, Collection: "modifier_tags", Input: map[string]json.RawMessage{"name": json.RawMessage(fmt.Sprintf(`%q`, name))}})
		if err != nil {
			t.Fatal(err)
		}
		return uuid.MustParse(result.Record.Data["id"].(string))
	}
	first, second, third := createTag("first", "modifier_tag_1"), createTag("second", "modifier_tag_2"), createTag("third", "modifier_tag_3")
	created, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "modifier_item_create", Collection: "modifier_items",
		Input: map[string]json.RawMessage{
			"title": json.RawMessage(`"item"`), "count": json.RawMessage(`10`),
			"roles": json.RawMessage(`["b","d"]`),
			"tags":  json.RawMessage(fmt.Sprintf(`[%q,%q]`, first, second)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.MustParse(created.Record.Data["id"].(string))
	updated, err := records.Update(ctx, UpdateRequest{
		Principal: principal, RequestID: "modifier_item_update", Collection: "modifier_items", ID: id,
		Input: map[string]json.RawMessage{
			"count": json.RawMessage(`100`), "count-": json.RawMessage(`50`),
			"+roles": json.RawMessage(`["a","b"]`), "roles+": json.RawMessage(`["c","a"]`), "roles-": json.RawMessage(`"b"`),
			"+tags": json.RawMessage(fmt.Sprintf(`[%q]`, third)), "tags-": json.RawMessage(fmt.Sprintf(`%q`, second)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(updated.Record.Data["count"]) != "50" {
		t.Fatalf("count=%#v", updated.Record.Data["count"])
	}
	var roles []string
	rolesRaw, ok := updated.Record.Data["roles"].(json.RawMessage)
	if !ok || json.Unmarshal(rolesRaw, &roles) != nil || fmt.Sprint(roles) != "[a d c]" {
		t.Fatalf("roles=%#v", updated.Record.Data["roles"])
	}
	if tags, ok := updated.Record.Data["tags"].([]string); !ok || fmt.Sprint(tags) != fmt.Sprintf("[%s %s]", third, first) {
		t.Fatalf("tags=%#v", updated.Record.Data["tags"])
	}

	if _, err := records.Update(ctx, UpdateRequest{
		Principal: principal, RequestID: "modifier_item_failed", Collection: "modifier_items", ID: id,
		Input: map[string]json.RawMessage{"count+": json.RawMessage(`20`), "tags+": json.RawMessage(`["not-a-uuid"]`)},
	}); err == nil {
		t.Fatal("invalid relation modifier succeeded")
	}
	got, err := records.Get(ctx, GetRequest{Principal: principal, RequestID: "modifier_item_get", Collection: "modifier_items", ID: id})
	if err != nil || fmt.Sprint(got.Data["count"]) != "50" {
		t.Fatalf("failed modifier changed record: %#v err=%v", got.Data, err)
	}
}
