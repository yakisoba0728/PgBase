//go:build integration

package pgbase

import (
	"net/http"
	"testing"
)

func TestVerificationHTTPRejectsTokenAfterAdministratorEmailChange(t *testing.T) {
	address, messages := localMailSink(t)
	app := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.SMTP = SMTPOptions{Address: address, From: "binding@example.test", AllowInsecure: true}
	})
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	session := registerPublicUser(t, app, "original@example.test", publicUserOnePass)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/request-verification", map[string]any{}, session.Token, nil, nil), http.StatusNoContent)
	oldToken := mailActionToken(t, messages, "verify")
	recordPath := "/api/admin/auth-collections/00000000-0000-0000-0000-000000000001/records/" + session.User.ID.String()
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPatch, recordPath, map[string]any{"email": "replacement@example.test"}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), http.StatusOK)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/confirm-verification", map[string]any{"token": oldToken}, "", nil, nil), http.StatusUnauthorized)
	response := publicUsersRequest(t, app, http.MethodGet, recordPath, nil, "", cookie, nil)
	assertPublicUsersStatus(t, response, http.StatusOK)
	var record struct {
		Email    string `json:"email"`
		Verified bool   `json:"verified"`
	}
	decodePublicUsersJSON(t, response, &record)
	if record.Email != "replacement@example.test" || record.Verified {
		t.Fatalf("old-address token changed account: %#v", record)
	}
	session = userManagementLogin(t, app, record.Email, publicUserOnePass)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/request-verification", map[string]any{}, session.Token, nil, nil), http.StatusNoContent)
	freshToken := mailActionToken(t, messages, "verify")
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/confirm-verification", map[string]any{"token": freshToken}, "", nil, nil), http.StatusNoContent)
}
