//go:build integration

package pgbase

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestAuthRecordEmailVisibilityPersistsAndProjectsByPrincipal(t *testing.T) {
	fixture := newPublicUsersApp(t, true)
	adminCookie, csrf := setupPublicUsersAdministrator(t, fixture)
	realmID := uuid.New()
	realmName := "visible_" + realmID.String()[:8]
	created := publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/auth-collections", map[string]any{
		"id": realmID.String(), "name": realmName, "registrationEnabled": true, "fields": []any{},
		"rules": map[string]any{"list": "true", "view": "true", "create": "true", "update": "@request.auth.id = id", "delete": "false", "auth": "true", "manage": "false"},
	}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, created, http.StatusCreated)

	owner := registerAuthVisibilityUser(t, fixture, realmName, "private-owner@example.test", publicUserOnePass)
	other := registerAuthVisibilityUser(t, fixture, realmName, "other-viewer@example.test", publicUserTwoPass)
	recordPath := "/api/v1/auth/" + realmName + "/records/" + owner.User.ID.String()
	publicPath := "/api/v1/public/auth/" + realmName + "/records/" + owner.User.ID.String()

	assertAuthVisibilityResponse(t, publicUsersRequest(t, fixture, http.MethodGet, publicPath, nil, "", nil, nil), false, false)
	assertAuthVisibilityResponse(t, publicUsersRequest(t, fixture, http.MethodGet, recordPath, nil, owner.Token, nil, nil), true, false)
	assertAuthVisibilityResponse(t, publicUsersRequest(t, fixture, http.MethodGet, recordPath, nil, other.Token, nil, nil), false, false)
	adminPath := "/api/admin/auth-collections/" + realmID.String() + "/records/" + owner.User.ID.String()
	assertAuthVisibilityResponse(t, publicUsersRequest(t, fixture, http.MethodGet, adminPath, nil, "", adminCookie, nil), true, false)

	visible := true
	updated := publicUsersRequest(t, fixture, http.MethodPatch, recordPath, map[string]any{"emailVisibility": visible}, owner.Token, nil, nil)
	assertAuthVisibilityResponse(t, updated, true, true)
	assertAuthVisibilityResponse(t, publicUsersRequest(t, fixture, http.MethodGet, publicPath, nil, "", nil, nil), true, true)
	assertAuthVisibilityResponse(t, publicUsersRequest(t, fixture, http.MethodGet, recordPath, nil, other.Token, nil, nil), true, true)
	var persisted bool
	if err := fixture.app.pool.QueryRow(context.Background(), `select email_visibility from pgbase_meta.users where id=$1`, owner.User.ID).Scan(&persisted); err != nil || !persisted {
		t.Fatalf("email visibility persistence = %v, err=%v", persisted, err)
	}
}

func TestAuthRecordPrivateEmailProjectsToManagedRecordsOverHTTPAndJavaScript(t *testing.T) {
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/managed-email","access":"user","handler":{"module":"route.js","export":"run"},"capabilities":["records"]}]}`, `exports.run=(e)=>{const r=new Record(new Collection({name:e.json.collection}),{id:e.json.recordId});$apis.enrichRecord(e,r);return {status:200,json:r.export().data}}`)
	fixture := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.JavaScript = &JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"records": true}}
	})
	adminCookie, csrf := setupPublicUsersAdministrator(t, fixture)
	realmID := uuid.New()
	realmName := "managed_visible_" + realmID.String()[:8]
	created := publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/auth-collections", map[string]any{
		"id": realmID.String(), "name": realmName, "registrationEnabled": true,
		"fields": []map[string]any{{"name": "can_manage", "type": "boolean"}},
		"rules":  map[string]any{"list": "true", "view": "true", "create": "true", "update": "true", "delete": "false", "auth": "true", "manage": "@request.auth.can_manage = true && id != @request.auth.id"},
	}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, created, http.StatusCreated)

	target := registerAuthVisibilityUser(t, fixture, realmName, "managed-target@example.test", publicUserOnePass)
	manager := registerAuthVisibilityUser(t, fixture, realmName, "managed-manager@example.test", publicUserTwoPass)
	viewer := registerAuthVisibilityUser(t, fixture, realmName, "managed-viewer@example.test", "third correct horse battery staple")
	profilePath := "/api/v1/auth/" + realmName + "/profile"
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodPatch, profilePath, map[string]any{"can_manage": true}, manager.Token, nil, nil), http.StatusOK)

	recordPath := "/api/v1/auth/" + realmName + "/records/" + target.User.ID.String()
	listPath := "/api/v1/auth/" + realmName + "/records"
	publicRecordPath := "/api/v1/public/auth/" + realmName + "/records/" + target.User.ID.String()
	publicListPath := "/api/v1/public/auth/" + realmName + "/records"
	adminRecordPath := "/api/admin/auth-collections/" + realmID.String() + "/records/" + target.User.ID.String()
	adminListPath := "/api/admin/auth-collections/" + realmID.String() + "/records"

	assertAuthVisibilityResponse(t, publicUsersRequest(t, fixture, http.MethodGet, publicRecordPath, nil, "", nil, nil), false, false)
	assertAuthVisibilityList(t, publicUsersRequest(t, fixture, http.MethodGet, publicListPath, nil, "", nil, nil), target.User.ID, false)
	assertAuthVisibilityResponse(t, publicUsersRequest(t, fixture, http.MethodGet, recordPath, nil, target.Token, nil, nil), true, false)
	assertAuthVisibilityList(t, publicUsersRequest(t, fixture, http.MethodGet, listPath, nil, target.Token, nil, nil), target.User.ID, true)
	assertAuthVisibilityResponse(t, publicUsersRequest(t, fixture, http.MethodGet, recordPath, nil, manager.Token, nil, nil), true, false)
	assertAuthVisibilityList(t, publicUsersRequest(t, fixture, http.MethodGet, listPath, nil, manager.Token, nil, nil), target.User.ID, true)
	assertAuthVisibilityResponse(t, publicUsersRequest(t, fixture, http.MethodGet, recordPath, nil, viewer.Token, nil, nil), false, false)
	assertAuthVisibilityList(t, publicUsersRequest(t, fixture, http.MethodGet, listPath, nil, viewer.Token, nil, nil), target.User.ID, false)
	assertAuthVisibilityResponse(t, publicUsersRequest(t, fixture, http.MethodGet, adminRecordPath, nil, "", adminCookie, nil), true, false)
	assertAuthVisibilityList(t, publicUsersRequest(t, fixture, http.MethodGet, adminListPath, nil, "", adminCookie, nil), target.User.ID, true)

	javascript := publicUsersRequest(t, fixture, http.MethodPost, "/managed-email", map[string]any{"collection": realmName, "recordId": target.User.ID.String()}, manager.Token, nil, nil)
	assertAuthVisibilityResponse(t, javascript, true, false)
	nonManagerJavaScript := publicUsersRequest(t, fixture, http.MethodPost, "/managed-email", map[string]any{"collection": realmName, "recordId": target.User.ID.String()}, viewer.Token, nil, nil)
	assertAuthVisibilityResponse(t, nonManagerJavaScript, false, false)
}

func registerAuthVisibilityUser(t *testing.T, fixture *publicUsersFixture, realm, email, password string) publicUserSession {
	t.Helper()
	response := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/auth/"+realm+"/register", map[string]any{"email": email, "password": password}, "", nil, nil)
	assertPublicUsersStatus(t, response, http.StatusCreated)
	var result publicUserSession
	decodePublicUsersJSON(t, response, &result)
	return result
}

func assertAuthVisibilityResponse(t *testing.T, response *httptest.ResponseRecorder, wantEmail, wantVisibility bool) {
	t.Helper()
	assertPublicUsersStatus(t, response, http.StatusOK)
	var record map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	_, hasEmail := record["email"]
	if hasEmail != wantEmail || record["emailVisibility"] != wantVisibility {
		t.Fatalf("email=%v visibility=%v response=%s", hasEmail, record["emailVisibility"], response.Body.String())
	}
}

func assertAuthVisibilityList(t *testing.T, response *httptest.ResponseRecorder, target uuid.UUID, wantEmail bool) {
	t.Helper()
	assertPublicUsersStatus(t, response, http.StatusOK)
	var page struct {
		Items []map[string]any `json:"items"`
	}
	decodePublicUsersJSON(t, response, &page)
	for _, record := range page.Items {
		if record["id"] != target.String() {
			continue
		}
		_, hasEmail := record["email"]
		if hasEmail != wantEmail {
			t.Fatalf("email=%v want=%v response=%s", hasEmail, wantEmail, response.Body.String())
		}
		return
	}
	t.Fatalf("target record %s missing from %s", target, response.Body.String())
}
