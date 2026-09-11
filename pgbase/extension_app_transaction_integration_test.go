//go:build integration

package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/jsvm"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestRecordHooksFollowExternalTransactionOutcomeExactlyOnce(t *testing.T) {
	var successes, failures atomic.Int32
	hooks := &HookRegistry{}
	_, _ = hooks.On("record.model.create.success", Hook{Before: func(context.Context, *HookEvent) error { successes.Add(1); return nil }})
	_, _ = hooks.On("record.model.create.error", Hook{Before: func(context.Context, *HookEvent) error { failures.Add(1); return nil }})
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) { options.Hooks = hooks })
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	createPublicUsersCollection(t, fixture, cookie, csrf, "external_tx_records")
	var actor uuid.UUID
	if err := fixture.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.admins where email=$1`, publicAdminEmail).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: actor}, requestID: "external-tx-rollback", valid: true, authorityContext: t.Context()}
	transaction := func(method string, token string) string {
		input := json.RawMessage(`{}`)
		if token != "" {
			input = json.RawMessage(`{"token":"` + token + `"}`)
		}
		output, err := host.Call(t.Context(), "app-control.database"+method, input)
		if err != nil {
			t.Fatalf("transaction %s: %v", method, err)
		}
		var response struct {
			Token string `json:"token"`
		}
		_ = json.Unmarshal(output, &response)
		return response.Token
	}
	create := func(title string) {
		input, _ := json.Marshal(extensionRecordRequest{Collection: "external_tx_records", Input: map[string]json.RawMessage{"title": json.RawMessage(`"` + title + `"`)}})
		if _, err := host.Call(t.Context(), "records.create", input); err != nil {
			t.Fatalf("create %q: %v", title, err)
		}
	}
	token := transaction("Begin", "")
	create("rolled back")
	if successes.Load() != 0 || failures.Load() != 0 {
		t.Fatalf("hooks ran before rollback: success=%d error=%d", successes.Load(), failures.Load())
	}
	transaction("Rollback", token)
	host.requestID = "external-tx-commit"
	token = transaction("Begin", "")
	create("committed")
	transaction("Commit", token)
	var rows int
	if err := fixture.app.pool.QueryRow(t.Context(), `select count(*) from app.external_tx_records`).Scan(&rows); err != nil || rows != 1 || successes.Load() != 1 || failures.Load() != 1 {
		t.Fatalf("outcomes rows=%d success=%d error=%d err=%v", rows, successes.Load(), failures.Load(), err)
	}
}

func TestPocketBaseRunInTransactionUsesInvocationBoundPostgresTransaction(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, nil)
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	createPublicUsersCollection(t, fixture, cookie, csrf, "js_tx_records")
	var actor uuid.UUID
	if err := fixture.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.admins where email=$1`, publicAdminEmail).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/tx","access":"admin","handler":{"module":"route.js","export":"run"},"capabilities":["app-control","records"]}]}`, `exports.run=()=>{let inside=false,rolled=false,nested=false;try{$app.runInTransaction((tx)=>{inside=$app.isTransactional();tx.db().exec("create table app.js_tx_rollback(id integer)");tx.save(new Record("js_tx_records",{title:"rolled"}));throw new Error("rollback")})}catch(_){rolled=true}$app.runInTransaction((tx)=>{tx.db().exec("create table app.js_tx_commit(id integer)");tx.db().exec("insert into app.js_tx_commit values($1)",[7]);tx.save(new Record("js_tx_records",{title:"committed"}));tx.runInTransaction(inner=>{nested=inner===tx});const rows=tx.db().query("select id from app.js_tx_commit").items;if(rows.length!==1)throw new Error("query")});return {status:200,json:{inside,rolled,nested,outside:$app.isTransactional()}}}`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: actor}, requestID: "tx-js-request", valid: true, authorityContext: t.Context()}
	out, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Host: host, Capabilities: []string{"app-control", "records"}})
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
	var recordCount int
	if err := fixture.app.pool.QueryRow(t.Context(), `select count(*) from app.js_tx_records`).Scan(&recordCount); err != nil || recordCount != 1 {
		t.Fatalf("transaction model rows=%d err=%v", recordCount, err)
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
