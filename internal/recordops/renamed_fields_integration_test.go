//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestDeleteGraphAcceptsRenamedScalarFieldsInOwnAndUnrelatedCollections(t *testing.T) {
	_, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	unrelated := applyDeleteCollection(t, ctx, schemas, principal.ID, schema.CreateCollectionInput{Name: "renamed_unrelated", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}}})
	renameOperationFields(t, schemas, principal.ID, unrelated, []schema.FieldInput{{ID: &unrelated.Fields[0].ID, Name: "headline", Type: schema.TypeText}})
	first := createDeleteRecord(t, ctx, records, principal, "records", "unrelated-rename", map[string]json.RawMessage{"title": json.RawMessage(`"first"`)})
	if _, err := records.Delete(ctx, DeleteRequest{Principal: principal, RequestID: "delete-unrelated-renamed", Collection: "records", ID: recordResultID(t, first), IfMatch: EntityTagSet{Present: true, Values: []string{first.Record.ETag}}}); err != nil {
		t.Fatalf("delete with unrelated renamed scalar: %v", err)
	}
	own := applyDeleteCollection(t, ctx, schemas, principal.ID, schema.CreateCollectionInput{Name: "renamed_own", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}}})
	second := createDeleteRecord(t, ctx, records, principal, own.Name, "own-rename", map[string]json.RawMessage{"title": json.RawMessage(`"second"`)})
	renameOperationFields(t, schemas, principal.ID, own, []schema.FieldInput{{ID: &own.Fields[0].ID, Name: "headline", Type: schema.TypeText}})
	id := recordResultID(t, second)
	current, err := records.Get(ctx, GetRequest{Principal: principal, RequestID: "get-own-renamed", Collection: own.Name, ID: id})
	if err != nil {
		t.Fatal(err)
	}
	if current.Data["headline"] != "second" {
		t.Fatalf("renamed scalar value: %#v", current.Data)
	}
	if _, err := records.Delete(ctx, DeleteRequest{Principal: principal, RequestID: "delete-own-renamed", Collection: own.Name, ID: id, IfMatch: EntityTagSet{Present: true, Values: []string{current.ETag}}}); err != nil {
		t.Fatalf("delete own renamed scalar: %v", err)
	}
}

func TestRecordOperationsUsePhysicalStorageAfterRelationFieldRename(t *testing.T) {
	for _, cardinality := range []schema.RelationCardinality{schema.RelationSingle, schema.RelationMultiple} {
		t.Run(string(cardinality), func(t *testing.T) {
			_, schemas, records, principal := newRecordOperationsTest(t)
			ctx := context.Background()
			applyDeleteCollection(t, ctx, schemas, principal.ID, schema.CreateCollectionInput{Name: "rename_targets", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}}})
			input := deleteRelationField("owner", "rename_targets", cardinality, schema.RelationRestrict)
			source := applyDeleteCollection(t, ctx, schemas, principal.ID, schema.CreateCollectionInput{Name: "rename_sources", Fields: []schema.FieldInput{input}})
			target := createDeleteRecord(t, ctx, records, principal, "rename_targets", "rename-target", map[string]json.RawMessage{"title": json.RawMessage(`"target"`)})
			value := relationUUIDJSON(recordResultID(t, target))
			if cardinality == schema.RelationMultiple {
				value = relationUUIDArrayJSON(recordResultID(t, target))
			}
			created := createDeleteRecord(t, ctx, records, principal, source.Name, "rename-source", map[string]json.RawMessage{"owner": value})
			input.ID, input.Name = &source.Fields[0].ID, "author"
			renameOperationFields(t, schemas, principal.ID, source, []schema.FieldInput{input})
			id := recordResultID(t, created)
			current, err := records.Get(ctx, GetRequest{Principal: principal, RequestID: "get-renamed-relation", Collection: source.Name, ID: id, Expand: "author"})
			if err != nil {
				t.Fatalf("read renamed relation: %v", err)
			}
			if current.Data["author"] == nil {
				t.Fatalf("missing renamed relation: %#v", current.Data)
			}
			updated, err := records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "update-renamed-relation", Collection: source.Name, ID: id, Input: map[string]json.RawMessage{"author": value}, IfMatch: EntityTagSet{Present: true, Values: []string{current.ETag}}})
			if err != nil {
				t.Fatalf("update renamed relation: %v", err)
			}
			if _, err := records.Delete(ctx, DeleteRequest{Principal: principal, RequestID: "delete-renamed-relation", Collection: source.Name, ID: id, IfMatch: EntityTagSet{Present: true, Values: []string{updated.Record.ETag}}}); err != nil {
				t.Fatalf("delete renamed relation: %v", err)
			}
		})
	}
}

func renameOperationFields(t *testing.T, schemas *schema.Service, administrator uuid.UUID, collection schema.Collection, fields []schema.FieldInput) {
	t.Helper()
	change := schema.NewUpdateChange(schema.UpdateCollectionInput{Name: collection.Name, Fields: fields})
	plan, err := schemas.Preview(context.Background(), change)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schemas.Apply(context.Background(), schema.LegacyApply(change, plan, administrator)); err != nil {
		t.Fatal(err)
	}
}
