//go:build integration

package schema

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"testing"
)

func TestInheritedViewDocumentPoliciesAreInertAndRoundtrip(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	ctx := context.Background()
	service.SetAuthExecutablePolicyValidator(authExecutableValidator{})
	source := applyFreshPlan(t, service, CreateCollectionInput{Name: "inherited_doc_source", Fields: []FieldInput{{Name: "title", Type: TypeText}}}, actor)
	view := applyFreshPlan(t, service, CreateCollectionInput{Name: "inherited_doc_view", View: &ViewInput{Source: source.Name, Projection: []string{"title"}}}, actor)
	sqlView := applyFreshPlan(t, service, CreateCollectionInput{Name: "inherited_doc_sql", View: &ViewInput{SQL: `SELECT id,title FROM app.inherited_doc_source`}}, actor)
	custom := json.RawMessage(`{"list":{"op":"allow"},"view":{"op":"allow"},"create":{"op":"allow"},"update":{"op":"allow"},"delete":{"op":"allow"}}`)
	for _, id := range []uuid.UUID{source.ID, view.ID, sqlView.ID} {
		if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.collection_access(collection_id,enabled) VALUES($1,true);`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.collection_rules(collection_id,rules) VALUES($1,$2)`, id, custom); err != nil {
			t.Fatal(err)
		}
	}
	exported, err := service.ExportSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range exported.Collections {
		if item.Name == view.Name {
			if item.UserAccess || validateDefaultOwnerRules(Catalog{}, Collection{}, item.Rules) != nil {
				t.Fatalf("ignored metadata exported: %+v", item)
			}
		} else if !item.UserAccess || string(item.Rules) == string(ownerRuleDocument()) {
			t.Fatalf("own policy lost: %+v", item)
		}
	}
	plan, err := service.PreviewSchemaImport(ctx, exported)
	if err != nil {
		t.Fatalf("export preview: %#v", err)
	}
	if _, err := service.ApplySchemaImport(ctx, SchemaImportIntent{Document: exported, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "inherited-doc-roundtrip-0001", ValidateRules: func(Catalog, Collection, json.RawMessage) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		gate  bool
		rules json.RawMessage
	}{{"gate", true, ownerRuleDocument()}, {"rules", false, custom}} {
		t.Run(tc.name, func(t *testing.T) {
			document := SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{{Name: view.Name, View: &ViewInput{Source: source.Name, Projection: []string{"title"}}, UserAccess: tc.gate, Rules: tc.rules}}}
			if _, err := service.PreviewSchemaImport(ctx, document); err == nil {
				t.Fatal("ignored override preview accepted")
			}
			if _, err := service.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "inherited-doc-override-" + tc.name, ValidateRules: func(Catalog, Collection, json.RawMessage) error { return nil }}); err == nil {
				t.Fatal("ignored override apply accepted")
			}
		})
	}
	_, target, targetActor := newSchemaServiceTest(t)
	target.SetAuthExecutablePolicyValidator(authExecutableValidator{})
	targetPlan, err := target.PreviewSchemaImport(ctx, exported)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ApplySchemaImport(ctx, SchemaImportIntent{Document: exported, BaseVersion: targetPlan.BaseVersion, Checksum: targetPlan.Checksum, Actor: targetActor, OperationID: uuid.New(), IdempotencyKey: "inherited-doc-target-roundtrip-0001", ValidateRules: func(Catalog, Collection, json.RawMessage) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	var ignoredRows int
	if err := target.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM pgbase_meta.collection_access WHERE collection_id=c.id)+(SELECT count(*) FROM pgbase_meta.collection_rules WHERE collection_id=c.id) FROM pgbase_meta.collections c WHERE name=$1`, view.Name).Scan(&ignoredRows); err != nil || ignoredRows != 0 {
		t.Fatalf("ignored rows=%d err=%v", ignoredRows, err)
	}
}
