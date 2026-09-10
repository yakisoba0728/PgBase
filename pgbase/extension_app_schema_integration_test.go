//go:build integration

package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordops"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestExtensionAppSchemaAppliesIntrospectsTruncatesAndRollsBack(t *testing.T) {
	hooks := &HookRegistry{}
	_, err := hooks.On(HookCollectionCreate, Hook{Before: func(_ context.Context, event *HookEvent) error {
		if event.Data.(*schema.CollectionMutationEvent).Name == "bypass_items" {
			return errors.New("veto")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) { options.Hooks = hooks })
	_, _ = setupPublicUsersAdministrator(t, fixture)
	var actor uuid.UUID
	if err := fixture.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.admins where email=$1`, publicAdminEmail).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: actor}, requestID: "schema-test", valid: true}
	call := func(method string, input any) json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		out, err := host.callAppControl(t.Context(), method, raw)
		if err != nil {
			t.Fatalf("%s: %v: %v", method, err, errors.Unwrap(err))
		}
		return out
	}
	var document schema.SchemaDocument
	if err := json.Unmarshal(call("app-control.schemaExport", map[string]any{}), &document); err != nil {
		t.Fatal(err)
	}
	document.Collections = append(document.Collections, schema.SchemaDocumentCollection{Name: "js_schema_items", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText, Required: true}}, Rules: json.RawMessage(`{"list":{"op":"owner"},"view":{"op":"owner"},"create":{"op":"owner"},"update":{"op":"owner"},"delete":{"op":"owner"}}`)})
	var plan schema.SchemaImportPlan
	if err := json.Unmarshal(call("app-control.schemaPreview", map[string]any{"document": document}), &plan); err != nil {
		t.Fatal(err)
	}
	call("app-control.schemaApply", map[string]any{"document": document, "baseVersion": plan.BaseVersion, "checksum": plan.Checksum, "operationId": uuid.New(), "idempotencyKey": "js-schema-integration-0001"})
	columns := call("app-control.databaseColumns", map[string]any{"name": "js_schema_items"})
	if !containsJSONKeyInArray(columns, "title") {
		t.Fatalf("columns=%s", columns)
	}
	service, _ := fixture.app.schemaOperations.(*schema.Service)
	catalog, err := service.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	table := catalog.Collections["js_schema_items"].TableName
	if _, err := fixture.app.records.Create(t.Context(), recordops.CreateRequest{
		Principal: identity.Subject{Kind: identity.Administrator, ID: actor}, RequestID: "schema-create-0001", Collection: "js_schema_items",
		Input: map[string]json.RawMessage{"title": json.RawMessage(`"before"`)},
	}); err != nil {
		t.Fatal(err)
	}
	call("app-control.collectionTruncate", map[string]any{"name": "js_schema_items"})
	var count int
	if err := fixture.app.pool.QueryRow(t.Context(), "select count(*) from "+qualifiedAppIdentifier(table)).Scan(&count); err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	probe := "js_app_control_tx_probe"
	_, _ = fixture.app.pool.Exec(t.Context(), "drop table if exists "+pgx.Identifier{probe}.Sanitize())
	defer fixture.app.pool.Exec(t.Context(), "drop table if exists "+pgx.Identifier{probe}.Sanitize())
	_, err = host.callAppControl(t.Context(), "app-control.databaseTransaction", json.RawMessage(`{"statements":[{"sql":"create table js_app_control_tx_probe(id integer)"},{"sql":"insert into missing_table values(1)"}]}`))
	if err == nil {
		t.Fatal("failed transaction committed")
	}
	var exists bool
	if err := fixture.app.pool.QueryRow(t.Context(), `select to_regclass($1) is not null`, probe).Scan(&exists); err != nil || exists {
		t.Fatalf("probe exists=%v err=%v", exists, err)
	}
	denied := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.User, ID: uuid.New()}, valid: true}
	if _, err := denied.callAppControl(t.Context(), "app-control.schemaExport", json.RawMessage(`{}`)); err == nil {
		t.Fatal("user reached schema control")
	}
	bypass := schema.SchemaDocumentCollection{Name: "bypass_items", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}}, Rules: json.RawMessage(`{"list":{"op":"owner"},"view":{"op":"owner"},"create":{"op":"owner"},"update":{"op":"owner"},"delete":{"op":"owner"}}`)}
	raw, _ := json.Marshal(map[string]any{"collection": bypass})
	if _, err := host.callAppControl(t.Context(), "app-control.collectionSave", raw); err == nil {
		t.Fatal("ordinary save ignored collection hook veto")
	}
	if _, err := host.callAppControl(t.Context(), "app-control.collectionSaveNoValidate", raw); err != nil {
		t.Fatalf("hook-bypass save: %v: %v", err, errors.Unwrap(err))
	}
}

func containsJSONKeyInArray(raw json.RawMessage, name string) bool {
	var items []map[string]any
	if json.Unmarshal(raw, &items) != nil {
		return false
	}
	for _, item := range items {
		if item["name"] == name {
			return true
		}
	}
	return false
}
