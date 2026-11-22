package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/userauth"
)

type verifierFactors struct {
	callbackFactors
	begins, completes int
}

func (f *verifierFactors) BeginOAuth(context.Context, string, string, string) (userauth.OAuthAuthorization, error) {
	f.begins++
	return userauth.OAuthAuthorization{State: "state", CodeVerifier: "verifier", CodeChallengeMethod: "S256"}, nil
}

func (f *verifierFactors) CompleteOAuth(context.Context, string, string, string) (userauth.Session, error) {
	f.completes++
	return userauth.Session{}, nil
}

func TestOAuthVerifierFieldsAreScopedToTheirActions(t *testing.T) {
	for _, test := range []struct {
		action, body string
		status       int
	}{
		{"oauth/start", `{"provider":"local","redirectUri":"https://app.example/cb","requireCodeVerifier":true}`, http.StatusOK},
		{"oauth/complete", `{"provider":"local","state":"state","code":"code","codeVerifier":"verifier"}`, http.StatusOK},
		{"oauth/start", `{"provider":"local","redirectUri":"https://app.example/cb","codeVerifier":"verifier"}`, http.StatusBadRequest},
		{"oauth/complete", `{"provider":"local","state":"state","code":"code","requireCodeVerifier":true}`, http.StatusBadRequest},
		{"otp/complete", `{"otpId":"id","code":"123456","codeVerifier":"verifier"}`, http.StatusBadRequest},
		{"mfa/complete", `{"challengeToken":"token","code":"123456","requireCodeVerifier":true}`, http.StatusBadRequest},
	} {
		f := &verifierFactors{}
		e := userEndpoints{dependencies: Dependencies{AuthFactors: f}}
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/"+test.action, strings.NewReader(test.body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.factorAction(test.action).ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("%s %s status=%d body=%s", test.action, test.body, w.Code, w.Body.String())
		}
		if test.status == http.StatusOK && f.begins+f.completes != 1 || test.status != http.StatusOK && f.begins+f.completes != 0 {
			t.Fatalf("%s %s begins=%d completes=%d", test.action, test.body, f.begins, f.completes)
		}
		if test.action == "oauth/start" && test.status == http.StatusOK && !strings.Contains(w.Body.String(), `"codeVerifier":"verifier"`) {
			t.Fatalf("start response lacks codeVerifier: %s", w.Body.String())
		}
	}
}
