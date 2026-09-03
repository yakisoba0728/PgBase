package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// planTestCatalog builds the catalog shape the compiled cache guarantees:
// keyed by collection name, with the derived incoming-relation index present.
func planTestCatalog(version int64, collections ...Collection) Catalog {
	byName := make(map[string]Collection, len(collections))
	for _, collection := range collections {
		byName[collection.Name] = collection
	}
	return Catalog{Version: version, Collections: byName, Incoming: buildIncomingIndex(byName)}
}

func planCreateAt(version int64, input CreateCollectionInput) (Plan, error) {
	return CompilePlan(planTestCatalog(version), NewCreateChange(input))
}

func planUpdateAt(version int64, current Collection, input UpdateCollectionInput) (Plan, error) {
	return CompilePlan(planTestCatalog(version, current), NewUpdateChange(input))
}

func planDeleteAt(version int64, current Collection) (Plan, error) {
	return CompilePlan(planTestCatalog(version, current), NewDeleteChange(current.Name))
}

func TestPlannerPlanCopiesTheInputOutOfTheCallersReach(t *testing.T) {
	current := updateTestCollection()
	catalog := planTestCatalog(7, current)
	createInput := CreateCollectionInput{Name: "comments", Fields: []FieldInput{{
		Name: "body", Type: TypeText, Default: json.RawMessage(`"draft"`),
	}}}
	updateInput := UpdateCollectionInput{Name: current.Name, Fields: planInputFields(Plan{Definition: PlannedCollection{Fields: []PlannedField{
		{Name: "title", Type: TypeText, Required: true, Indexed: true},
		{Name: "views", Type: TypeBigInt},
	}}})}

	tests := []struct {
		name   string
		change Change
	}{
		{name: "create", change: NewCreateChange(createInput)},
		{name: "update", change: NewUpdateChange(updateInput)},
		{name: "delete", change: NewDeleteChange(current.Name)},
	}
	want := make([]Plan, len(tests))
	for index, test := range tests {
		plan, err := CompilePlan(catalog, test.change)
		if err != nil {
			t.Fatal(err)
		}
		want[index] = plan
	}

	createInput.Name = "mutated"
	createInput.Fields[0].Name = "mutated"
	updateInput.Name = "mutated"
	updateInput.Fields[0].Name = "mutated"

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := CompilePlan(catalog, test.change)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want[index]) {
				t.Fatalf("Plan() = %#v, want %#v", got, want[index])
			}
		})
	}
}

func TestPlannerPlanRejectsACatalogEntryFiledUnderAnotherName(t *testing.T) {
	current := updateTestCollection()
	catalog := Catalog{Version: 7, Collections: map[string]Collection{"others": current}}
	_, err := CompilePlan(catalog, NewUpdateChange(UpdateCollectionInput{Name: "others"}))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Plan(mis-keyed catalog) error = %v, want ErrConflict", err)
	}
}

func TestPlannerPlanRejectsAChangeForAnAbsentCollection(t *testing.T) {
	catalog := planTestCatalog(7)
	if _, err := CompilePlan(catalog, NewUpdateChange(UpdateCollectionInput{Name: "posts"})); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("Plan(update absent) error = %v, want ErrCollectionNotFound", err)
	}
	if _, err := CompilePlan(catalog, NewDeleteChange("posts")); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("Plan(delete absent) error = %v, want ErrCollectionNotFound", err)
	}
}

func TestPlannerPlanRejectsUnconstructedChange(t *testing.T) {
	_, err := CompilePlan(Catalog{Collections: map[string]Collection{}}, Change{})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Plan(zero Change) error = %v, want ErrConflict", err)
	}
}

func TestPlanCreateIsDeterministic(t *testing.T) {
	input := CreateCollectionInput{
		Name: "posts",
		Fields: []FieldInput{
			{Name: "title", Type: TypeText, Required: true, Indexed: true},
			{Name: "views", Type: TypeBigInt, Default: json.RawMessage(`"0"`)},
		},
	}
	first, err := planCreateAt(7, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := planCreateAt(7, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Checksum != second.Checksum || !reflect.DeepEqual(first.Statements, second.Statements) {
		t.Fatalf("plans differ: %#v %#v", first, second)
	}
	if got, want := first.Statements, []string{
		`CREATE TABLE "app"."posts" ("id" uuid primary key, "created_at" timestamptz not null, "updated_at" timestamptz not null, "title" text not null, "views" bigint default 0)`,
		`CREATE INDEX "idx_d072ae317b2f5e0e" ON "app"."posts" ("created_at", "id")`,
		`CREATE INDEX "idx_34beac8152cca6ed" ON "app"."posts" ("title")`,
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("statements = %#v, want %#v", got, want)
	}
}

func TestPlanChecksumIncludesCompilerVersion(t *testing.T) {
	plan := Plan{
		Operation:   "create",
		BaseVersion: 7,
		Definition:  PlannedCollection{Name: "versioned_checksum", TableName: "versioned_checksum"},
		Statements:  []string{`CREATE TABLE "app"."versioned_checksum" ()`},
	}
	first, err := planChecksumWithCompilerVersion(plan, "schema-plan-v1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := planChecksumWithCompilerVersion(plan, "schema-plan-v2")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("checksum did not include the compiler version")
	}
	if _, err := planChecksumWithCompilerVersion(plan, ""); err == nil {
		t.Fatal("checksum accepted an empty compiler version")
	}
}

func TestPlanCreateValidatesFieldsAndAvoidsRedundantIndexes(t *testing.T) {
	_, err := planCreateAt(0, CreateCollectionInput{
		Name: "posts",
		Fields: []FieldInput{
			{Name: "title", Type: TypeText},
			{Name: "title", Type: TypeText},
		},
	})
	var validation *ValidationError
	if !errors.As(err, &validation) || len(validation.Details) != 1 || validation.Details[0] != (ValidationDetail{Field: "fields.1.name", Code: "duplicate_name"}) {
		t.Fatalf("duplicate fields error = %#v, want fields.1.name -> duplicate_name", err)
	}

	plan, err := planCreateAt(0, CreateCollectionInput{
		Name:   "posts",
		Fields: []FieldInput{{Name: "slug", Type: TypeText, Unique: true, Indexed: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Statements) != 3 || !strings.HasPrefix(plan.Statements[2], `CREATE UNIQUE INDEX `) {
		t.Fatalf("unique indexed field statements = %#v, want one unique index", plan.Statements)
	}
}

func TestPlanCreateRejectsDefinitionsBeyondPostgreSQLLimits(t *testing.T) {
	fields := make([]FieldInput, maximumCollectionFields+1)
	for index := range fields {
		fields[index] = FieldInput{Name: fmt.Sprintf("field_%d", index), Type: TypeText}
	}
	_, err := planCreateAt(0, CreateCollectionInput{Name: "too_wide", Fields: fields})
	var validation *ValidationError
	if !errors.As(err, &validation) || !reflect.DeepEqual(validation.Details, []ValidationDetail{{Field: "fields", Code: "too_many_fields"}}) {
		t.Fatalf("wide collection error = %#v", err)
	}

	for _, test := range []struct {
		name string
		typ  FieldType
		raw  json.RawMessage
	}{
		{name: "numeric precision", typ: TypeNumeric, raw: json.RawMessage(`"` + strings.Repeat("9", 131073) + `"`)},
		{name: "jsonb exponent", typ: TypeJSONB, raw: json.RawMessage(`{"value":1e131072}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := planCreateAt(0, CreateCollectionInput{Name: "limits", Fields: []FieldInput{{Name: "value", Type: test.typ, Default: test.raw}}})
			if !errors.As(err, &validation) || validation.Details[0].Code != "invalid_default" {
				t.Fatalf("limit error = %#v", err)
			}
		})
	}
}

func TestPlanCreateRejectsTimestampYearZeroAtTheDefaultField(t *testing.T) {
	_, err := planCreateAt(0, CreateCollectionInput{
		Name:   "events",
		Fields: []FieldInput{{Name: "starts_at", Type: TypeTimestampTZ, Default: json.RawMessage(`"0000-01-01T00:00:00Z"`)}},
	})
	var validation *ValidationError
	want := []ValidationDetail{{Field: "fields.0.default", Code: "invalid_default"}}
	if !errors.As(err, &validation) || !reflect.DeepEqual(validation.Details, want) {
		t.Fatalf("timestamp year-zero error = %#v, want %#v", err, want)
	}
}

func TestPlanCreateRejectsInjectionShapedMetadata(t *testing.T) {
	_, err := planCreateAt(0, CreateCollectionInput{
		Name:   `posts; DROP TABLE pgbase_meta.collections`,
		Fields: []FieldInput{{Name: "title", Type: TypeText, Default: json.RawMessage(`{"sql":"'x'; DROP TABLE app.posts"}`)}},
	})
	if err == nil {
		t.Fatal("PlanCreate succeeded for injection-shaped metadata")
	}
	if strings.Contains(err.Error(), "DROP TABLE") {
		t.Fatalf("validation error leaked untrusted SQL: %v", err)
	}

	_, err = planCreateAt(0, CreateCollectionInput{
		Name:   "posts",
		Fields: []FieldInput{{Name: "title", Type: TypeText, Default: json.RawMessage(`{"sql":"'x'; DROP TABLE app.posts"}`)}},
	})
	var validation *ValidationError
	if !errors.As(err, &validation) || len(validation.Details) != 1 || validation.Details[0] != (ValidationDetail{Field: "fields.0.default", Code: "invalid_default"}) {
		t.Fatalf("injection-shaped default error = %#v, want fields.0.default -> invalid_default", err)
	}
}

func TestPlanCreateCanonicalizesJSONDefaultsAndSupportsEveryFieldType(t *testing.T) {
	plan, err := planCreateAt(3, CreateCollectionInput{
		Name: "all_types",
		Fields: []FieldInput{
			{Name: "flag", Type: TypeBoolean, Default: json.RawMessage("false")},
			{Name: "count", Type: TypeInteger, Default: json.RawMessage(`"1"`)},
			{Name: "total", Type: TypeBigInt, Default: json.RawMessage(`"2"`)},
			{Name: "ratio", Type: TypeNumeric, Default: json.RawMessage(`"3.40"`)},
			{Name: "label", Type: TypeText, Default: json.RawMessage(`"safe"`)},
			{Name: "published_at", Type: TypeTimestampTZ, Default: json.RawMessage(`{"kind":"now"}`)},
			{Name: "external_id", Type: TypeUUID, Default: json.RawMessage(`"12345678-1234-4567-8901-123456789abc"`)},
			{Name: "metadata", Type: TypeJSONB, Default: json.RawMessage(`{"z":1,"a":2}`), Unique: true, Indexed: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(plan.Definition.Fields[7].Default), `{"a":2,"z":1}`; got != want {
		t.Errorf("canonical JSONB default = %s, want %s", got, want)
	}
	if len(plan.Statements) != 3 || !strings.Contains(plan.Statements[0], `"metadata" jsonb default E'{"a":2,"z":1}'::jsonb`) {
		t.Fatalf("statements = %#v, want typed DDL and one unique index", plan.Statements)
	}
	if !strings.HasPrefix(plan.Statements[2], "CREATE UNIQUE INDEX ") {
		t.Errorf("index statement = %q, want a unique index", plan.Statements[2])
	}
	indexTokens := strings.Fields(plan.Statements[2])
	if len(indexTokens) < 4 || len(strings.Trim(indexTokens[3], `"`)) > 63 {
		t.Errorf("index name is not a PostgreSQL-safe length: %q", plan.Statements[2])
	}

	changedVersion, err := planCreateAt(4, CreateCollectionInput{Name: "all_types", Fields: planInputFields(plan)})
	if err != nil {
		t.Fatal(err)
	}
	if changedVersion.Checksum == plan.Checksum {
		t.Error("checksum did not include BaseVersion")
	}
}

func planInputFields(plan Plan) []FieldInput {
	fields := make([]FieldInput, 0, len(plan.Definition.Fields))
	for _, field := range plan.Definition.Fields {
		fields = append(fields, FieldInput{
			Name: field.Name, Type: field.Type, Required: field.Required, Unique: field.Unique, Indexed: field.Indexed, Default: field.Default,
		})
	}
	return fields
}

func updateTestCollection() Collection {
	return Collection{
		ID:        uuid.MustParse("019551d0-e9c0-7000-8000-0000000000aa"),
		Name:      "posts",
		TableName: "posts",
		Fields: []Field{
			{ID: uuid.MustParse("019551d0-e9c0-7000-8000-0000000000ab"), Name: "title", ColumnName: "title", Type: TypeText, Required: true, Indexed: true, Ordinal: 0},
			{ID: uuid.MustParse("019551d0-e9c0-7000-8000-0000000000ac"), Name: "views", ColumnName: "views", Type: TypeBigInt, Ordinal: 1},
		},
	}
}

func TestPlanUpdateIsDeterministicWithGoldenStatements(t *testing.T) {
	input := UpdateCollectionInput{
		Name: "posts",
		Fields: []FieldInput{
			{Name: "title", Type: TypeText, Required: true, Indexed: true},
			{Name: "views", Type: TypeNumeric},
			{Name: "extra", Type: TypeBoolean, Default: json.RawMessage("false")},
		},
	}
	first, err := planUpdateAt(7, updateTestCollection(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := planUpdateAt(7, updateTestCollection(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Checksum != second.Checksum || !reflect.DeepEqual(first.Statements, second.Statements) {
		t.Fatalf("plans differ: %#v %#v", first, second)
	}
	if got, want := first.Statements, []string{
		`ALTER TABLE "app"."posts" ALTER COLUMN "views" TYPE numeric USING "views"::numeric`,
		`ALTER TABLE "app"."posts" ADD COLUMN "extra" boolean default FALSE`,
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("statements = %#v, want %#v", got, want)
	}
	if first.Operation != updateCollectionOperation {
		t.Errorf("operation = %q, want %q", first.Operation, updateCollectionOperation)
	}
	if got, want := len(first.Definition.Fields), 3; got != want {
		t.Errorf("definition field count = %d, want %d", got, want)
	}
}

func TestPlanUpdateDropsRemovedFieldsAndKeepsOrderChangesMetadataOnly(t *testing.T) {
	drop := UpdateCollectionInput{
		Name:   "posts",
		Fields: []FieldInput{{Name: "title", Type: TypeText, Required: true, Indexed: true}},
	}
	plan, err := planUpdateAt(7, updateTestCollection(), drop)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := plan.Statements, []string{`ALTER TABLE "app"."posts" DROP COLUMN "views"`}; !reflect.DeepEqual(got, want) {
		t.Fatalf("drop statements = %#v, want %#v", got, want)
	}
	if got, want := len(plan.Definition.Fields), 1; got != want {
		t.Fatalf("definition field count = %d, want %d", got, want)
	}

	reorder := UpdateCollectionInput{
		Name:   "posts",
		Fields: []FieldInput{{Name: "views", Type: TypeBigInt}, {Name: "title", Type: TypeText, Required: true, Indexed: true}},
	}
	plan, err = planUpdateAt(7, updateTestCollection(), reorder)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Statements) != 0 {
		t.Fatalf("reorder statements = %#v, want metadata-only change", plan.Statements)
	}
	if plan.Definition.Fields[0].Name != "views" || plan.Definition.Fields[0].Ordinal != 0 {
		t.Errorf("reordered definition = %#v, want views first", plan.Definition.Fields)
	}
}

func TestPlanUpdateCreatesIndexesForAddedFields(t *testing.T) {
	base := Collection{Name: "pages", TableName: "pages", Fields: []Field{{Name: "body", ColumnName: "body", Type: TypeText, Ordinal: 0}}}
	plan, err := planUpdateAt(4, base, UpdateCollectionInput{
		Name: "pages",
		Fields: []FieldInput{
			{Name: "body", Type: TypeText},
			{Name: "slug", Type: TypeText, Required: true, Unique: true, Indexed: true, Default: json.RawMessage(`""`)},
			{Name: "views", Type: TypeInteger, Indexed: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := plan.Statements, []string{
		`ALTER TABLE "app"."pages" ADD COLUMN "slug" text not null default E''::text`,
		`CREATE UNIQUE INDEX "idx_282a0bddc05f55f9" ON "app"."pages" ("slug")`,
		`ALTER TABLE "app"."pages" ADD COLUMN "views" integer`,
		`CREATE INDEX "idx_1580b5fe6b8166d2" ON "app"."pages" ("views")`,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("statements = %#v, want %#v", got, want)
	}
}

func TestPlanUpdateManagesFieldIndexes(t *testing.T) {
	promote := UpdateCollectionInput{
		Name:   "posts",
		Fields: []FieldInput{{Name: "title", Type: TypeText, Required: true, Unique: true}, {Name: "views", Type: TypeBigInt, Indexed: true}},
	}
	plan, err := planUpdateAt(7, updateTestCollection(), promote)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := plan.Statements, []string{
		`DROP INDEX IF EXISTS "app"."idx_34beac8152cca6ed"`,
		`CREATE UNIQUE INDEX "idx_34beac8152cca6ed" ON "app"."posts" ("title")`,
		`CREATE INDEX "idx_a1bd4f3cf569ea82" ON "app"."posts" ("views")`,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("index promotion statements = %#v, want %#v", got, want)
	}

	demote := UpdateCollectionInput{
		Name:   "posts",
		Fields: []FieldInput{{Name: "title", Type: TypeText, Required: true}, {Name: "views", Type: TypeBigInt}},
	}
	plan, err = planUpdateAt(7, updateTestCollection(), demote)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := plan.Statements, []string{`DROP INDEX IF EXISTS "app"."idx_34beac8152cca6ed"`}; !reflect.DeepEqual(got, want) {
		t.Fatalf("index demote statements = %#v, want %#v", got, want)
	}
}

func TestPlanUpdateRejectsUnsupportedTypeChangesAndUnsafeAdds(t *testing.T) {
	cases := []struct {
		name  string
		input UpdateCollectionInput
		want  ValidationDetail
	}{
		{
			name: "text to integer",
			input: UpdateCollectionInput{
				Name:   "posts",
				Fields: []FieldInput{{Name: "title", Type: TypeInteger}},
			},
			want: ValidationDetail{Field: "fields.0.type", Code: "unsupported_schema_change"},
		},
		{
			name: "bigint to text",
			input: UpdateCollectionInput{
				Name:   "posts",
				Fields: []FieldInput{{Name: "title", Type: TypeText, Required: true, Indexed: true}, {Name: "views", Type: TypeText}},
			},
			want: ValidationDetail{Field: "fields.1.type", Code: "unsupported_schema_change"},
		},
		{
			name: "required without default",
			input: UpdateCollectionInput{
				Name:   "posts",
				Fields: []FieldInput{{Name: "title", Type: TypeText, Required: true, Indexed: true}, {Name: "views", Type: TypeBigInt}, {Name: "slug", Type: TypeText, Required: true}},
			},
			want: ValidationDetail{Field: "fields.2.required", Code: "missing_default"},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := planUpdateAt(7, updateTestCollection(), test.input)
			var validation *ValidationError
			if !errors.As(err, &validation) || !reflect.DeepEqual(validation.Details, []ValidationDetail{test.want}) {
				t.Fatalf("error = %#v, want %#v", err, test.want)
			}
			if strings.Contains(err.Error(), "ALTER TABLE") || strings.Contains(err.Error(), "DROP TABLE") {
				t.Fatalf("validation error leaked untrusted SQL: %v", err)
			}
		})
	}
}

func TestPlanUpdateAllowsWhitelistedWideningChanges(t *testing.T) {
	base := Collection{
		Name: "measurements", TableName: "measurements",
		Fields: []Field{{Name: "amount", ColumnName: "amount", Type: TypeInteger, Ordinal: 0}},
	}
	plan, err := planUpdateAt(2, base, UpdateCollectionInput{
		Name:   "measurements",
		Fields: []FieldInput{{Name: "amount", Type: TypeNumeric}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := plan.Statements, []string{
		`ALTER TABLE "app"."measurements" ALTER COLUMN "amount" TYPE numeric USING "amount"::numeric`,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("statements = %#v, want %#v", got, want)
	}
}

func TestPlanUpdateRejectsNameAndStateMismatches(t *testing.T) {
	renamed := updateTestCollection()
	renamed.TableName = "posts_v2"
	if _, err := planUpdateAt(7, renamed, UpdateCollectionInput{Name: "posts", Fields: nil}); !errors.Is(err, ErrConflict) {
		t.Fatalf("table name drift error = %v, want ErrConflict", err)
	}
	if _, err := planUpdateAt(7, updateTestCollection(), UpdateCollectionInput{Name: "posts", Fields: []FieldInput{{Name: "title", Type: TypeText, Required: true, Indexed: true}, {Name: "views", Type: TypeBigInt}, {Name: "extra", Type: TypeBoolean}}}); err != nil {
		t.Fatal(err)
	}
}

func TestPlanUpdateEmitsSingleDefaultChangeWithTypeChange(t *testing.T) {
	base := Collection{
		Name: "measurements", TableName: "measurements",
		Fields: []Field{{Name: "amount", ColumnName: "amount", Type: TypeInteger, Default: json.RawMessage(`"5"`), Ordinal: 0}},
	}
	plan, err := planUpdateAt(2, base, UpdateCollectionInput{
		Name:   "measurements",
		Fields: []FieldInput{{Name: "amount", Type: TypeBigInt, Default: json.RawMessage(`"7"`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := plan.Statements, []string{
		`ALTER TABLE "app"."measurements" ALTER COLUMN "amount" TYPE bigint USING "amount"::bigint, ALTER COLUMN "amount" SET DEFAULT 7`,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("statements = %#v, want %#v", got, want)
	}

	kept, err := planUpdateAt(2, base, UpdateCollectionInput{
		Name:   "measurements",
		Fields: []FieldInput{{Name: "amount", Type: TypeBigInt, Default: json.RawMessage(`"5"`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := kept.Statements, []string{
		`ALTER TABLE "app"."measurements" ALTER COLUMN "amount" TYPE bigint USING "amount"::bigint`,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("kept-default statements = %#v, want %#v", got, want)
	}
}

func TestPlanUpdateCarriesOnlyAnUnchangedLegacyOversizedDefault(t *testing.T) {
	legacy := json.RawMessage(`"` + strings.Repeat("x", MaximumTextBytes+1) + `"`)
	base := Collection{
		Name: "legacy_defaults", TableName: "legacy_defaults",
		Fields: []Field{{
			Name: "body", ColumnName: "body", Type: TypeText, Default: legacy, Ordinal: 0,
		}},
	}
	input := UpdateCollectionInput{
		Name:   "legacy_defaults",
		Fields: []FieldInput{{Name: "body", Type: TypeText, Default: append(json.RawMessage(nil), legacy...)}},
	}

	first, err := planUpdateAt(7, base, input)
	if err != nil {
		t.Fatalf("PlanUpdate unchanged legacy default: %v", err)
	}
	second, err := planUpdateAt(7, base, input)
	if err != nil {
		t.Fatalf("PlanUpdate unchanged legacy default again: %v", err)
	}
	if len(first.Statements) != 0 {
		t.Fatalf("unchanged legacy default emitted DDL: %#v", first.Statements)
	}
	if got := string(first.Definition.Fields[0].Default); got != string(legacy) {
		t.Fatalf("carried default changed bytes: got %d bytes, want %d", len(got), len(legacy))
	}
	if first.Checksum == "" || first.Checksum != second.Checksum || !reflect.DeepEqual(first, second) {
		t.Fatalf("unchanged legacy plan is not byte-stable: first=%#v second=%#v", first, second)
	}

	changed := input
	changed.Fields = append([]FieldInput(nil), input.Fields...)
	changed.Fields[0].Default = json.RawMessage(`"` + strings.Repeat("y", MaximumTextBytes+1) + `"`)
	if _, err := planUpdateAt(7, base, changed); err == nil {
		t.Fatal("PlanUpdate accepted a changed oversized default")
	}

	added := input
	added.Fields = append(append([]FieldInput(nil), input.Fields...), FieldInput{
		Name: "other", Type: TypeText, Default: legacy,
	})
	if _, err := planUpdateAt(7, base, added); err == nil {
		t.Fatal("PlanUpdate accepted a new oversized default")
	}
}

func TestPlanDeleteProducesSingleDropStatement(t *testing.T) {
	first, err := planDeleteAt(9, updateTestCollection())
	if err != nil {
		t.Fatal(err)
	}
	second, err := planDeleteAt(9, updateTestCollection())
	if err != nil {
		t.Fatal(err)
	}
	if first.Checksum != second.Checksum {
		t.Fatal("delete plans are not deterministic")
	}
	if got, want := first.Statements, []string{`DROP TABLE "app"."posts"`}; !reflect.DeepEqual(got, want) {
		t.Fatalf("statements = %#v, want %#v", got, want)
	}
	if first.Operation != deleteCollectionOperation {
		t.Errorf("operation = %q, want %q", first.Operation, deleteCollectionOperation)
	}
	if first.BaseVersion != 9 {
		t.Errorf("base version = %d, want 9", first.BaseVersion)
	}
}
