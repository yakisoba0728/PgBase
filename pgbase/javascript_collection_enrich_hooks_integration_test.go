//go:build integration

package pgbase

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestJavaScriptCollectionRequestHooksVetoAndFilterByCollection(t *testing.T) {
	directory := t.TempDir()
	manifest := `{"format":"pgbase.scripts/v1","bootstrap":"bootstrap.js","routes":[{"method":"GET","pattern":"/phases","access":"administrator","handler":{"module":"route.js","export":"phases"},"capabilities":["app-control"]}],"hooks":[
	 {"event":"collection.request.list","before":{"module":"route.js","export":"x"},"after":{"module":"route.js","export":"x"},"capabilities":["app-control"]},
	 {"event":"collection.request.view","before":{"module":"route.js","export":"x"},"after":{"module":"route.js","export":"x"},"capabilities":["app-control"]},
	 {"event":"collection.request.create","before":{"module":"route.js","export":"x"},"after":{"module":"route.js","export":"x"},"capabilities":["app-control"]},
	 {"event":"collection.request.update","before":{"module":"route.js","export":"x"},"after":{"module":"route.js","export":"x"},"capabilities":["app-control"]},
	 {"event":"collection.request.delete","before":{"module":"route.js","export":"x"},"after":{"module":"route.js","export":"x"},"capabilities":["app-control"]}]}`
	bootstrap := `const install=(register,tag,veto)=>{register((e)=>{e.next();if(e.phase==="before"&&veto)throw new Error("request veto")},...(tag?[tag]:[]));register((e)=>{$app.store().set("phase-"+e.data.operation,{failed:e.failed,state:e.state||null})},...(tag?[tag]:[]))};
	install(onCollectionsListRequest,"",true);install(onCollectionViewRequest,"blocked_view",true);install(onCollectionCreateRequest,"blocked_create",true);install(onCollectionUpdateRequest,"blocked_update",true);install(onCollectionDeleteRequest,"blocked_delete",true);`
	writeJavaScriptFixture(t, directory, manifest, `exports.x=()=>({});exports.phases=()=>({status:200,json:{create:$app.store().get("phase-create"),view:$app.store().get("phase-view"),update:$app.store().get("phase-update"),delete:$app.store().get("phase-delete"),list:$app.store().get("phase-list")}})`)
	if err := os.WriteFile(filepath.Join(directory, "bootstrap.js"), []byte(bootstrap), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newPublicUsersAppOptions(t, false, func(o *Options) {
		o.JavaScript = &JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"app-control": true}}
	})
	admin, csrf := setupPublicUsersAdministrator(t, fixture)
	createPublicUsersCollection(t, fixture, admin, csrf, "allowed_collection")
	if got := publicUsersRequest(t, fixture, http.MethodGet, "/api/admin/collections/allowed_collection", nil, "", admin, nil); got.Code != http.StatusOK {
		t.Fatalf("tag filter rejected allowed view: %d %s", got.Code, got.Body.String())
	}
	for _, name := range []string{"blocked_view", "blocked_update", "blocked_delete"} {
		createPublicUsersCollection(t, fixture, admin, csrf, name)
	}
	if got := publicUsersRequest(t, fixture, http.MethodGet, "/api/admin/collections/blocked_view", nil, "", admin, nil); got.Code < 400 {
		t.Fatal("view veto was bypassed")
	}
	if got := publicUsersRequest(t, fixture, http.MethodGet, "/api/admin/collections", nil, "", admin, nil); got.Code < 400 {
		t.Fatal("list veto was bypassed")
	}
	applyHookCollectionChange(t, fixture, admin, csrf, map[string]any{"operation": "create", "name": "blocked_create", "fields": []map[string]any{{"name": "title", "type": "text"}}}, true)
	applyHookCollectionChange(t, fixture, admin, csrf, map[string]any{"operation": "update", "name": "blocked_update", "fields": []map[string]any{{"name": "title", "type": "text", "required": true}}}, true)
	applyHookCollectionChange(t, fixture, admin, csrf, map[string]any{"operation": "delete", "name": "blocked_delete"}, true)
	phase := publicUsersRequest(t, fixture, http.MethodGet, "/phases", nil, "", admin, nil)
	if phase.Code != http.StatusOK {
		t.Fatalf("phase observations unavailable: %d %s", phase.Code, phase.Body.String())
	}
	var observed map[string]struct {
		Failed bool `json:"failed"`
	}
	decodePublicUsersJSON(t, phase, &observed)
	for _, operation := range []string{"create", "view", "update", "delete", "list"} {
		value, exists := observed[operation]
		if !exists || !value.Failed {
			t.Fatalf("%s after hook did not observe the veto: %#v", operation, observed)
		}
	}
}

func applyHookCollectionChange(t *testing.T, fixture *publicUsersFixture, admin *http.Cookie, csrf string, change map[string]any, wantFailure bool) {
	t.Helper()
	plan := publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/collections/plan", change, "", admin, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, plan, http.StatusOK)
	var p struct {
		BaseVersion int64  `json:"baseVersion"`
		Checksum    string `json:"checksum"`
	}
	decodePublicUsersJSON(t, plan, &p)
	change["baseVersion"], change["checksum"] = p.BaseVersion, p.Checksum
	got := publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/collections/apply", change, "", admin, map[string]string{"X-PgBase-CSRF": csrf})
	if wantFailure && got.Code < 400 {
		t.Fatalf("%s veto bypassed: %d %s", change["operation"], got.Code, got.Body.String())
	}
}

func TestJavaScriptRecordEnrichChangesVisibleFieldsWithoutAddingHiddenData(t *testing.T) {
	directory := t.TempDir()
	manifest := `{"format":"pgbase.scripts/v1","bootstrap":"bootstrap.js","hooks":[{"event":"record.enrich","before":{"module":"route.js","export":"x"}}]}`
	writeJavaScriptFixture(t, directory, manifest, `exports.x=()=>({})`)
	if err := os.WriteFile(filepath.Join(directory, "bootstrap.js"), []byte(`onRecordEnrich((e)=>{e.next();return {patch:{record:{title:"enriched",secret:"must-not-appear"}}}},"enrich_notes")`), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newPublicUsersAppOptions(t, true, func(o *Options) { o.JavaScript = &JavaScriptOptions{Directory: directory} })
	admin, csrf := setupPublicUsersAdministrator(t, fixture)
	applyHookCollectionChange(t, fixture, admin, csrf, map[string]any{"operation": "create", "name": "enrich_notes", "fields": []map[string]any{
		{"name": "title", "type": "text", "required": true},
		{"name": "secret", "type": "text", "hidden": true, "default": "server-secret"},
	}}, false)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodPut, "/api/admin/collections/enrich_notes/user-access", map[string]any{"enabled": true}, "", admin, map[string]string{"X-PgBase-CSRF": csrf}), http.StatusOK)
	user := registerPublicUser(t, fixture, "enrich-hook@example.test", publicUserOnePass)
	created := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/user/collections/enrich_notes/records", map[string]any{"title": "original"}, user.Token, nil, map[string]string{"Idempotency-Key": "enrich-hook-record-1"})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	var record map[string]any
	decodePublicUsersJSON(t, created, &record)
	if record["title"] != "enriched" {
		t.Fatalf("enrich patch missing: %#v", record)
	}
	if _, exists := record["secret"]; exists {
		t.Fatalf("enrich added non-visible field: %#v", record)
	}
}
