//go:build integration

package schema

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestClosurePocketBaseRequiredPreflight(t *testing.T) {
	for _, tc := range []struct {
		name      string
		kind      FieldType
		value     string
		required  bool
		wantError bool
	}{{"true_boolean", TypeBoolean, "true", true, false}, {"false_boolean", TypeBoolean, "false", true, true}, {"blank_text", TypeText, "''", false, true}, {"zero_integer", TypeInteger, "0", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			pool, service, actor := newSchemaServiceTest(t)
			ctx := context.Background()
			mode := ValidationModePostgres
			if !tc.required {
				mode = ValidationModePocketBase
			}
			created := applyFreshPlan(t, service, CreateCollectionInput{Name: "closure_required", ValidationMode: mode, Fields: []FieldInput{{Name: "value", Type: tc.kind, Required: tc.required}}}, actor)
			if _, err := pool.Exec(ctx, `insert into app.closure_required(id,created_at,updated_at,value) values(gen_random_uuid(),now(),now(),`+tc.value+`)`); err != nil {
				t.Fatalf("%#v", err)
			}
			mode = ValidationModePocketBase
			id := created.Fields[0].ID
			change := NewUpdateChange(UpdateCollectionInput{Name: created.Name, ValidationMode: &mode, Fields: []FieldInput{{ID: &id, Name: "value", Type: tc.kind, Required: true}}})
			plan, err := service.Preview(ctx, change)
			if err != nil {
				t.Fatalf("%#v", err)
			}
			_, err = service.Apply(ctx, LegacyApply(change, plan, actor))
			if tc.wantError && !errors.Is(err, ErrDataConstraint) || !tc.wantError && err != nil {
				t.Fatalf("apply=%v", err)
			}
		})
	}
}

func TestClosureRulesOnlyImportRecordsMigration(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	ctx := context.Background()
	_ = applyFreshPlan(t, service, CreateCollectionInput{Name: "closure_rules", Fields: []FieldInput{{Name: "title", Type: TypeText}}}, actor)
	if _, err := pool.Exec(ctx, `create table public.n1_recorded_transactions(operation_id uuid primary key)`); err != nil {
		t.Fatalf("%#v", err)
	}
	service.SetMigrationRecorder(&transactionalMigrationRecorder{})
	doc, err := service.ExportSchema(ctx)
	if err != nil {
		t.Fatalf("%#v", err)
	}
	doc.AuthCollections = nil
	doc.Collections[0].UserAccess = !doc.Collections[0].UserAccess
	plan, err := service.PreviewSchemaImport(ctx, doc)
	if err != nil {
		t.Fatalf("%#v", err)
	}
	if len(plan.Changes) != 0 {
		t.Fatalf("expected rules only, got %d", len(plan.Changes))
	}
	if _, err = service.ApplySchemaImport(ctx, SchemaImportIntent{Document: doc, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "closure-rules-import", ValidateRules: acceptMigrationRulesForTest}); err != nil {
		t.Fatalf("%#v", err)
	}
	plan, err = service.PreviewSchemaImport(ctx, doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ApplySchemaImport(ctx, SchemaImportIntent{Document: doc, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "closure-rules-noop", ValidateRules: acceptMigrationRulesForTest}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `select count(*) from public.n1_recorded_transactions`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("recordings=%d err=%v", count, err)
	}
}
