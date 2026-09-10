package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

func TestSetupRouteRejectsMalformedJSON(t *testing.T) {
	handler := mustRouter(t, Dependencies{})
	request := httptest.NewRequest(http.MethodPost, "/api/admin/setup", strings.NewReader("{"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "invalid_request")
}

func TestLoginRouteRejectsMalformedJSON(t *testing.T) {
	handler := mustRouter(t, Dependencies{})
	request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/login", strings.NewReader("{"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "invalid_request")
}

func TestRejectDuplicateJSONKeysDoesNotFailOpenOnLargeNumbers(t *testing.T) {
	if err := rejectDuplicateJSONKeys([]byte(`{"value":1e400,"label":"safe"}`)); err != nil {
		t.Fatalf("unique large-number JSON error = %v", err)
	}
	for _, raw := range []string{
		`{"value":1e400,"value":2}`,
		`{"value":1e400,"nested":{"label":"safe","label":"last"}}`,
		`{"value":1e400,"l\u0061bel":"safe","label":"last"}`,
	} {
		if err := rejectDuplicateJSONKeys([]byte(raw)); err == nil {
			t.Fatalf("rejectDuplicateJSONKeys(%s) error = nil", raw)
		}
	}
}

func TestSessionRouteRequiresAuthentication(t *testing.T) {
	handler := mustRouter(t, Dependencies{})
	request := httptest.NewRequest(http.MethodGet, "/api/admin/session", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "authentication_required")
}

func TestAdminStatusReportsFirstRunWithoutAuthentication(t *testing.T) {
	auth := &fakeAdminAuth{setupRequired: true}
	handler := mustRouter(t, testDependencies(auth, mustURL("https://pgbase.example")))
	request := httptest.NewRequest(http.MethodGet, "/api/admin/status", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		SetupRequired bool `json:"setupRequired"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.SetupRequired || auth.setupRequiredCalls != 1 {
		t.Fatalf("body=%+v calls=%d", body, auth.setupRequiredCalls)
	}
}

func TestLogoutRouteRequiresAuthentication(t *testing.T) {
	handler := mustRouter(t, Dependencies{})
	request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/logout", nil)
	request.Header.Set("Origin", "http://example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "authentication_required")
}

func TestSetupSetsSecureAdminCookie(t *testing.T) {
	auth := &fakeAdminAuth{setupSession: validSession()}
	handler := mustRouter(t, testDependencies(auth, mustURL("https://pgbase.example")))
	request := httptest.NewRequest(http.MethodPost, "/api/admin/setup",
		strings.NewReader("{\"setupToken\":\"secret\",\"email\":\"admin@example.com\",\"password\":\"correct horse battery staple\"}"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://pgbase.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%+v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != adminCookieName || cookie.Value != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" || cookie.Path != "/" || cookie.Domain != "" ||
		!cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie=%+v", cookie)
	}
	if auth.setupCalls != 1 || auth.setupToken != "secret" || auth.setupEmail != "admin@example.com" ||
		auth.setupPassword != "correct horse battery staple" || auth.setupSubject != "192.0.2.1" {
		t.Fatalf("setup call=%+v", auth)
	}
	assertAdminSessionResponse(t, response.Body.Bytes(), validSession())
}

func TestSetupAcceptsMaximumConfiguredTokenAndPasswordWithinBodyLimit(t *testing.T) {
	auth := &fakeAdminAuth{setupSession: validSession()}
	handler := mustRouter(t, testDependencies(auth, mustURL("https://pgbase.example")))
	input := map[string]string{
		// Both values force encoding/json's six-byte escaping. The 1 KiB setup
		// token ceiling must still leave room for the 1 KiB password ceiling.
		"setupToken": strings.Repeat("&", 1024),
		"email":      "admin@example.com",
		"password":   strings.Repeat("&", 1024),
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > maximumCredentialRequestBytes {
		t.Fatalf("encoded maximum credentials = %d bytes, body limit %d", len(body), maximumCredentialRequestBytes)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/setup", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://pgbase.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if auth.setupToken != input["setupToken"] || auth.setupPassword != input["password"] {
		t.Fatal("setup service did not receive the maximum-size credentials")
	}
}

func TestLoginReturnsAdministratorSession(t *testing.T) {
	auth := &fakeAdminAuth{loginSession: validSession()}
	handler := mustRouter(t, testDependencies(auth, mustURL("http://pgbase.example")))
	request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/login",
		strings.NewReader("{\"email\":\"admin@example.com\",\"password\":\"correct horse battery staple\"}"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://pgbase.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != adminDevelopmentCookie || cookies[0].Secure {
		t.Fatalf("cookies=%+v", cookies)
	}
	if auth.loginCalls != 1 || auth.loginSubject != "192.0.2.1" {
		t.Fatalf("login call=%+v", auth)
	}
	assertAdminSessionResponse(t, response.Body.Bytes(), validSession())
}

func TestSessionAuthenticatesCookieAndReturnsCSRFToken(t *testing.T) {
	session := validSession()
	auth := &fakeAdminAuth{authenticateAdmin: session.Administrator, authenticateExpiry: session.ExpiresAt}
	handler := mustRouter(t, testDependencies(auth, mustURL("https://pgbase.example")))
	request := httptest.NewRequest(http.MethodGet, "/api/admin/session", nil)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: session.RawToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if auth.authenticateCalls != 1 || auth.authenticateToken != session.RawToken {
		t.Fatalf("authenticate call=%+v", auth)
	}
	assertAdminSessionResponse(t, response.Body.Bytes(), session)
}

func TestLogoutRevokesSessionAndClearsCookie(t *testing.T) {
	session := validSession()
	auth := &fakeAdminAuth{authenticateAdmin: session.Administrator, authenticateExpiry: session.ExpiresAt}
	handler := mustRouter(t, testDependencies(auth, mustURL("https://pgbase.example")))
	request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/logout", nil)
	request.Header.Set("Origin", "https://pgbase.example")
	request.Header.Set("X-PgBase-CSRF", "6nY1MEBlcXJ8G0ZYhHnSS7QrtrSIeotz4iAyPNYTbF4")
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: session.RawToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if auth.logoutCalls != 1 || auth.logoutToken != session.RawToken {
		t.Fatalf("logout call=%+v", auth)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != adminCookieName || cookies[0].MaxAge >= 0 || !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Fatalf("cleared cookies=%+v", cookies)
	}
}

func TestSetupAndLoginRejectUnknownAndTrailingJSON(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "setup unknown", path: "/api/admin/setup", body: `{"setupToken":"secret","email":"admin@example.com","password":"valid password","role":"owner"}`},
		{name: "setup trailing", path: "/api/admin/setup", body: `{"setupToken":"secret","email":"admin@example.com","password":"valid password"} {}`},
		{name: "login unknown", path: "/api/admin/auth/login", body: `{"email":"admin@example.com","password":"valid password","role":"owner"}`},
		{name: "login trailing", path: "/api/admin/auth/login", body: `{"email":"admin@example.com","password":"valid password"} []`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &fakeAdminAuth{setupSession: validSession(), loginSession: validSession()}
			handler := mustRouter(t, testDependencies(auth, mustURL("https://pgbase.example")))
			request := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", "https://pgbase.example")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertErrorCode(t, response.Body.Bytes(), "invalid_request")
			if auth.setupCalls != 0 || auth.loginCalls != 0 {
				t.Fatalf("authentication service called: %+v", auth)
			}
		})
	}
}

func TestAdminAuthenticationErrorsUseSafeEnvelope(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		retryAfter string
	}{
		{name: "validation", err: adminauth.ErrValidation, wantStatus: http.StatusBadRequest, wantCode: "validation_failed"},
		{name: "credentials", err: adminauth.ErrInvalidCredentials, wantStatus: http.StatusUnauthorized, wantCode: "invalid_credentials"},
		{name: "setup unavailable", err: adminauth.ErrSetupUnavailable, wantStatus: http.StatusConflict, wantCode: "setup_unavailable"},
		{name: "rate limited", err: adminauth.ErrRateLimited, wantStatus: http.StatusTooManyRequests, wantCode: "rate_limited", retryAfter: "1"},
		{name: "retryable database", err: &pgconn.PgError{Code: "40001", Message: "serialization failure"}, wantStatus: http.StatusServiceUnavailable, wantCode: "authentication_unavailable", retryAfter: "1"},
		{name: "internal", err: errors.New("SQLSTATE 23505 postgres://secret"), wantStatus: http.StatusInternalServerError, wantCode: "internal_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &fakeAdminAuth{loginErr: tt.err}
			handler := mustRouter(t, testDependencies(auth, mustURL("https://pgbase.example")))
			request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/login", strings.NewReader(`{"email":"admin@example.com","password":"secret-password"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", "https://pgbase.example")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertErrorCode(t, response.Body.Bytes(), tt.wantCode)
			if got := response.Header().Get("Retry-After"); got != tt.retryAfter {
				t.Fatalf("Retry-After=%q, want %q", got, tt.retryAfter)
			}
			if strings.Contains(response.Body.String(), "SQLSTATE") || strings.Contains(response.Body.String(), "postgres://") || strings.Contains(response.Body.String(), "secret-password") {
				t.Fatalf("response leaked sensitive input: %s", response.Body.String())
			}
		})
	}
}

func assertAdminSessionResponse(t *testing.T, body []byte, want adminauth.Session) {
	t.Helper()
	var response struct {
		Administrator struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"administrator"`
		CSRFToken string `json:"csrfToken"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode session response: %v", err)
	}
	if response.Administrator.ID != want.Administrator.ID.String() || response.Administrator.Email != want.Administrator.Email {
		t.Fatalf("administrator=%+v", response.Administrator)
	}
	if response.CSRFToken != "6nY1MEBlcXJ8G0ZYhHnSS7QrtrSIeotz4iAyPNYTbF4" {
		t.Fatalf("csrfToken=%q", response.CSRFToken)
	}
	if response.ExpiresAt != want.ExpiresAt.UTC().Format(time.RFC3339) {
		t.Fatalf("expiresAt=%q, want %q", response.ExpiresAt, want.ExpiresAt.UTC().Format(time.RFC3339))
	}
}
