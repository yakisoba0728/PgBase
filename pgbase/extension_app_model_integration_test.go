//go:build integration

package pgbase

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/jsvm"
	"github.com/yakisoba0728/PgBase/internal/recordops"
)

func TestJavaScriptAppModelFamilyUsesValidatedRecordStorage(t *testing.T) {
	fixture := newPublicUsersApp(t, false)
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	createPublicUsersCollection(t, fixture, cookie, csrf, "model_notes")
	directory := t.TempDir()
	files := map[string]string{
		"pgbase.scripts.json": `{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/models","access":"admin","handler":{"module":"route.js","export":"run"},"capabilities":["records"]}]}`,
		"route.js":            `exports.run=()=>{const bad=new Record("model_notes",{title:42});let rejected=false;try{$app.validate(bad)}catch(_){rejected=true}const r=new Record("model_notes",{title:"saved"});const saved=$app.saveWithContext({},r),items=$app.auxModelQuery(new Model({table:"model_notes"})).where("title = 'saved'").all().items;$app.auxDeleteWithContext({},new Record("model_notes",{id:saved.data.id}));return {rejected,count:items.length,id:saved.data.id}}`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(t.Context()) })
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: uuid.New()}, requestID: "model-family", valid: true}
	output, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Capabilities: []string{"records"}, Host: host})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Rejected bool   `json:"rejected"`
		Count    int    `json:"count"`
		ID       string `json:"id"`
	}
	if err := json.Unmarshal(output, &got); err != nil || !got.Rejected || got.Count != 1 || got.ID == "" {
		t.Fatalf("output=%s err=%v", output, err)
	}
	if _, err := fixture.app.extensions.records.Get(t.Context(), recordops.GetRequest{Principal: identity.Subject{Kind: identity.Administrator, ID: uuid.New()}, RequestID: "model-after-delete", Collection: "model_notes", ID: uuid.MustParse(got.ID)}); err == nil {
		t.Fatal("deleted model remained readable")
	}
}

func TestAppModelNoValidateRequiresAdministrator(t *testing.T) {
	fixture := newPublicUsersApp(t, false)
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.User, ID: uuid.New()}, requestID: "model-denied", valid: true}
	if _, err := host.Call(t.Context(), "hooks-bypass.modelSaveNoValidate", json.RawMessage(`{"collection":"missing","input":{}}`)); err == nil {
		t.Fatal("non-administrator used model validation bypass")
	}
}
