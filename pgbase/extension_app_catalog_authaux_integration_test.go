//go:build integration

package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/jsvm"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestJavaScriptAppCatalogAndAuthAuxUseAdministratorPGFacade(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pgbase.scripts.json"), []byte(`{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/x","access":"administrator","handler":{"module":"r.js","export":"run"},"capabilities":["app-control"]}]}`), 0600)
	os.WriteFile(filepath.Join(dir, "r.js"), []byte(`exports.run=(q)=>{try{let c=$app.findCollectionByNameOrId(q.collection),a=$app.findAuthAuxiliary(q.realm,q.recordId,"externalAuths");if(q.deleteId)$app.deleteAuthAuxiliary(q.realm,q.recordId,"externalAuths",q.deleteId);return {name:c.name,count:1,unique:$app.isCollectionNameUnique(q.collection),aux:a.length}}catch(e){return {error:String(e.code||""),message:String(e.message||"")}}}`), 0600)
	fixture := newPublicUsersAppOptions(t, true, func(o *Options) {
		o.JavaScript = &JavaScriptOptions{Directory: dir, Capabilities: map[string]bool{"app-control": true}}
	})
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	realm := uuid.New()
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/auth-collections", map[string]any{"id": realm, "name": "aux_customers", "registrationEnabled": true, "profileSchema": map[string]string{}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), 201)
	registered := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/auth/aux_customers/register", map[string]any{"email": "aux@example.test", "password": publicUserOnePass}, "", nil, nil)
	assertPublicUsersStatus(t, registered, 201)
	var session publicUserSession
	decodePublicUsersJSON(t, registered, &session)
	first, second := uuid.New(), uuid.New()
	if _, e := fixture.app.pool.Exec(context.Background(), `insert into pgbase_meta.user_oauth_identities(id,auth_collection_id,provider,subject,user_id,created_at) values($1,$2,'one','one',$3,now()),($4,$2,'two','two',$3,now())`, first, realm, session.User.ID, second); e != nil {
		t.Fatal(e)
	}
	bundle, e := jsvm.LoadBundle(dir)
	if e != nil {
		t.Fatal(e)
	}
	runtime, e := jsvm.NewRuntime(bundle, jsvm.Limits{MaximumHostCalls: 16})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { runtime.Close(t.Context()) })
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: uuid.New()}, requestID: "catalog-aux", valid: true}
	invoke := func(deleteID string) (map[string]any, error) {
		in, _ := json.Marshal(map[string]string{"collection": "aux_customers", "realm": realm.String(), "recordId": session.User.ID.String(), "deleteId": deleteID})
		out, e := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Capabilities: []string{"app-control"}, Input: in, Host: host})
		var v map[string]any
		if e == nil {
			e = json.Unmarshal(out, &v)
		}
		return v, e
	}
	v, e := invoke("")
	if e != nil || v["name"] != "aux_customers" || v["aux"].(float64) != 2 || v["unique"] != false {
		t.Fatalf("catalog/aux=%v err=%v cause=%v", v, e, errors.Unwrap(e))
	}
	if _, e = invoke(first.String()); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = fixture.app.pool.QueryRow(t.Context(), `select count(*) from pgbase_meta.user_oauth_identities where user_id=$1`, session.User.ID).Scan(&n); e != nil || n != 1 {
		t.Fatalf("delete n=%d err=%v", n, e)
	}
	// The adapter reaches the native deletion path; password authentication remains an active sign-in method in this fixture.
	if _, e = invoke(second.String()); e != nil {
		t.Fatal(e)
	}
	wrong := &extensionHost{bridge: fixture.app.extensions, principal: host.principal, requestID: "wrong", valid: true}
	bad, _ := json.Marshal(map[string]string{"collection": "aux_customers", "recordId": session.User.ID.String(), "kind": "externalAuths"})
	if _, e = wrong.callAppCatalogAuthAux(t.Context(), "app.authAux.list", bad); e == nil {
		t.Fatal("missing realm accepted")
	}
	denied := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.User, ID: uuid.New()}, requestID: "user", valid: true}
	if _, e = denied.callAppCatalogAuthAux(t.Context(), "app.collection.list", json.RawMessage(`{}`)); e == nil {
		t.Fatal("user app-control accepted")
	}
}
