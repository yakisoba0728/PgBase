package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

type callbackFactors struct {
	destination string
	calls       int
}

func (*callbackFactors) BeginTOTPEnrollment(context.Context, uuid.UUID, string) (userauth.TOTPEnrollment, error) {
	return userauth.TOTPEnrollment{}, nil
}
func (*callbackFactors) ConfirmTOTPEnrollment(context.Context, uuid.UUID, string) (userauth.RecoveryCodes, error) {
	return userauth.RecoveryCodes{}, nil
}
func (*callbackFactors) DisableTOTP(context.Context, uuid.UUID, string, string) error { return nil }
func (*callbackFactors) CompleteMFA(context.Context, string, string) (userauth.Session, error) {
	return userauth.Session{}, nil
}
func (*callbackFactors) BeginOAuth(context.Context, string, string, string) (userauth.OAuthAuthorization, error) {
	return userauth.OAuthAuthorization{}, nil
}
func (*callbackFactors) BeginOAuthWithProviderRedirect(context.Context, string, string, string, string) (userauth.OAuthAuthorization, error) {
	return userauth.OAuthAuthorization{}, nil
}
func (*callbackFactors) BeginOAuthLink(context.Context, uuid.UUID, string, string, string) (userauth.OAuthAuthorization, error) {
	return userauth.OAuthAuthorization{}, nil
}
func (*callbackFactors) BeginOAuthLinkWithProviderRedirect(context.Context, uuid.UUID, string, string, string, string) (userauth.OAuthAuthorization, error) {
	return userauth.OAuthAuthorization{}, nil
}
func (*callbackFactors) UnlinkOAuthIdentity(context.Context, uuid.UUID, string) error { return nil }
func (*callbackFactors) CompleteOAuth(context.Context, string, string, string) (userauth.Session, error) {
	return userauth.Session{}, nil
}
func (f *callbackFactors) AcceptOAuthCallback(_ context.Context, provider, state, code, providerError, description string) (string, error) {
	f.calls++
	if provider != "apple" || state != "state" || code != "code" {
		return "", userauth.ErrInvalidCredentials
	}
	return f.destination, nil
}
func (*callbackFactors) GetOAuthAvatar(context.Context, uuid.UUID) (userauth.OAuthAvatar, error) {
	return userauth.OAuthAvatar{}, nil
}
func (*callbackFactors) RequestEmailOTP(context.Context, string, string) (userauth.EmailOTPRequest, error) {
	return userauth.EmailOTPRequest{}, nil
}
func (*callbackFactors) CompleteEmailOTP(context.Context, string, string) (userauth.Session, error) {
	return userauth.Session{}, nil
}

func TestAppleOAuthCallbackRedirectsBoundDestination(t *testing.T) {
	f := &callbackFactors{destination: "https://app.example/callback?code=code&state=state"}
	e := userEndpoints{dependencies: Dependencies{AuthFactors: f}}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/apple/callback", strings.NewReader("code=code&state=state"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	e.appleOAuthCallback(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != f.destination || f.calls != 1 {
		t.Fatalf("status=%d location=%q calls=%d", w.Code, w.Header().Get("Location"), f.calls)
	}
}

func TestAppleOAuthCallbackRejectsMalformedForms(t *testing.T) {
	f := &callbackFactors{destination: "https://app.example/callback"}
	e := userEndpoints{dependencies: Dependencies{AuthFactors: f}}
	for _, test := range []struct{ contentType, body string }{{"application/json", `{}`}, {"application/x-www-form-urlencoded", "code=a&code=b&state=state"}, {"application/x-www-form-urlencoded", "code=code&state=state&redirect_uri=https://attacker.test"}} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/apple/callback", strings.NewReader(test.body))
		r.Header.Set("Content-Type", test.contentType)
		w := httptest.NewRecorder()
		e.appleOAuthCallback(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%q status=%d", test.body, w.Code)
		}
	}
	if f.calls != 0 {
		t.Fatalf("service calls=%d", f.calls)
	}
}
