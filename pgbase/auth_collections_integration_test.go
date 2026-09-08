//go:build integration

package pgbase

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestAuthCollectionHTTPIsolationAndProfile(t *testing.T) {
	app := newPublicUsersApp(t, true)
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	realmID := uuid.New()
	created := publicUsersRequest(t, app, http.MethodPost, "/api/admin/auth-collections", map[string]any{"id": realmID.String(), "name": "customers", "registrationEnabled": true, "profileSchema": map[string]string{"display_name": "text"}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	registered := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/customers/register", map[string]any{"email": "realm@example.test", "password": publicUserOnePass}, "", nil, nil)
	assertPublicUsersStatus(t, registered, http.StatusCreated)
	var session publicUserSession
	decodePublicUsersJSON(t, registered, &session)
	profile := publicUsersRequest(t, app, http.MethodPatch, "/api/v1/auth/customers/profile", map[string]any{"display_name": "Realm User"}, session.Token, nil, nil)
	assertPublicUsersStatus(t, profile, http.StatusOK)
	partialUpdate := publicUsersRequest(t, app, http.MethodPatch, "/api/admin/auth-collections/"+realmID.String(), map[string]any{"registrationEnabled": false}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, partialUpdate, http.StatusBadRequest)
	stillAcceptsProfile := publicUsersRequest(t, app, http.MethodPatch, "/api/v1/auth/customers/profile", map[string]any{"display_name": "Still Valid"}, session.Token, nil, nil)
	assertPublicUsersStatus(t, stillAcceptsProfile, http.StatusOK)
	cross := publicUsersRequest(t, app, http.MethodGet, "/api/v1/auth/users/session", nil, session.Token, nil, nil)
	assertPublicUsersStatus(t, cross, http.StatusUnauthorized)
	duplicateDefault := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/register", map[string]any{"email": "realm@example.test", "password": publicUserTwoPass}, "", nil, nil)
	assertPublicUsersStatus(t, duplicateDefault, http.StatusCreated)
	deleted := publicUsersRequest(t, app, http.MethodDelete, "/api/admin/auth-collections/"+realmID.String(), nil, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, deleted, http.StatusConflict)
}
