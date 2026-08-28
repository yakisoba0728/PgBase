package schema

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestPlanCreateIsDeterministic(t *testing.T) {
	input := CreateCollectionInput{
		Name: "posts",
		Fields: []FieldInput{
			{Name: "title", Type: TypeText, Required: true, Indexed: true},
			{Name: "views", Type: TypeBigInt, Default: json.RawMessage(`"0"`)},
		},
	}
	planner := Planner{}
	first, err := planner.PlanCreate(7, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := planner.PlanCreate(7, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Checksum != second.Checksum || !reflect.DeepEqual(first.Statements, second.Statements) {
		t.Fatalf("plans differ: %#v %#v", first, second)
	}
	if got, want := first.Statements, []string{
		`CREATE TABLE "app"."posts" ("id" uuid primary key, "created_at" timestamptz not null, "updated_at" timestamptz not null, "title" text not null, "views" bigint default 0)`,
		`CREATE INDEX "idx_34beac8152cca6ed" ON "app"."posts" ("title")`,
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("statements = %#v, want %#v", got, want)
	}
}

func TestPlanCreateValidatesFieldsAndAvoidsRedundantIndexes(t *testing.T) {
	planner := Planner{}
	_, err := planner.PlanCreate(0, CreateCollectionInput{
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

	plan, err := planner.PlanCreate(0, CreateCollectionInput{
		Name:   "posts",
		Fields: []FieldInput{{Name: "slug", Type: TypeText, Unique: true, Indexed: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Statements) != 2 || !strings.HasPrefix(plan.Statements[1], `CREATE UNIQUE INDEX `) {
		t.Fatalf("unique indexed field statements = %#v, want one unique index", plan.Statements)
	}
}

func TestPlanCreateRejectsInjectionShapedMetadata(t *testing.T) {
	planner := Planner{}
	_, err := planner.PlanCreate(0, CreateCollectionInput{
		Name:   `posts; DROP TABLE pgbase_meta.collections`,
		Fields: []FieldInput{{Name: "title", Type: TypeText, Default: json.RawMessage(`{"sql":"'x'; DROP TABLE app.posts"}`)}},
	})
	if err == nil {
		t.Fatal("PlanCreate succeeded for injection-shaped metadata")
	}
	if strings.Contains(err.Error(), "DROP TABLE") {
		t.Fatalf("validation error leaked untrusted SQL: %v", err)
	}

	_, err = planner.PlanCreate(0, CreateCollectionInput{
		Name:   "posts",
		Fields: []FieldInput{{Name: "title", Type: TypeText, Default: json.RawMessage(`{"sql":"'x'; DROP TABLE app.posts"}`)}},
	})
	var validation *ValidationError
	if !errors.As(err, &validation) || len(validation.Details) != 1 || validation.Details[0] != (ValidationDetail{Field: "fields.0.default", Code: "invalid_default"}) {
		t.Fatalf("injection-shaped default error = %#v, want fields.0.default -> invalid_default", err)
	}
}

func TestPlanCreateCanonicalizesJSONDefaultsAndSupportsEveryFieldType(t *testing.T) {
	planner := Planner{}
	plan, err := planner.PlanCreate(3, CreateCollectionInput{
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
	if len(plan.Statements) != 2 || !strings.Contains(plan.Statements[0], `"metadata" jsonb default E'{"a":2,"z":1}'::jsonb`) {
		t.Fatalf("statements = %#v, want typed DDL and one unique index", plan.Statements)
	}
	if !strings.HasPrefix(plan.Statements[1], "CREATE UNIQUE INDEX ") {
		t.Errorf("index statement = %q, want a unique index", plan.Statements[1])
	}
	indexTokens := strings.Fields(plan.Statements[1])
	if len(indexTokens) < 4 || len(strings.Trim(indexTokens[3], `"`)) > 63 {
		t.Errorf("index name is not a PostgreSQL-safe length: %q", plan.Statements[1])
	}

	changedVersion, err := planner.PlanCreate(4, CreateCollectionInput{Name: "all_types", Fields: planInputFields(plan)})
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
