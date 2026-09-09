//go:build integration

package pgbase

import (
	"net/http"
	"testing"
)

func TestHiddenFieldHTTPContract(t *testing.T) {
	app := newPublicUsersApp(t, true)
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	change := map[string]any{"operation": "create", "name": "hidden_notes", "fields": []map[string]any{
		{"name": "title", "type": "text", "required": true, "presentable": true},
		{"name": "secret", "type": "text", "hidden": true, "default": "server-secret", "help": "Administrators only."},
	}}
	preview := publicUsersRequest(t, app, http.MethodPost, "/api/admin/collections/plan", change, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, preview, http.StatusOK)
	var plan struct {
		BaseVersion int64  `json:"baseVersion"`
		Checksum    string `json:"checksum"`
	}
	decodePublicUsersJSON(t, preview, &plan)
	change["baseVersion"], change["checksum"] = plan.BaseVersion, plan.Checksum
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPost, "/api/admin/collections/apply", change, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), http.StatusCreated)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPut, "/api/admin/collections/hidden_notes/user-access", map[string]any{"enabled": true}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), http.StatusOK)
	user := registerPublicUser(t, app, "hidden-user@example.test", publicUserOnePass)
	base := "/api/v1/user/collections/hidden_notes/records"
	denied := publicUsersRequest(t, app, http.MethodPost, base, map[string]any{"title": "bad", "secret": "override"}, user.Token, nil, map[string]string{"Idempotency-Key": "hidden-denied-0001"})
	assertPublicUsersStatus(t, denied, http.StatusUnprocessableEntity)
	created := publicUsersRequest(t, app, http.MethodPost, base, map[string]any{"title": "visible"}, user.Token, nil, map[string]string{"Idempotency-Key": "hidden-create-0001"})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	var record map[string]any
	decodePublicUsersJSON(t, created, &record)
	if _, ok := record["secret"]; ok {
		t.Fatalf("create leaked hidden field: %#v", record)
	}
	id := record["id"].(string)
	get := publicUsersRequest(t, app, http.MethodGet, base+"/"+id, nil, user.Token, nil, nil)
	assertPublicUsersStatus(t, get, http.StatusOK)
	decodePublicUsersJSON(t, get, &record)
	if _, ok := record["secret"]; ok {
		t.Fatalf("get leaked hidden field: %#v", record)
	}
	list := publicUsersRequest(t, app, http.MethodGet, base, nil, user.Token, nil, nil)
	assertPublicUsersStatus(t, list, http.StatusOK)
	var page struct {
		Items []map[string]any `json:"items"`
	}
	decodePublicUsersJSON(t, list, &page)
	if len(page.Items) != 1 {
		t.Fatalf("list=%#v", page.Items)
	}
	if _, ok := page.Items[0]["secret"]; ok {
		t.Fatalf("list leaked hidden: %#v", page.Items)
	}
	updated := publicUsersRequest(t, app, http.MethodPatch, base+"/"+id, map[string]any{"title": "updated"}, user.Token, nil, map[string]string{"If-Match": get.Header().Get("ETag")})
	assertPublicUsersStatus(t, updated, http.StatusOK)
	decodePublicUsersJSON(t, updated, &record)
	if _, ok := record["secret"]; ok {
		t.Fatalf("update leaked hidden: %#v", record)
	}
	batch := publicUsersRequest(t, app, http.MethodPost, "/api/v1/user/collections/batch", map[string]any{"requests": []map[string]any{{"method": "POST", "url": "/api/v1/collections/hidden_notes/records", "headers": map[string]string{"Idempotency-Key": "hidden-batch-0001"}, "body": map[string]any{"title": "batch"}}}}, user.Token, nil, nil)
	assertPublicUsersStatus(t, batch, http.StatusOK)
	var batchOut struct {
		Results []struct {
			Body map[string]any `json:"body"`
		} `json:"results"`
	}
	decodePublicUsersJSON(t, batch, &batchOut)
	if len(batchOut.Results) != 1 {
		t.Fatalf("batch=%#v", batchOut)
	}
	if _, ok := batchOut.Results[0].Body["secret"]; ok {
		t.Fatalf("batch leaked hidden: %#v", batchOut)
	}
	filtered := publicUsersRequest(t, app, http.MethodGet, base+"?filter=secret%3Alower%20%3D%20%27server-secret%27", nil, user.Token, nil, nil)
	assertPublicUsersStatus(t, filtered, http.StatusUnprocessableEntity)
	admin := publicUsersRequest(t, app, http.MethodGet, "/api/v1/collections/hidden_notes/records/"+id, nil, "", cookie, nil)
	assertPublicUsersStatus(t, admin, http.StatusOK)
	decodePublicUsersJSON(t, admin, &record)
	if record["secret"] != "server-secret" {
		t.Fatalf("admin hidden value=%#v", record)
	}

	applyFieldVisibilityCollection(t, app, cookie, csrf, map[string]any{"operation": "create", "name": "hidden_targets", "fields": []map[string]any{{"name": "name", "type": "text", "required": true}, {"name": "secret", "type": "text", "hidden": true, "default": "target-secret"}}})
	applyFieldVisibilityCollection(t, app, cookie, csrf, map[string]any{"operation": "create", "name": "hidden_sources", "fields": []map[string]any{{"name": "title", "type": "text"}, {"name": "owner", "type": "relation", "indexed": true, "relation": map[string]any{"targetCollection": "hidden_targets", "cardinality": "single", "minSelect": 0, "maxSelect": 1, "onDelete": "restrict"}}, {"name": "concealed", "type": "relation", "indexed": true, "hidden": true, "relation": map[string]any{"targetCollection": "hidden_targets", "cardinality": "single", "minSelect": 0, "maxSelect": 1, "onDelete": "restrict"}}}})
	for _, name := range []string{"hidden_targets", "hidden_sources"} {
		assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPut, "/api/admin/collections/"+name+"/user-access", map[string]any{"enabled": true}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), http.StatusOK)
	}
	targetResponse := publicUsersRequest(t, app, http.MethodPost, "/api/v1/user/collections/hidden_targets/records", map[string]any{"name": "target"}, user.Token, nil, map[string]string{"Idempotency-Key": "hidden-target-0001"})
	assertPublicUsersStatus(t, targetResponse, http.StatusCreated)
	var target map[string]any
	decodePublicUsersJSON(t, targetResponse, &target)
	targetID := target["id"].(string)
	sourceResponse := publicUsersRequest(t, app, http.MethodPost, "/api/v1/user/collections/hidden_sources/records", map[string]any{"title": "source", "owner": targetID}, user.Token, nil, map[string]string{"Idempotency-Key": "hidden-source-0001"})
	assertPublicUsersStatus(t, sourceResponse, http.StatusCreated)
	var source map[string]any
	decodePublicUsersJSON(t, sourceResponse, &source)
	sourceID := source["id"].(string)
	expanded := publicUsersRequest(t, app, http.MethodGet, "/api/v1/user/collections/hidden_sources/records/"+sourceID+"?expand=owner", nil, user.Token, nil, nil)
	assertPublicUsersStatus(t, expanded, http.StatusOK)
	decodePublicUsersJSON(t, expanded, &source)
	owner := source["expand"].(map[string]any)["owner"].(map[string]any)
	if _, ok := owner["secret"]; ok {
		t.Fatalf("nested target leaked: %#v", owner)
	}
	for _, path := range []string{"/api/v1/user/collections/hidden_sources/records/" + sourceID + "?expand=concealed", "/api/v1/user/collections/hidden_targets/records/" + targetID + "?expand=hidden_sources_via_concealed", "/api/v1/user/collections/hidden_sources/records?filter=owner.secret%20%3D%20%27target-secret%27", "/api/v1/user/collections/hidden_sources/records?sort=owner.secret"} {
		assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodGet, path, nil, user.Token, nil, nil), http.StatusUnprocessableEntity)
	}
}

func applyFieldVisibilityCollection(t *testing.T, app *publicUsersFixture, cookie *http.Cookie, csrf string, change map[string]any) {
	t.Helper()
	preview := publicUsersRequest(t, app, http.MethodPost, "/api/admin/collections/plan", change, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, preview, http.StatusOK)
	var plan struct {
		BaseVersion int64  `json:"baseVersion"`
		Checksum    string `json:"checksum"`
	}
	decodePublicUsersJSON(t, preview, &plan)
	change["baseVersion"], change["checksum"] = plan.BaseVersion, plan.Checksum
	want := http.StatusCreated
	if change["operation"] == "update" {
		want = http.StatusOK
	}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPost, "/api/admin/collections/apply", change, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), want)
}
