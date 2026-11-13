//go:build integration

package schema

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestSchemaImportRejectsCustomPoliciesWithoutValidatorBeforeApplyingChanges(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	ctx := t.Context()
	before, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	document := SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{
		{Name: "nil_validator_first", Rules: ownerRuleDocument()},
		{Name: "nil_validator_second", Rules: ownerRuleDocument()},
	}}
	for _, raw := range []string{
		`{"list":{"op":"unknown"},"view":{"op":"owner"},"create":{"op":"owner"},"update":{"op":"owner"},"delete":{"op":"owner"}}`,
		`{"list":{"op":"eq","field":"missing","value":true},"view":{"op":"owner"},"create":{"op":"owner"},"update":{"op":"owner"},"delete":{"op":"owner"}}`,
	} {
		document.Collections[1].Rules = json.RawMessage(raw)
		plan, err := service.PreviewSchemaImport(ctx, document)
		if err != nil {
			t.Fatal(err)
		}
		op := uuid.New()
		_, err = service.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: op, IdempotencyKey: "nil-validator-" + op.String()})
		if err == nil {
			t.Fatal("unvalidated custom policy applied")
		}
		after, err := service.Catalog(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if after.Version != before.Version || len(after.Collections) != len(before.Collections) {
			t.Fatal("rejected policy partially applied schema")
		}
		var claimed bool
		if err := pool.QueryRow(ctx, `select exists(select 1 from pgbase_meta.schema_import_operations where operation_id=$1)`, op).Scan(&claimed); err != nil {
			t.Fatal(err)
		}
		if claimed {
			t.Fatal("unvalidated policy reached operation claim")
		}
	}
	document.Collections[1].Rules = ownerRuleDocument()
	plan, err := service.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "default-owner-no-validator-0001"})
	if err != nil {
		t.Fatalf("schema-only default owner import rejected: %v", err)
	}
}
