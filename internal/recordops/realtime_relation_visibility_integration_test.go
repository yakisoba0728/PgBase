//go:build integration

package recordops

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/realtime"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestRealtimeRelationsUseCurrentVisibilityAndOriginalTargetIdentity(t *testing.T) {
	f := newUserAccessFixture(t)
	ctx := recordOperationsTestContext(t)
	apply := func(input schema.CreateCollectionInput) {
		t.Helper()
		plan, err := f.schemas.Preview(ctx, schema.NewCreateChange(input))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(input), plan, f.admin.ID)); err != nil {
			t.Fatal(err)
		}
	}
	policy := &schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 32, Pattern: `^[A-Za-z0-9]+$`}
	apply(schema.CreateCollectionInput{Name: "event_targets", RecordID: policy, Fields: []schema.FieldInput{{Name: "visible", Type: schema.TypeBoolean}}})
	maximum := 4
	apply(schema.CreateCollectionInput{Name: "event_sources", Fields: []schema.FieldInput{
		{Name: "title", Type: schema.TypeText},
		{Name: "target", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "event_targets", Cardinality: schema.RelationSingle, OnDelete: schema.RelationSetNull}},
		{Name: "targets", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "event_targets", Cardinality: schema.RelationMultiple, MaxSelect: &maximum, OnDelete: schema.RelationSetNull}},
	}})
	f.enable(t, "event_targets")
	f.enable(t, "event_sources")
	allow := accessrule.Rule{Op: "allow"}
	targetView := accessrule.Rule{Op: "eq", Field: "visible", Value: json.RawMessage(`true`)}
	rules := recordaccess.NewRuleService(f.pool, f.schemas)
	if _, err := rules.Set(ctx, "event_targets", accessrule.Rules{List: allow, View: targetView, Create: allow, Update: allow, Delete: allow}); err != nil {
		t.Fatal(err)
	}
	if _, err := rules.Set(ctx, "event_sources", accessrule.Rules{List: allow, View: allow, Create: allow, Update: allow, Delete: allow}); err != nil {
		t.Fatal(err)
	}
	create := func(collection, body string) MutationResult {
		t.Helper()
		var input map[string]json.RawMessage
		if err := json.Unmarshal([]byte(body), &input); err != nil {
			t.Fatal(err)
		}
		result, err := f.records.Create(ctx, CreateRequest{Principal: f.admin, RequestID: uuid.NewString(), Collection: collection, Input: input})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	create("event_targets", `{"id":"VisibleTarget","visible":true}`)
	create("event_targets", `{"id":"PrivateTarget","visible":false}`)
	source := create("event_sources", `{"title":"original","target":"PrivateTarget","targets":["PrivateTarget","VisibleTarget"]}`)
	if _, err := f.records.Update(ctx, UpdateRequest{Principal: f.admin, RequestID: uuid.NewString(), Collection: "event_sources", ID: source.InternalID, Input: map[string]json.RawMessage{"title": json.RawMessage(`"current"`)}}); err != nil {
		t.Fatal(err)
	}
	stream := realtime.NewService(f.pool, nil, f.schemas)
	read := func(principal identity.Subject) realtime.Event {
		t.Helper()
		if err := stream.Deliver(ctx); err != nil {
			t.Fatal(err)
		}
		zero := int64(0)
		events, _, err := stream.ReadFor(ctx, realtime.ReadRequest{Principal: principal, Collection: "event_sources", After: &zero})
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.Operation == "create" && event.RecordID == source.InternalID {
				return event
			}
		}
		t.Fatal("source create event missing")
		return realtime.Event{}
	}
	for _, principal := range []identity.Subject{{Kind: identity.Anonymous}, f.first} {
		event := read(principal)
		if event.Record["title"] != "original" || event.Record["target"] != nil || fmt.Sprint(event.Record["targets"]) != "[VisibleTarget]" {
			t.Fatalf("current target policy or historical source value lost: %#v", event.Record)
		}
	}
	adminEvent := read(f.admin)
	if adminEvent.Record["target"] != "PrivateTarget" || fmt.Sprint(adminEvent.Record["targets"]) != "[PrivateTarget VisibleTarget]" {
		t.Fatalf("administrator projection = %#v", adminEvent.Record)
	}
	if _, err := f.records.Delete(ctx, DeleteRequest{Principal: f.admin, RequestID: uuid.NewString(), Collection: "event_targets", PublicID: "PrivateTarget"}); err != nil {
		t.Fatal(err)
	}
	create("event_targets", `{"id":"PrivateTarget","visible":true}`)
	event := read(f.first)
	if event.Record["target"] != nil || fmt.Sprint(event.Record["targets"]) != "[VisibleTarget]" {
		t.Fatalf("reused public ID changed the original target identity: %#v", event.Record)
	}
	if _, err := recordaccess.NewService(f.pool).Set(ctx, "event_targets", false); err != nil {
		t.Fatal(err)
	}
	event = read(f.first)
	if event.Record["target"] != nil || fmt.Sprint(event.Record["targets"]) != "[]" {
		t.Fatalf("disabled target collection retained relation values: %#v", event.Record)
	}
}
