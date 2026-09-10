//go:build integration

package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/jsvm"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestPocketBaseRunInTransactionUsesInvocationBoundPostgresTransaction(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, nil)
	_, _ = setupPublicUsersAdministrator(t, fixture)
	var actor uuid.UUID
	if err := fixture.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.admins where email=$1`, publicAdminEmail).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/tx","access":"admin","handler":{"module":"route.js","export":"run"},"capabilities":["app-control"]}]}`, `exports.run=()=>{let inside=false,rolled=false;try{$app.runInTransaction((tx)=>{inside=$app.isTransactional();tx.exec("create table app.js_tx_rollback(id integer)");throw new Error("rollback")})}catch(_){rolled=true}$app.runInTransaction((tx)=>{tx.exec("create table app.js_tx_commit(id integer)");tx.exec("insert into app.js_tx_commit values($1)",[7]);const rows=tx.query("select id from app.js_tx_commit").items;if(rows.length!==1)throw new Error("query")});return {status:200,json:{inside,rolled,outside:$app.isTransactional()}}}`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: actor}, requestID: "tx-js", valid: true, authorityContext: t.Context()}
	out, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Host: host, Capabilities: []string{"app-control"}})
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(out, &response); err != nil {
		t.Fatal(err)
	}
	if string(out) == "" {
		t.Fatal("empty response")
	}
	defer fixture.app.pool.Exec(t.Context(), `drop table if exists app.js_tx_commit`)
	var rolledBack, committed bool
	if err := fixture.app.pool.QueryRow(t.Context(), `select to_regclass('app.js_tx_rollback') is null, to_regclass('app.js_tx_commit') is not null`).Scan(&rolledBack, &committed); err != nil || !rolledBack || !committed {
		t.Fatalf("rolledBack=%v committed=%v err=%v out=%s", rolledBack, committed, err, out)
	}
}

func TestPocketBaseDatabaseMetadataAndViewAdapters(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, nil)
	_, _ = setupPublicUsersAdministrator(t, fixture)
	var actor uuid.UUID
	if err := fixture.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.admins where email=$1`, publicAdminEmail).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: actor}, requestID: "metadata-js", valid: true, authorityContext: t.Context()}
	call := func(method string, value any) json.RawMessage {
		raw, _ := json.Marshal(value)
		out, err := host.callAppControl(t.Context(), method, raw)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		return out
	}
	defer fixture.app.pool.Exec(t.Context(), `drop view if exists app.js_view_probe`)
	call("app-control.databaseSaveView", map[string]any{"name": "js_view_probe", "query": "select '00000000-0000-0000-0000-000000000001'::uuid id, 'ok'::text title"})
	var exists bool
	if err := json.Unmarshal(call("app-control.databaseHasTable", map[string]any{"name": "JS_VIEW_PROBE"}), &exists); err != nil || !exists {
		t.Fatalf("exists=%v err=%v", exists, err)
	}
	var fields []map[string]any
	if err := json.Unmarshal(call("app-control.databaseCreateViewFields", map[string]any{"name": "probe", "query": "select * from app.js_view_probe"}), &fields); err != nil || len(fields) != 2 {
		t.Fatalf("fields=%v err=%v", fields, err)
	}
	var sample struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(call("app-control.databaseDryRunView", map[string]any{"name": "probe", "query": "select * from app.js_view_probe", "sampleSize": 1}), &sample); err != nil || len(sample.Items) != 1 {
		t.Fatalf("sample=%v err=%v", sample.Items, err)
	}
	call("app-control.databaseDeleteView", map[string]any{"name": "js_view_probe"})
}

func TestPocketBaseImportCollectionsAppliesSchema(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, nil)
	_, _ = setupPublicUsersAdministrator(t, fixture)
	var actor uuid.UUID
	if err := fixture.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.admins where email=$1`, publicAdminEmail).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/import","access":"admin","handler":{"module":"route.js","export":"run"},"capabilities":["app-control"]}]}`, `exports.run=()=>{const c=new Collection({name:"js_import_actual",fields:[new TextField({name:"title"})],rules:{list:{op:"owner"},view:{op:"owner"},create:{op:"owner"},update:{op:"owner"},delete:{op:"owner"}}});$app.importCollectionsByMarshaledJSON(JSON.stringify([c.export()]));return {status:200,json:{exists:$app.hasTable("js_import_actual")}}}`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: actor}, requestID: "import-js", valid: true, authorityContext: t.Context()}
	if _, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Host: host, Capabilities: []string{"app-control"}}); err != nil {
		t.Fatalf("%v: %v", err, errors.Unwrap(err))
	}
	service, _ := fixture.app.schemaOperations.(*schema.Service)
	catalog, err := service.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := catalog.Collections["js_import_actual"]; !ok {
		t.Fatal("importCollections only previewed schema")
	}
}
