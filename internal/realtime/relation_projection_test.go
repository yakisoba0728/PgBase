package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordartifact"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestLegacyEventRelationsFailClosedWithoutIdentityLookup(t *testing.T) {
	target := uuid.New()
	fields := []schema.Field{
		{ID: uuid.New(), Name: "target", Type: schema.TypeRelation, Relation: &schema.RelationSpec{TargetCollectionID: target, Cardinality: schema.RelationSingle}},
		{ID: uuid.New(), Name: "targets", Type: schema.TypeRelation, Relation: &schema.RelationSpec{TargetCollectionID: target, Cardinality: schema.RelationMultiple}},
	}
	collection := schema.Collection{Fields: fields}
	for _, replacement := range []bool{false, true} {
		t.Run(fmt.Sprint(replacement), func(t *testing.T) {
			var snapshot []byte
			if replacement {
				// A newly created field with a reused name cannot inherit an old
				// field's identities, even when the target collection is the same.
				snapshot, _ = json.Marshal(map[uuid.UUID]recordartifact.RelationSnapshot{uuid.New(): {TargetCollectionID: target, Cardinality: schema.RelationSingle, IDs: []uuid.UUID{uuid.New()}}})
			}
			events := []Event{{Record: map[string]any{"title": "original", "target": "HistoricalID", "targets": []any{"HistoricalID"}}}}
			if err := filterEventRelations(context.Background(), nil, schema.Catalog{}, collection, ReadRequest{Principal: identity.Subject{Kind: identity.Anonymous}}, events, [][]byte{snapshot}); err != nil {
				t.Fatal(err)
			}
			if events[0].Record["title"] != "original" || events[0].Record["target"] != nil || fmt.Sprint(events[0].Record["targets"]) != "[]" {
				t.Fatalf("legacy projection = %#v", events[0].Record)
			}
		})
	}
}

func TestEventRelationSnapshotRejectsMisalignedValues(t *testing.T) {
	field := schema.Field{Relation: &schema.RelationSpec{Cardinality: schema.RelationMultiple}}
	if _, err := eventRelationValues(field, []any{"PublicID"}, nil); err == nil {
		t.Fatal("accepted a public value without its original internal identity")
	}
	if _, err := eventRelationValues(field, []any{"PublicID"}, []uuid.UUID{uuid.Nil}); err == nil {
		t.Fatal("accepted a missing internal identity")
	}
}
