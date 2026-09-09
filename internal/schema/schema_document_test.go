package schema

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestSchemaImportRuleValidationUsesProjectedCatalogForNoOpCollections(t *testing.T) {
	service := &Service{}
	plan := SchemaImportPlan{Document: SchemaDocument{Collections: []SchemaDocumentCollection{{Name: "posts", Rules: json.RawMessage(`{"field":"title"}`)}}}, validationCatalog: Catalog{Collections: map[string]Collection{"posts": {Name: "posts", Fields: []Field{{Name: "title", Type: TypeText}}}}}}
	err := service.ValidateSchemaImportPlan(plan, func(collection Collection, _ json.RawMessage) error {
		if len(collection.Fields) != 1 || collection.Fields[0].Name != "title" {
			return errors.New("missing projected field")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSchemaImportMergePreservesOmittedFieldsAndIndexes(t *testing.T) {
	id := uuid.New()
	catalog := Catalog{Collections: map[string]Collection{"posts": {
		ID: uuid.New(), Name: "posts", TableName: "posts",
		Fields:  []Field{{ID: id, Name: "kept", ColumnName: "kept", Type: TypeText, Help: "existing"}},
		Indexes: []Index{{ID: uuid.New(), Name: "kept_idx", Fields: []string{"kept"}}},
	}}}
	changes, err := importChanges(catalog, SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{{Name: "posts", Fields: []FieldInput{{Name: "added", Type: TypeInteger}}}}})
	if err != nil {
		t.Fatal(err)
	}
	input := changes[0].update
	if len(input.Fields) != 2 || input.Fields[1].ID == nil || *input.Fields[1].ID != id || input.Fields[1].Help != "existing" {
		t.Fatalf("fields=%+v", input.Fields)
	}
	if input.Indexes == nil || len(*input.Indexes) != 1 || (*input.Indexes)[0].Name != "kept_idx" {
		t.Fatalf("indexes=%+v", input.Indexes)
	}
}

func TestSchemaImportRejectsConflictingExistingFieldType(t *testing.T) {
	catalog := Catalog{Collections: map[string]Collection{"posts": {
		ID: uuid.New(), Name: "posts", TableName: "posts",
		Fields: []Field{{ID: uuid.New(), Name: "value", ColumnName: "value", Type: TypeText}},
	}}}
	_, err := importChanges(catalog, SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{{Name: "posts", Fields: []FieldInput{{Name: "value", Type: TypeInteger}}}}})
	var validationError *ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("error=%v", err)
	}
}

func TestSchemaImportRejectsUnsupportedViewFieldMetadata(t *testing.T) {
	plan := Plan{Definition: PlannedCollection{Name: "report", Fields: []PlannedField{{Name: "total", Type: TypeInteger}}}}
	item := SchemaDocumentCollection{Name: "report", View: &ViewInput{SQL: "select 1"}, Fields: []FieldInput{{Name: "total", Type: TypeInteger, Options: &FieldOptions{Min: intPointerString("1")}}}}
	if err := applyImportedViewPresentation(&plan, item); err == nil {
		t.Fatal("expected view_field_mismatch")
	}
	id := uuid.New()
	item.Fields[0] = FieldInput{ID: &id, Name: "total", Type: TypeInteger}
	if err := applyImportedViewPresentation(&plan, item); err == nil {
		t.Fatal("expected forbidden view field ID")
	}
}

func intPointerString(value string) *string { return &value }
