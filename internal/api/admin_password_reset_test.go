package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

func TestAdministratorPasswordResetEndpoints(t *testing.T) {
	auth := &fakeAdminAuth{}
	handler := mustRouter(t, testDependencies(auth, mustURL("https://pgbase.example")))

	request := httptest.NewRequest(http.MethodPost, "https://pgbase.example/api/admin/auth/request-password-reset", strings.NewReader(`{"email":"admin@example.test"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || auth.resetEmail != "admin@example.test" || auth.resetAddress == "" {
		t.Fatalf("request status=%d email=%q address=%q", response.Code, auth.resetEmail, auth.resetAddress)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control=%q", response.Header().Get("Cache-Control"))
	}

	confirm := httptest.NewRequest(http.MethodPost, "https://pgbase.example/api/admin/auth/confirm-password-reset", strings.NewReader(`{"token":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","newPassword":"correct horse battery staple"}`))
	confirm.Header.Set("Content-Type", "application/json")
	confirmed := httptest.NewRecorder()
	handler.ServeHTTP(confirmed, confirm)
	if confirmed.Code != http.StatusNoContent || auth.resetToken == "" || auth.resetPassword != "correct horse battery staple" {
		t.Fatalf("confirm status=%d token=%q password=%q", confirmed.Code, auth.resetToken, auth.resetPassword)
	}
}

func TestAdministratorPasswordResetInvalidTokenReturnsUnauthorized(t *testing.T) {
	auth := &fakeAdminAuth{resetConfirmErr: adminauth.ErrInvalidCredentials}
	handler := mustRouter(t, testDependencies(auth, mustURL("https://pgbase.example")))
	request := httptest.NewRequest(http.MethodPost, "https://pgbase.example/api/admin/auth/confirm-password-reset", strings.NewReader(`{"token":"invalid","newPassword":"correct horse battery staple"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
