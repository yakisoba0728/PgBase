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

func TestSchemaDocumentChecksumSealsOrderedPlans(t *testing.T) {
	document := SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{{Name: "posts"}}}
	first := []Plan{{Operation: createCollectionOperation, BaseVersion: 4, Statements: []string{"create table app.posts(id uuid)"}}}
	second := []Plan{{Operation: createCollectionOperation, BaseVersion: 4, Statements: []string{"create table app.posts(id uuid, title text)"}}}
	one, err := schemaDocumentChecksum(4, "policy", document, first)
	if err != nil {
		t.Fatal(err)
	}
	two, err := schemaDocumentChecksum(4, "policy", document, second)
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Fatalf("checksums match for different reviewed DDL plans: %s", one)
	}
	reordered, err := schemaDocumentChecksum(4, "policy", document, []Plan{
		{Operation: createCollectionOperation, BaseVersion: 4, Statements: []string{"select 2"}},
		{Operation: updateCollectionOperation, BaseVersion: 5, Statements: []string{"select 1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := schemaDocumentChecksum(4, "policy", document, []Plan{
		{Operation: updateCollectionOperation, BaseVersion: 5, Statements: []string{"select 1"}},
		{Operation: createCollectionOperation, BaseVersion: 4, Statements: []string{"select 2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reordered == reversed {
		t.Fatalf("checksum does not bind plan order: %s", reordered)
	}
}

func TestSchemaDocumentChecksumDistinguishesCompilerGenerations(t *testing.T) {
	catalog := Catalog{Collections: map[string]Collection{}}
	document := SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText, Indexed: true}}}}}
	change := NewCreateChange(CreateCollectionInput{Name: "posts", Fields: document.Collections[0].Fields})
	v1, err := compilePlan(catalog, change, false)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := compilePlan(catalog, change, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(v1.Statements) == len(v2.Statements) {
		t.Fatalf("compiler fixtures did not produce different DDL: v1=%#v v2=%#v", v1.Statements, v2.Statements)
	}
	first, err := schemaDocumentChecksum(0, "policy", document, []Plan{v1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := schemaDocumentChecksum(0, "policy", document, []Plan{v2})
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("schema import checksum did not bind compiler generation: %s", first)
	}
}

func TestSchemaImportCollectionMetadataChangeIsNotANoOp(t *testing.T) {
	current := Collection{ID: uuid.New(), Name: "posts", CompatibilityName: "Posts", Type: CollectionTypeBase, ValidationMode: ValidationModePostgres, RecordID: DefaultRecordIDPolicy(), TableName: "posts"}
	tests := []struct {
		name     string
		metadata SchemaDocumentCollection
	}{
		{name: "compatibility name", metadata: SchemaDocumentCollection{CompatibilityName: "LegacyPosts"}},
		{name: "type", metadata: SchemaDocumentCollection{Type: CollectionTypeAuth}},
		{name: "system", metadata: SchemaDocumentCollection{System: true}},
		{name: "validation mode", metadata: SchemaDocumentCollection{ValidationMode: ValidationModePocketBase}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := test.metadata
			item.Name = "posts"
			document := SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{item}}
			changes, err := importChanges(Catalog{Collections: map[string]Collection{"posts": current}}, document)
			if err != nil {
				t.Fatal(err)
			}
			if len(changes) != 1 || changes[0].kind != changeUpdate {
				t.Fatalf("changes=%#v, want one collection metadata update", changes)
			}
		})
	}
}

func TestApplyMigrationViewFieldsValidatesOverriddenMetadata(t *testing.T) {
	tests := []struct {
		name  string
		field FieldInput
	}{
		{name: "hidden presentable", field: FieldInput{Name: "title", Type: TypeText, Hidden: true, Presentable: true}},
		{name: "overlong help", field: FieldInput{Name: "title", Type: TypeText, Help: string(make([]rune, 301))}},
		{name: "incompatible options", field: FieldInput{Name: "title", Type: TypeText, Options: &FieldOptions{Thumbs: []string{"20x20"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := Plan{Definition: PlannedCollection{Name: "report", Fields: []PlannedField{{Name: "title", Type: TypeText}}}}
			snapshot := MigrationSnapshotCollection{Name: "report", View: &ViewInput{SQL: "select title from app.posts"}, Fields: []FieldInput{test.field}}
			if err := applyMigrationViewFields(&plan, snapshot); err == nil {
				t.Fatal("invalid migration view field metadata accepted")
			}
		})
	}
}

func intPointerString(value string) *string { return &value }
