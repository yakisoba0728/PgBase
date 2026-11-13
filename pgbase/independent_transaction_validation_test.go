//go:build integration

package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/jsvm"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type independentValidationHost struct {
	*extensionHost
	failures []error
}

func (h *independentValidationHost) Call(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	result, err := h.extensionHost.Call(ctx, method, raw)
	if err != nil && (method == "records.create" || method == "records.update") {
		h.failures = append(h.failures, err)
	}
	return result, err
}

func TestIndependentNativeTransactionValidatesSingleRecordFiles(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, nil)
	setupPublicUsersAdministrator(t, fixture)
	ctx := t.Context()
	var actor uuid.UUID
	if err := fixture.app.pool.QueryRow(ctx, `select id from pgbase_meta.admins where email=$1`, publicAdminEmail).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	definition := schema.CreateCollectionInput{Name: "independent_tx_files", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}, {Name: "attachment", Type: schema.TypeFile, Required: true}}}
	schemas := schema.NewService(fixture.app.pool, fixture.app.schemaCache, nil)
	plan, err := schemas.Preview(ctx, schema.NewCreateChange(definition))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(definition), plan, actor)); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if _, err := fixture.app.pool.Exec(ctx, `insert into app.independent_tx_files(id,title,created_at,updated_at) values($1,'before',current_timestamp,current_timestamp)`, id); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v2","routes":[{"method":"POST","pattern":"/independent-tx","access":"admin","handler":{"module":"route.js","export":"run"},"capabilities":["app-control","records"]}]}`, `
const pg=require('pgbase');exports.run=()=>pg.app.transaction(api=>{
 let createRejected=false,updateRejected=false;
 try{api.records.create({collection:'independent_tx_files',input:{title:'missing'}})}catch(e){createRejected=true}
 try{api.records.update({collection:'independent_tx_files',id:'`+id.String()+`',input:{title:'after'}})}catch(e){updateRejected=true}

 return {createRejected,updateRejected};
});`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: actor}, requestID: "independent-native-tx", valid: true, authorityContext: ctx}
	observed := &independentValidationHost{extensionHost: host}
	output, err := runtime.Invoke(ctx, jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Host: observed, Capabilities: []string{"app-control", "records"}})
	if err != nil {
		t.Fatalf("native transaction: output=%s, err=%v", output, err)
	}
	if len(observed.failures) != 2 {
		t.Fatalf("mutation failures=%v", observed.failures)
	}
	for _, failure := range observed.failures {
		var invalid *query.ValidationError
		if !errors.As(failure, &invalid) || len(invalid.Details) != 1 || invalid.Details[0].Field != "attachment" || invalid.Details[0].Code != "required_file" {
			t.Fatalf("wrong mutation failure: %#v", failure)
		}
	}
	var result map[string]bool
	if err := json.Unmarshal(output, &result); err != nil || !result["createRejected"] || !result["updateRejected"] {
		t.Fatalf("output=%s, err=%v", output, err)
	}
	var titles string
	if err := fixture.app.pool.QueryRow(ctx, `select string_agg(title,',') from app.independent_tx_files`).Scan(&titles); err != nil || strings.TrimSpace(titles) != "before" {
		t.Fatalf("failed operations committed: titles=%q, err=%v", titles, err)
	}
}
