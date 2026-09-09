//go:build integration

package pgbase

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestNamedRealmImpersonationHTTPProjectionRestrictionsAndRevocation(t *testing.T) {
	app := newPublicUsersApp(t, true)
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	realmID := uuid.New()
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPost, "/api/admin/auth-collections", map[string]any{"id": realmID, "name": "impersonated_customers", "registrationEnabled": true, "profileSchema": map[string]string{}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), http.StatusCreated)
	registered := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/impersonated_customers/register", map[string]any{"email": "impersonated-http@example.test", "password": publicUserOnePass}, "", nil, nil)
	assertPublicUsersStatus(t, registered, http.StatusCreated)
	var ordinary publicUserSession
	decodePublicUsersJSON(t, registered, &ordinary)

	issued := publicUsersRequest(t, app, http.MethodPost, "/api/admin/users/"+ordinary.User.ID.String()+"/impersonate", map[string]any{"durationSeconds": 120}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, issued, http.StatusOK)
	var impersonated publicUserSession
	decodePublicUsersJSON(t, issued, &impersonated)
	if !impersonated.Impersonated || impersonated.ImpersonatingAdministratorID == uuid.Nil || impersonated.Token == "" {
		t.Fatalf("impersonation response omitted origin: %#v", impersonated)
	}

	current := publicUsersRequest(t, app, http.MethodGet, "/api/v1/auth/impersonated_customers/session", nil, impersonated.Token, nil, nil)
	assertPublicUsersStatus(t, current, http.StatusOK)
	var projected publicUserSession
	decodePublicUsersJSON(t, current, &projected)
	if !projected.Impersonated || projected.ImpersonatingAdministratorID != impersonated.ImpersonatingAdministratorID {
		t.Fatalf("named realm session lost impersonation origin: %#v", projected)
	}

	restricted := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/change-password", map[string]any{"currentPassword": publicUserOnePass, "newPassword": publicUserTwoPass}, impersonated.Token, nil, nil)
	assertPublicUsersStatus(t, restricted, http.StatusUnauthorized)
	for _, action := range []struct {
		path string
		body map[string]any
	}{
		{"/api/v1/auth/totp/enroll", map[string]any{"currentPassword": publicUserOnePass}},
		{"/api/v1/auth/request-email-change", map[string]any{"currentPassword": publicUserOnePass, "newEmail": "changed@example.test"}},
		{"/api/v1/auth/oauth/link", map[string]any{"currentPassword": publicUserOnePass, "provider": "google", "redirectUri": "https://client.example.test/callback"}},
	} {
		assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPost, action.path, action.body, impersonated.Token, nil, nil), http.StatusUnauthorized)
	}
	revoked := publicUsersRequest(t, app, http.MethodPost, "/api/admin/users/"+ordinary.User.ID.String()+"/revoke-sessions", nil, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, revoked, http.StatusOK)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodGet, "/api/v1/auth/impersonated_customers/session", nil, impersonated.Token, nil, nil), http.StatusUnauthorized)
}
