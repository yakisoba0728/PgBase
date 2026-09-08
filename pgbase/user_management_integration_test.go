//go:build integration

package pgbase

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const changedUserPassword = "changed-user-fixture-passphrase-6048"

type managedPublicUser struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func TestAdministratorUserManagementAndPasswordChangeHTTPContract(t *testing.T) {
	app := newPublicUsersApp(t, true)
	adminCookie, csrf := setupPublicUsersAdministrator(t, app)
	createPublicUsersCollection(t, app, adminCookie, csrf, "managed_notes")
	enable := publicUsersRequest(t, app, http.MethodPut, "/api/admin/collections/managed_notes/user-access",
		map[string]any{"enabled": true}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, enable, http.StatusOK)

	first := registerPublicUser(t, app, "managed-first@example.test", publicUserOnePass)
	second := registerPublicUser(t, app, "managed-second@example.test", publicUserTwoPass)
	collectionPath := "/api/v1/user/collections/managed_notes/records"
	created := publicUsersRequest(t, app, http.MethodPost, collectionPath,
		map[string]any{"title": "ownership survives account state"}, first.Token, nil,
		map[string]string{"Idempotency-Key": "managed-user-record-create-0001"})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	var record map[string]any
	decodePublicUsersJSON(t, created, &record)
	recordID, ok := record["id"].(string)
	if !ok || uuid.Validate(recordID) != nil {
		t.Fatalf("created record id = %#v", record["id"])
	}
	recordPath := collectionPath + "/" + recordID

	listed := publicUsersRequest(t, app, http.MethodGet, "/api/admin/users?limit=50", nil, "", adminCookie, nil)
	assertPublicUsersStatus(t, listed, http.StatusOK)
	listedBody := strings.ToLower(listed.Body.String())
	var page struct {
		Items     []map[string]json.RawMessage `json:"items"`
		NextAfter *uuid.UUID                   `json:"nextAfter,omitempty"`
	}
	decodePublicUsersJSON(t, listed, &page)
	if len(page.Items) != 2 || page.NextAfter != nil {
		t.Fatalf("administrator user page items=%d nextAfter=%v", len(page.Items), page.NextAfter)
	}
	wantEmails := map[string]uuid.UUID{first.User.Email: first.User.ID, second.User.Email: second.User.ID}
	for index, item := range page.Items {
		for field := range item {
			switch field {
			case "id", "email", "active", "verified", "createdAt", "updatedAt":
			default:
				t.Fatalf("administrator user item %d exposed unexpected field %q", index, field)
			}
		}
		var user managedPublicUser
		encoded, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &user); err != nil {
			t.Fatalf("decode administrator user item %d: %v", index, err)
		}
		if wantEmails[user.Email] != user.ID || !user.Active || user.CreatedAt.IsZero() || user.UpdatedAt.IsZero() {
			t.Fatalf("administrator user item %d = %+v", index, user)
		}
		delete(wantEmails, user.Email)
	}
	if len(wantEmails) != 0 {
		t.Fatalf("administrator list omitted users: %v", wantEmails)
	}
	if strings.Contains(listedBody, "password") || strings.Contains(listedBody, "token") {
		t.Fatal("administrator user list exposed authentication material")
	}

	publicOnAdmin := publicUsersRequest(t, app, http.MethodGet, "/api/admin/users", nil, first.Token, nil, nil)
	assertPublicUsersStatus(t, publicOnAdmin, http.StatusUnauthorized)
	adminOnPassword := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/change-password",
		map[string]any{"currentPassword": publicAdminPass, "newPassword": changedUserPassword}, "", adminCookie, nil)
	assertPublicUsersStatus(t, adminOnPassword, http.StatusUnauthorized)

	firstPath := "/api/admin/users/" + first.User.ID.String()
	missingPath := "/api/admin/users/0199ab10-0000-7000-8000-000000000099"
	missingPatch := publicUsersRequest(t, app, http.MethodPatch, missingPath,
		map[string]any{"active": false}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, missingPatch, http.StatusNotFound)
	missingRevoke := publicUsersRequest(t, app, http.MethodPost, missingPath+"/revoke-sessions",
		nil, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, missingRevoke, http.StatusNotFound)
	missingCSRF := publicUsersRequest(t, app, http.MethodPatch, firstPath,
		map[string]any{"active": false}, "", adminCookie, nil)
	assertPublicUsersStatus(t, missingCSRF, http.StatusForbidden)
	wrongOrigin := publicUsersRequest(t, app, http.MethodPost, firstPath+"/revoke-sessions",
		nil, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf, "Origin": "https://attacker.example"})
	assertPublicUsersStatus(t, wrongOrigin, http.StatusForbidden)

	disabled := publicUsersRequest(t, app, http.MethodPatch, firstPath,
		map[string]any{"active": false}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, disabled, http.StatusOK)
	var disabledUser managedPublicUser
	decodePublicUsersJSON(t, disabled, &disabledUser)
	if disabledUser.ID != first.User.ID || disabledUser.Active {
		t.Fatalf("disabled user = %+v", disabledUser)
	}
	assertUserManagementSessionStatus(t, app, first.Token, http.StatusUnauthorized)
	assertUserManagementLoginStatus(t, app, first.User.Email, publicUserOnePass, http.StatusUnauthorized)

	reactivated := publicUsersRequest(t, app, http.MethodPatch, firstPath,
		map[string]any{"active": true}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, reactivated, http.StatusOK)
	var reactivatedUser managedPublicUser
	decodePublicUsersJSON(t, reactivated, &reactivatedUser)
	if reactivatedUser.ID != first.User.ID || !reactivatedUser.Active {
		t.Fatalf("reactivated user = %+v", reactivatedUser)
	}
	assertUserManagementSessionStatus(t, app, first.Token, http.StatusUnauthorized)
	activeOne := userManagementLogin(t, app, first.User.Email, publicUserOnePass)
	preserved := publicUsersRequest(t, app, http.MethodGet, recordPath, nil, activeOne.Token, nil, nil)
	assertPublicUsersStatus(t, preserved, http.StatusOK)
	activeTwo := userManagementLogin(t, app, first.User.Email, publicUserOnePass)

	revoked := publicUsersRequest(t, app, http.MethodPost, firstPath+"/revoke-sessions",
		nil, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, revoked, http.StatusOK)
	var revokeResult struct {
		RevokedSessions int `json:"revokedSessions"`
	}
	decodePublicUsersJSON(t, revoked, &revokeResult)
	if revokeResult.RevokedSessions != 2 {
		t.Fatalf("revoked sessions = %d, want 2", revokeResult.RevokedSessions)
	}
	assertUserManagementSessionStatus(t, app, activeOne.Token, http.StatusUnauthorized)
	assertUserManagementSessionStatus(t, app, activeTwo.Token, http.StatusUnauthorized)
	passwordSessionOne := userManagementLogin(t, app, first.User.Email, publicUserOnePass)
	passwordSessionTwo := userManagementLogin(t, app, first.User.Email, publicUserOnePass)

	malformed := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/change-password",
		map[string]any{"currentPassword": publicUserOnePass}, passwordSessionOne.Token, nil, nil)
	assertPublicUsersStatus(t, malformed, http.StatusBadRequest)
	wrongCurrent := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/change-password",
		map[string]any{"currentPassword": publicUserTwoPass, "newPassword": changedUserPassword},
		passwordSessionOne.Token, nil, nil)
	assertPublicUsersStatus(t, wrongCurrent, http.StatusUnauthorized)
	assertUserManagementSessionStatus(t, app, passwordSessionOne.Token, http.StatusOK)
	assertUserManagementSessionStatus(t, app, passwordSessionTwo.Token, http.StatusOK)

	changed := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/change-password",
		map[string]any{"currentPassword": publicUserOnePass, "newPassword": changedUserPassword},
		passwordSessionOne.Token, nil, nil)
	assertPublicUsersStatus(t, changed, http.StatusNoContent)
	assertUserManagementSessionStatus(t, app, passwordSessionOne.Token, http.StatusUnauthorized)
	assertUserManagementSessionStatus(t, app, passwordSessionTwo.Token, http.StatusUnauthorized)
	assertUserManagementLoginStatus(t, app, first.User.Email, publicUserOnePass, http.StatusUnauthorized)
	newPasswordSession := userManagementLogin(t, app, first.User.Email, changedUserPassword)
	assertUserManagementSessionStatus(t, app, newPasswordSession.Token, http.StatusOK)
}

func userManagementLogin(t *testing.T, app *publicUsersFixture, email, password string) publicUserSession {
	t.Helper()
	response := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/login",
		map[string]any{"email": email, "password": password}, "", nil, nil)
	assertPublicUsersStatus(t, response, http.StatusOK)
	var session publicUserSession
	decodePublicUsersJSON(t, response, &session)
	if session.Token == "" || session.User.Email != email {
		t.Fatalf("login session token_empty=%t email=%q", session.Token == "", session.User.Email)
	}
	return session
}

func assertUserManagementLoginStatus(t *testing.T, app *publicUsersFixture, email, password string, want int) {
	t.Helper()
	response := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/login",
		map[string]any{"email": email, "password": password}, "", nil, nil)
	assertPublicUsersStatus(t, response, want)
}

func assertUserManagementSessionStatus(t *testing.T, app *publicUsersFixture, token string, want int) {
	t.Helper()
	response := publicUsersRequest(t, app, http.MethodGet, "/api/v1/auth/session", nil, token, nil, nil)
	assertPublicUsersStatus(t, response, want)
}
