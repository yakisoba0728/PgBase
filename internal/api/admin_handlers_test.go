package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/webisnoteasy/PgBase/internal/adminauth"
)

func TestSetupRouteRejectsMalformedJSON(t *testing.T) {
	handler := NewRouter(Dependencies{})
	request := httptest.NewRequest(http.MethodPost, "/api/admin/setup", strings.NewReader("{"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code == http.StatusNotFound {
		t.Fatalf("administrator setup route is not registered: body=%s", response.Body.String())
	}
}

func TestLoginRouteRejectsMalformedJSON(t *testing.T) {
	handler := NewRouter(Dependencies{})
	request := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader("{"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code == http.StatusNotFound {
		t.Fatalf("administrator login route is not registered: body=%s", response.Body.String())
	}
}

func TestSessionRouteRequiresAuthentication(t *testing.T) {
	handler := NewRouter(Dependencies{})
	request := httptest.NewRequest(http.MethodGet, "/api/admin/session", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code == http.StatusNotFound {
		t.Fatalf("administrator session route is not registered: body=%s", response.Body.String())
	}
}

func TestLogoutRouteRequiresAuthentication(t *testing.T) {
	handler := NewRouter(Dependencies{})
	request := httptest.NewRequest(http.MethodDelete, "/api/admin/session", nil)
	request.Header.Set("Origin", "http://example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code == http.StatusNotFound {
		t.Fatalf("administrator logout route is not registered: body=%s", response.Body.String())
	}
}

func TestSetupSetsSecureAdminCookie(t *testing.T) {
	auth := &fakeAdminAuth{setupSession: validSession()}
	handler := NewRouter(testDependencies(auth, mustURL("https://pgbase.example")))
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
	if cookie.Name != "pgbase_admin" || cookie.Value != "fixed-session-token" || cookie.Path != "/" ||
		!cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie=%+v", cookie)
	}
	if auth.setupCalls != 1 || auth.setupToken != "secret" || auth.setupEmail != "admin@example.com" ||
		auth.setupPassword != "correct horse battery staple" || auth.setupSubject != "192.0.2.1" {
		t.Fatalf("setup call=%+v", auth)
	}
	assertAdminSessionResponse(t, response.Body.Bytes(), validSession().Administrator)
}

func TestLoginReturnsAdministratorSession(t *testing.T) {
	auth := &fakeAdminAuth{loginSession: validSession()}
	handler := NewRouter(testDependencies(auth, mustURL("http://pgbase.example")))
	request := httptest.NewRequest(http.MethodPost, "/api/admin/login",
		strings.NewReader("{\"email\":\"admin@example.com\",\"password\":\"correct horse battery staple\"}"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://pgbase.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Secure {
		t.Fatalf("cookies=%+v", cookies)
	}
	if auth.loginCalls != 1 || auth.loginSubject != "192.0.2.1" {
		t.Fatalf("login call=%+v", auth)
	}
	assertAdminSessionResponse(t, response.Body.Bytes(), validSession().Administrator)
}

func TestSessionAuthenticatesCookieAndReturnsCSRFToken(t *testing.T) {
	session := validSession()
	auth := &fakeAdminAuth{authenticateAdmin: session.Administrator}
	handler := NewRouter(testDependencies(auth, mustURL("https://pgbase.example")))
	request := httptest.NewRequest(http.MethodGet, "/api/admin/session", nil)
	request.AddCookie(&http.Cookie{Name: "pgbase_admin", Value: session.RawToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if auth.authenticateCalls != 1 || auth.authenticateToken != session.RawToken {
		t.Fatalf("authenticate call=%+v", auth)
	}
	assertAdminSessionResponse(t, response.Body.Bytes(), session.Administrator)
}

func TestLogoutRevokesSessionAndClearsCookie(t *testing.T) {
	session := validSession()
	auth := &fakeAdminAuth{authenticateAdmin: session.Administrator}
	handler := NewRouter(testDependencies(auth, mustURL("https://pgbase.example")))
	request := httptest.NewRequest(http.MethodDelete, "/api/admin/session", nil)
	request.Header.Set("Origin", "https://pgbase.example")
	request.Header.Set("X-PgBase-CSRF", "4LccAvSIPPQQ4q-qU6zW3cQ4E1sUfZmE2_lisE5NTqo")
	request.AddCookie(&http.Cookie{Name: "pgbase_admin", Value: session.RawToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if auth.logoutCalls != 1 || auth.logoutToken != session.RawToken {
		t.Fatalf("logout call=%+v", auth)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "pgbase_admin" || cookies[0].MaxAge >= 0 || !cookies[0].HttpOnly || !cookies[0].Secure {
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
		{name: "login unknown", path: "/api/admin/login", body: `{"email":"admin@example.com","password":"valid password","role":"owner"}`},
		{name: "login trailing", path: "/api/admin/login", body: `{"email":"admin@example.com","password":"valid password"} []`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &fakeAdminAuth{setupSession: validSession(), loginSession: validSession()}
			handler := NewRouter(testDependencies(auth, mustURL("https://pgbase.example")))
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
	}{
		{name: "validation", err: adminauth.ErrValidation, wantStatus: http.StatusBadRequest, wantCode: "validation_failed"},
		{name: "credentials", err: adminauth.ErrInvalidCredentials, wantStatus: http.StatusUnauthorized, wantCode: "invalid_credentials"},
		{name: "setup unavailable", err: adminauth.ErrSetupUnavailable, wantStatus: http.StatusConflict, wantCode: "setup_unavailable"},
		{name: "rate limited", err: adminauth.ErrRateLimited, wantStatus: http.StatusTooManyRequests, wantCode: "rate_limited"},
		{name: "internal", err: errors.New("SQLSTATE 23505 postgres://secret"), wantStatus: http.StatusInternalServerError, wantCode: "internal_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &fakeAdminAuth{loginErr: tt.err}
			handler := NewRouter(testDependencies(auth, mustURL("https://pgbase.example")))
			request := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(`{"email":"admin@example.com","password":"secret-password"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", "https://pgbase.example")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertErrorCode(t, response.Body.Bytes(), tt.wantCode)
			if strings.Contains(response.Body.String(), "SQLSTATE") || strings.Contains(response.Body.String(), "postgres://") || strings.Contains(response.Body.String(), "secret-password") {
				t.Fatalf("response leaked sensitive input: %s", response.Body.String())
			}
		})
	}
}

func assertAdminSessionResponse(t *testing.T, body []byte, want adminauth.Administrator) {
	t.Helper()
	var response struct {
		Administrator struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"administrator"`
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode session response: %v", err)
	}
	if response.Administrator.ID != want.ID.String() || response.Administrator.Email != want.Email {
		t.Fatalf("administrator=%+v", response.Administrator)
	}
	if response.CSRFToken != "4LccAvSIPPQQ4q-qU6zW3cQ4E1sUfZmE2_lisE5NTqo" {
		t.Fatalf("csrfToken=%q", response.CSRFToken)
	}
}
