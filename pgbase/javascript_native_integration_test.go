//go:build integration

package pgbase

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/jsvm"
)

func TestNativeJavaScriptTransactionIncludesBatchAndOwnReads(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, nil)
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	createPublicUsersCollection(t, fixture, cookie, csrf, "native_transaction_records")
	var actor uuid.UUID
	if err := fixture.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.admins where email=$1`, publicAdminEmail).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v2","routes":[{"method":"POST","pattern":"/native-tx","access":"admin","handler":{"module":"route.js","export":"run"},"capabilities":["app-control","records"]}]}`, `
const pg=require('pgbase');
exports.run=()=>{
 let rolledBack=false;
 try{pg.app.transaction(api=>{
   api.records.create({collection:'native_transaction_records',input:{title:'rollback create'}});
   api.records.batch({operations:[{operation:'create',collection:'native_transaction_records',input:{title:'rollback batch'}}]});
   throw new Error('rollback');
 })}catch(_){rolledBack=true}
 const visible=pg.app.transaction(api=>{
   api.records.create({collection:'native_transaction_records',input:{title:'committed'}});
   const page=api.records.list({collection:'native_transaction_records'});
   if(page.items.length!==1||page.items[0].title!=='committed')throw new Error('transaction read');
   return page.items.length;
 });
 return {rolledBack,visible,legacy:typeof $app};
};`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: actor}, requestID: "native-transaction", valid: true, authorityContext: t.Context()}
	output, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Host: host, Capabilities: []string{"app-control", "records"}})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		RolledBack bool   `json:"rolledBack"`
		Visible    int    `json:"visible"`
		Legacy     string `json:"legacy"`
	}
	if err := json.Unmarshal(output, &result); err != nil || !result.RolledBack || result.Visible != 1 || result.Legacy != "undefined" {
		t.Fatalf("native transaction = %s, %v", output, err)
	}
	var rows int
	if err := fixture.app.pool.QueryRow(t.Context(), `select count(*) from app.native_transaction_records`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("native transaction left partial writes: %d, %v", rows, err)
	}
}

func TestNativeJavaScriptSchemaPreviewAndApplyNormalizeTheSameRules(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, nil)
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	createPublicUsersCollection(t, fixture, cookie, csrf, "native_schema_records")
	var actor uuid.UUID
	if err := fixture.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.admins where email=$1`, publicAdminEmail).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v2","routes":[{"method":"POST","pattern":"/native-schema","access":"admin","handler":{"module":"route.js","export":"run"},"capabilities":["app-control"]}]}`, `
const pg=require('pgbase');exports.run=()=>{
 const document=pg.schema.export({});
 const collection=document.collections.find(item=>item.name==='native_schema_records');
 collection.rules.list={op:'expression',expression:"title = 'published'"};
 collection.rules.update={op:'owner'};
 const plan=pg.schema.preview({document});
 pg.schema.apply({document,baseVersion:plan.baseVersion,checksum:plan.checksum});
 const invalid=pg.schema.export({});
 invalid.collections.find(item=>item.name==='native_schema_records').rules.update={op:'expression',expression:'missing_column = true'};
 let rejected=false;try{pg.schema.preview({document:invalid})}catch(_){rejected=true}
 if(!rejected)throw new Error('invalid field accepted by preview');
 const related=pg.schema.export({});
 const source=related.collections.find(item=>item.name==='native_schema_records');
 source.fields.push({name:'owner',type:'relation',relation:{targetCollection:'native_schema_target',cardinality:'single'}});
 source.dependsOn=['native_schema_target'];
 source.rules.list={op:'expression',expression:'owner.enabled = true'};
 related.collections.push({name:'native_schema_target',fields:[{name:'enabled',type:'boolean',required:true}],userAccess:false,rules:{list:{op:'allow'},view:{op:'allow'},create:{op:'deny'},update:{op:'deny'},delete:{op:'deny'}}});
 const relationPlan=pg.schema.preview({document:related});
 pg.schema.apply({document:related,baseVersion:relationPlan.baseVersion,checksum:relationPlan.checksum});
 return {saved:true};
};`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: actor}, requestID: "native-schema-policy", valid: true, authorityContext: t.Context()}
	if output, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Host: host, Capabilities: []string{"app-control"}}); err != nil || string(output) != `{"saved":true}` {
		t.Fatalf("native schema apply = %s, %v", output, err)
	}
	var policy struct {
		List   struct{ Op, Binding, Expression string } `json:"list"`
		Update struct{ Op string }                      `json:"update"`
	}
	var raw []byte
	if err := fixture.app.pool.QueryRow(t.Context(), `select rules from pgbase_meta.collection_rules where collection_id=(select id from pgbase_meta.collections where name='native_schema_records')`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &policy); err != nil || policy.List.Op != "eq" || policy.List.Binding != "catalog" || policy.List.Expression != "owner.enabled = true" || policy.Update.Op != "owner" {
		t.Fatalf("native schema stored wrong policy: %s, %v", raw, err)
	}
}
