//go:build integration

package pgbase

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestJavaScriptRecordBoundaryAcceptsExactTextIdentities(t *testing.T) {
	directory := t.TempDir()
	files := map[string]string{
		"pgbase.scripts.json": `{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/text-identities","access":"administrator","handler":{"module":"route.js","export":"run"},"capabilities":["records"]}]}`,
		"route.js": `const pg=require("pgbase");exports.run=()=>{
const first="MiXeDKey01",second="BatchKey02";
const created=pg.records.create({collection:"js_text_identities",input:{id:first,title:"created"}});
const read=pg.records.get({collection:"js_text_identities",id:first});
const valid=$app.validate(new Record("js_text_identities",{id:first,title:"valid"})).valid;
const updated=pg.records.update({collection:"js_text_identities",id:first,input:{title:"updated"}});
const batch=pg.records.batch({operations:[
 {operation:"update",collection:"js_text_identities",id:first,input:{title:"batched"}},
 {operation:"create",collection:"js_text_identities",id:second,input:{title:"second"}}
]});
pg.records.delete({collection:"js_text_identities",id:first});
const survivor=pg.records.get({collection:"js_text_identities",id:second});
return {status:200,json:{created:created.data.id,read:read.data.id,valid,updated:updated.data.id,batch:batch.results.map(v=>v.data.id),survivor:survivor.data.id}};
};`,
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.JavaScript = &JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"records": true}}
	})
	adminCookie, csrf := setupPublicUsersAdministrator(t, fixture)
	var actor uuid.UUID
	if err := fixture.app.pool.QueryRow(context.Background(), `select id from pgbase_meta.admins where email=$1`, publicAdminEmail).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	applyN6SchemaChange(t, fixture.app.schemaOperations.(*schema.Service), actor, schema.NewCreateChange(schema.CreateCollectionInput{
		Name: "js_text_identities",
		RecordID: &schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 40,
			Pattern: `^[A-Za-z0-9]+$`},
		Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText, Required: true}},
	}))

	response := publicUsersRequest(t, fixture, http.MethodPost, "/text-identities", nil, "", adminCookie,
		map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, response, http.StatusOK)
	var got struct {
		Created  string   `json:"created"`
		Read     string   `json:"read"`
		Valid    bool     `json:"valid"`
		Updated  string   `json:"updated"`
		Batch    []string `json:"batch"`
		Survivor string   `json:"survivor"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Created != "MiXeDKey01" || got.Read != got.Created || !got.Valid || got.Updated != got.Created ||
		len(got.Batch) != 2 || got.Batch[0] != got.Created || got.Batch[1] != "BatchKey02" || got.Survivor != "BatchKey02" {
		t.Fatalf("JavaScript text identity result = %#v", got)
	}
	var rows, identities int
	if err := fixture.app.pool.QueryRow(context.Background(), `select count(*) from app.js_text_identities`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := fixture.app.pool.QueryRow(context.Background(), `select count(*) from pgbase_meta.record_identities where collection_kind='base' and external_id in ('MiXeDKey01','BatchKey02')`).Scan(&identities); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || identities != 1 {
		t.Fatalf("rows=%d identities=%d, want 1/1 after exact-id delete", rows, identities)
	}
}
