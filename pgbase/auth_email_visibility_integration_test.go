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
