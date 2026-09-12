//go:build integration

package pgbase

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestJavaScriptPocketBaseModelExecuteHookTagsAndVeto(t *testing.T) {
	directory := t.TempDir()
	manifest := `{"format":"pgbase.scripts/v1","bootstrap":"bootstrap.js","routes":[{"method":"POST","pattern":"/phase","access":"administrator","handler":{"module":"route.js","export":"run"},"capabilities":["records"]}],"hooks":[{"event":"record.model.create.execute","before":{"module":"route.js","export":"placeholder"}}]}`
	writeJavaScriptFixture(t, directory, manifest, `exports.placeholder=()=>({});exports.run=()=>{const ok=$app.save(new Record("phase_allowed",{title:"ok"}));let veto=false;try{$app.save(new Record("phase_blocked",{title:"blocked"}))}catch(_){veto=true}return {status:200,json:{ok:!!ok.data.id,veto}}}`)
	if err := os.WriteFile(filepath.Join(directory, "bootstrap.js"), []byte(`onRecordCreateExecute((e)=>{e.next();throw new Error("blocked by execute hook")},"phase_blocked")`), 0600); err != nil {
		t.Fatal(err)
	}
	fixture := newPublicUsersAppOptions(t, false, func(o *Options) {
		o.JavaScript = &JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"records": true}}
	})
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	createPublicUsersCollection(t, fixture, cookie, csrf, "phase_allowed")
	createPublicUsersCollection(t, fixture, cookie, csrf, "phase_blocked")
	response := publicUsersRequest(t, fixture, http.MethodPost, "/phase", map[string]any{}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, response, http.StatusOK)
	var result struct {
		OK   bool `json:"ok"`
		Veto bool `json:"veto"`
	}
	decodePublicUsersJSON(t, response, &result)
	if !result.OK || !result.Veto {
		t.Fatalf("result=%+v body=%s", result, response.Body.String())
	}
}

func TestJavaScriptPocketBaseAuthPasswordHookNextAndThrowVetoHTTP(t *testing.T) {
	directory := t.TempDir()
	manifest := `{"format":"pgbase.scripts/v1","bootstrap":"bootstrap.js","hooks":[{"event":"auth.password","before":{"module":"route.js","export":"run"}}]}`
	writeJavaScriptFixture(t, directory, manifest, `exports.run=()=>({})`)
	if err := os.WriteFile(filepath.Join(directory, "bootstrap.js"), []byte(`onRecordAuthWithPasswordRequest((e)=>{e.next();if(e.data.flow==="login")throw new Error("login veto")})`), 0600); err != nil {
		t.Fatal(err)
	}
	fixture := newPublicUsersAppOptions(t, true, func(o *Options) { o.JavaScript = &JavaScriptOptions{Directory: directory} })
	session := registerPublicUser(t, fixture, "js-auth-hook@example.test", publicUserOnePass)
	response := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/auth/login", map[string]any{"email": session.User.Email, "password": publicUserOnePass}, "", nil, nil)
	if response.Code < 400 {
		t.Fatalf("vetoed login status=%d body=%s", response.Code, response.Body.String())
	}
}
