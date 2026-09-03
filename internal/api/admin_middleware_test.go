package api

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestAdminMutationsRejectAbsentOrMismatchedOrigin(t *testing.T) {
	tests := []struct {
		name   string
		origin string
	}{
		{name: "absent"},
		{name: "mismatched", origin: "https://attacker.example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &fakeAdminAuth{setupSession: validSession()}
			handler := mustRouter(t, testDependencies(auth, mustURL("https://pgbase.example")))
			request := httptest.NewRequest(http.MethodPost, "/api/admin/setup",
				strings.NewReader(`{"setupToken":"secret","email":"admin@example.com","password":"correct horse battery staple"}`))
			request.Header.Set("Content-Type", "application/json")
			if tt.origin != "" {
				request.Header.Set("Origin", tt.origin)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertErrorCode(t, response.Body.Bytes(), "origin_rejected")
			if auth.setupCalls != 0 {
				t.Fatal("setup service called before origin rejection")
			}
		})
	}
}

func TestAuthenticatedMutationRejectsMissingCSRF(t *testing.T) {
	session := validSession()
	auth := &fakeAdminAuth{authenticateAdmin: session.Administrator}
	handler := mustRouter(t, testDependencies(auth, mustURL("https://pgbase.example")))
	request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/logout", nil)
	request.Header.Set("Origin", "https://pgbase.example")
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: session.RawToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "csrf_rejected")
	if auth.logoutCalls != 0 {
		t.Fatal("logout service called before CSRF rejection")
	}
}

func TestAdminSessionMiddlewareRequiresOriginAndCSRFForEveryMutationMethod(t *testing.T) {
	session := validSession()
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			auth := &fakeAdminAuth{authenticateAdmin: session.Administrator}
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
			handler := adminSessionMiddleware(auth, mustURL("https://pgbase.example"), nil, next)
			request := httptest.NewRequest(method, "https://pgbase.example/api/admin/resource", nil)
			request.Header.Set("Origin", "https://pgbase.example")
			request.AddCookie(&http.Cookie{Name: adminCookieName, Value: session.RawToken})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertErrorCode(t, response.Body.Bytes(), "csrf_rejected")
		})
	}
}

func TestAdminSessionMiddlewareAllowsReadWithoutOriginOrCSRF(t *testing.T) {
	session := validSession()
	auth := &fakeAdminAuth{authenticateAdmin: session.Administrator}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := adminSessionMiddleware(auth, mustURL("https://pgbase.example"), nil, next)
	request := httptest.NewRequest(http.MethodGet, "https://pgbase.example/api/admin/resource", nil)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: session.RawToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAdminSessionMiddlewareMapsDatabaseUnavailable(t *testing.T) {
	auth := &fakeAdminAuth{authenticateErr: &pgconn.PgError{Code: "53300", Message: "too many connections"}}
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	})
	handler := authenticateAdminMiddleware(auth, next)
	request := httptest.NewRequest(http.MethodGet, "https://pgbase.example/api/admin/session", nil)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: validSession().RawToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("status=%d retry-after=%q body=%s", response.Code, response.Header().Get("Retry-After"), response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "authentication_unavailable")
	if nextCalled {
		t.Fatal("next handler called after authentication database failure")
	}
}

func TestForwardedProtoControlsOriginAndCookieOnlyFromTrustedDirectProxy(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		forwarded  []string
		tls        bool
		origin     string
		wantStatus int
		wantSecure bool
	}{
		{name: "trusted https", remoteAddr: "10.0.0.2:1234", forwarded: []string{"https"}, origin: "https://pgbase.internal", wantStatus: http.StatusCreated, wantSecure: true},
		{name: "untrusted ignored", remoteAddr: "198.51.100.2:1234", forwarded: []string{"https"}, origin: "https://pgbase.internal", wantStatus: http.StatusForbidden},
		{name: "invalid ignored", remoteAddr: "10.0.0.2:1234", forwarded: []string{"ftp"}, origin: "https://pgbase.internal", wantStatus: http.StatusForbidden},
		{name: "comma value ignored", remoteAddr: "10.0.0.2:1234", forwarded: []string{"https, http"}, origin: "https://pgbase.internal", wantStatus: http.StatusForbidden},
		{name: "multiple values ignored", remoteAddr: "10.0.0.2:1234", forwarded: []string{"https", "http"}, origin: "https://pgbase.internal", wantStatus: http.StatusForbidden},
		{name: "direct TLS", remoteAddr: "198.51.100.2:1234", tls: true, origin: "https://pgbase.internal", wantStatus: http.StatusCreated, wantSecure: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &fakeAdminAuth{setupSession: validSession()}
			dependencies := testDependencies(auth, nil)
			dependencies.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
			handler := mustRouter(t, dependencies)
			request := httptest.NewRequest(http.MethodPost, "http://pgbase.internal/api/admin/setup",
				strings.NewReader(`{"setupToken":"secret","email":"admin@example.com","password":"correct horse battery staple"}`))
			request.RemoteAddr = tt.remoteAddr
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", tt.origin)
			for _, forwarded := range tt.forwarded {
				request.Header.Add("X-Forwarded-Proto", forwarded)
			}
			if tt.tls {
				request.TLS = &tls.ConnectionState{}
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if tt.wantStatus == http.StatusCreated {
				cookies := response.Result().Cookies()
				if len(cookies) != 1 || cookies[0].Secure != tt.wantSecure {
					t.Fatalf("cookies=%+v", cookies)
				}
			}
		})
	}
}
