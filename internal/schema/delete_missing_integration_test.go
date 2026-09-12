//go:build integration

package schema

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestSchemaImportDeleteMissingReportsProtectedSystemCollection(t *testing.T) {
	_, service, admin := newSchemaServiceTest(t)
	applyChange(t, service, NewCreateChange(CreateCollectionInput{Name: "delete_missing_system", System: true}), admin)
	document, err := service.ExportSchema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	document.DeleteMissing = true
	document.Collections = nil
	_, err = service.PreviewSchemaImport(context.Background(), document)
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || len(validationErr.Details) != 1 || validationErr.Details[0].Code != "system_collection_protected" {
		t.Fatalf("protected preview error = %#v", err)
	}
}

func TestSchemaImportDeleteMissingReportsProtectedAuthRealm(t *testing.T) {
	_, service, admin := newSchemaServiceTest(t)
	rules := json.RawMessage(`{"list":null,"view":null,"create":null,"update":null,"delete":null,"auth":null,"manage":null}`)
	document := SchemaDocument{Format: SchemaDocumentFormat, AuthCollections: []SchemaDocumentAuthCollection{{
		Name: "protected_realm", Rules: rules,
		Policy: SchemaDocumentAuthPolicy{PasswordEnabled: true, MinimumPasswordBytes: 12, MaximumPasswordBytes: 128, IdentityFields: []string{"email"}, MFADurationSeconds: 300, OTPDurationSeconds: 300, OTPMaxAttempts: 5},
	}}}
	plan, err := service.PreviewSchemaImport(context.Background(), document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplySchemaImport(context.Background(), SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: admin, OperationID: uuid.New(), IdempotencyKey: "protected-auth-realm-0001"}); err != nil {
		t.Fatal(err)
	}
	_, err = service.PreviewSchemaImport(context.Background(), SchemaDocument{Format: SchemaDocumentFormat, DeleteMissing: true})
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || len(validationErr.Details) != 1 || validationErr.Details[0].Code != "auth_realm_protected" {
		t.Fatalf("protected realm preview error = %#v", err)
	}
}

func TestSchemaImportDeleteMissingDeletesDependenciesInOneSealedApply(t *testing.T) {
	_, service, admin := newSchemaServiceTest(t)
	applyChange(t, service, NewCreateChange(CreateCollectionInput{Name: "delete_missing_target"}), admin)
	applyChange(t, service, NewCreateChange(CreateCollectionInput{Name: "delete_missing_source", Fields: []FieldInput{{Name: "label", Type: TypeText}, {Name: "target", Type: TypeRelation, Relation: &RelationInput{TargetCollection: "delete_missing_target", Cardinality: RelationSingle}}}}), admin)
	applyChange(t, service, NewCreateChange(CreateCollectionInput{Name: "delete_missing_view", Type: CollectionTypeView, View: &ViewInput{Source: "delete_missing_source", Projection: []string{"label"}}}), admin)
	applyChange(t, service, NewCreateChange(CreateCollectionInput{Name: "delete_missing_kept"}), admin)

	document, err := service.ExportSchema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	document.DeleteMissing = true
	for i := 0; i < len(document.Collections); {
		if document.Collections[i].Name == "delete_missing_kept" {
			i++
		} else {
			document.Collections = append(document.Collections[:i], document.Collections[i+1:]...)
		}
	}
	plan, err := service.PreviewSchemaImport(context.Background(), document)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 3 || plan.Changes[0].Definition.Name != "delete_missing_view" || plan.Changes[2].Definition.Name != "delete_missing_target" {
		t.Fatalf("dependency delete plans = %#v", plan.Changes)
	}
	result, err := service.ApplySchemaImport(context.Background(), SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: admin, OperationID: uuid.New(), IdempotencyKey: "delete-missing-integration-0001"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Applied != 3 {
		t.Fatalf("result = %#v", result)
	}
	catalog, err := service.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Collections) != 1 || catalog.Collections["delete_missing_kept"].Name == "" {
		t.Fatalf("catalog after deleteMissing = %#v", catalog.Collections)
	}
}

func TestSchemaImportDeleteMissingVetoRollsBackEarlierDeletes(t *testing.T) {
	_, service, admin := newSchemaServiceTest(t)
	for _, name := range []string{"delete_missing_drop_a", "delete_missing_drop_b", "delete_missing_keep"} {
		applyChange(t, service, NewCreateChange(CreateCollectionInput{Name: name}), admin)
	}
	document, err := service.ExportSchema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	document.DeleteMissing = true
	for i := 0; i < len(document.Collections); {
		if document.Collections[i].Name == "delete_missing_keep" {
			i++
		} else {
			document.Collections = append(document.Collections[:i], document.Collections[i+1:]...)
		}
	}
	plan, err := service.PreviewSchemaImport(context.Background(), document)
	if err != nil {
		t.Fatal(err)
	}
	service.SetCollectionMutationLifecycle(&selectiveCollectionLifecycle{vetoName: "delete_missing_drop_b"})
	_, err = service.ApplySchemaImport(context.Background(), SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: admin, OperationID: uuid.New(), IdempotencyKey: "delete-missing-veto-0001"})
	if err == nil {
		t.Fatal("vetoed deleteMissing applied")
	}
	catalog, err := service.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"delete_missing_drop_a", "delete_missing_drop_b", "delete_missing_keep"} {
		if catalog.Collections[name].Name == "" {
			t.Fatalf("rollback lost %s", name)
		}
	}
}
