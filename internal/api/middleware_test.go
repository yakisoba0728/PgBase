package api

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAccessLogUsesRouteTemplateWithoutRawRequestData(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := NewRouter(Dependencies{Logger: logger})
	const (
		collectionSentinel = "private_collection_sentinel"
		recordSentinel     = "019551d0-e9c0-7000-8000-private404"
		querySentinel      = "private_query_sentinel"
	)
	request := httptest.NewRequest(http.MethodGet,
		"/api/v1/collections/"+collectionSentinel+"/records/"+recordSentinel+"?token="+querySentinel, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	encoded := logs.String()
	if !strings.Contains(encoded, `"route":"/api/v1/collections/{collection}/records/{id}"`) {
		t.Fatalf("access log missing route template: %s", encoded)
	}
	for _, sentinel := range []string{collectionSentinel, recordSentinel, querySentinel, "token="} {
		if strings.Contains(encoded, sentinel) {
			t.Fatalf("access log leaked raw request data %q: %s", sentinel, encoded)
		}
	}
}

func TestUnknownAPIResponsesAreNeverCacheable(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/admin/schema-operations/12345678-1234-4567-8901-123456789abc", nil)
	response := httptest.NewRecorder()
	NewRouter(Dependencies{}).ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestRecoveryDiscardsPartialResponseBeforeWriting500(t *testing.T) {
	handler := recoveryMiddleware(slog.New(slog.NewJSONHandler(io.Discard, nil)), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Partial", "must-not-leak")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("partial secret"))
		panic("boom")
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if strings.Contains(response.Body.String(), "partial secret") || response.Header().Get("X-Partial") != "" {
		t.Fatalf("partial response leaked: headers=%v body=%q", response.Header(), response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "internal_error")
}

func TestCORSRejectsWildcardConfiguration(t *testing.T) {
	handler := NewRouter(Dependencies{CORSAllowedOrigins: []string{"*"}})
	request := httptest.NewRequest(http.MethodOptions, "/health/live", nil)
	request.Header.Set("Origin", "https://app.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("allow origin = %q", got)
	}
}

func TestCORSRejectsUnlistedOrigin(t *testing.T) {
	handler := NewRouter(Dependencies{CORSAllowedOrigins: []string{"https://admin.example"}})
	request := httptest.NewRequest(http.MethodOptions, "/health/live", nil)
	request.Header.Set("Origin", "https://other.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Header().Get("Vary"), "Origin") {
		t.Fatalf("Vary = %q", response.Header().Get("Vary"))
	}
}

func TestCORSAnswersListedOriginPreflight(t *testing.T) {
	handler := NewRouter(Dependencies{CORSAllowedOrigins: []string{"https://admin.example"}})
	request := httptest.NewRequest(http.MethodOptions, "/health/live", nil)
	request.Header.Set("Origin", "https://admin.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	request.Header.Set("Access-Control-Request-Headers", "Content-Type, Idempotency-Key, If-Match")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "https://admin.example" {
		t.Fatalf("allow origin = %q", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("allow credentials = %q", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Idempotency-Key") || !strings.Contains(got, "If-Match") {
		t.Fatalf("allow headers = %q", got)
	}
	if got := response.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(got, "ETag") ||
		!strings.Contains(got, "Idempotency-Replayed") || !strings.Contains(got, "PgBase-Compatibility") ||
		!strings.Contains(got, "Location") || strings.Contains(got, "Deprecation") {
		t.Fatalf("expose headers = %q", got)
	}
}

func TestCORSAllowlistDoesNotGrantCrossOriginMutationAuthority(t *testing.T) {
	session := validSession()
	auth := &fakeAdminAuth{authenticateAdmin: session.Administrator}
	dependencies := testDependencies(auth, mustURL("https://api.example"))
	dependencies.CORSAllowedOrigins = []string{"https://admin.example"}
	handler := NewRouter(dependencies)

	preflight := httptest.NewRequest(http.MethodOptions, "/api/admin/auth/logout", nil)
	preflight.Header.Set("Origin", "https://admin.example")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Access-Control-Request-Headers", "X-PgBase-CSRF")
	preflightResponse := httptest.NewRecorder()
	handler.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent {
		t.Fatalf("preflight status=%d body=%s", preflightResponse.Code, preflightResponse.Body.String())
	}

	mutation := httptest.NewRequest(http.MethodPost, "/api/admin/auth/logout", nil)
	mutation.Header.Set("Origin", "https://admin.example")
	mutation.Header.Set("X-PgBase-CSRF", validCSRF())
	mutation.AddCookie(&http.Cookie{Name: adminCookieName, Value: session.RawToken})
	mutationResponse := httptest.NewRecorder()
	handler.ServeHTTP(mutationResponse, mutation)
	if mutationResponse.Code != http.StatusForbidden {
		t.Fatalf("mutation status=%d body=%s", mutationResponse.Code, mutationResponse.Body.String())
	}
	assertErrorCode(t, mutationResponse.Body.Bytes(), "origin_rejected")
	if auth.logoutCalls != 0 {
		t.Fatalf("Logout() calls = %d, want 0", auth.logoutCalls)
	}
}

func TestCORSResponseVaryIsCacheSafe(t *testing.T) {
	handler := NewRouter(Dependencies{CORSAllowedOrigins: []string{"https://admin.example"}})
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	request.Header.Set("Origin", "https://admin.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "https://admin.example" {
		t.Fatalf("allow origin = %q", got)
	}
	if !strings.Contains(response.Header().Get("Vary"), "Origin") {
		t.Fatalf("Vary = %q", response.Header().Get("Vary"))
	}
}

// TestBodyLimitAppliesToEveryContentTypeSpelling pins the body cap to every
// request instead of to a Content-Type prefix match. decodeStrictJSON accepts a
// media type through mime.ParseMediaType, which trims Unicode spaces such as
// U+00A0, so a prefix gate here used to skip MaxBytesReader for a header the
// decoder still read as application/json.
func TestBodyLimitAppliesToEveryContentTypeSpelling(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
	}{
		{name: "canonical", contentType: "application/json"},
		{name: "absent", contentType: ""},
		{name: "no break space prefix", contentType: "\u00a0application/json"},
		{name: "next line prefix", contentType: "\u0085application/json"},
		{name: "vertical tab prefix", contentType: "\vapplication/json"},
		{name: "unrelated media type", contentType: "text/plain"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			var read int
			var readErr error
			handler := bodyLimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				raw, err := io.ReadAll(r.Body)
				read, readErr = len(raw), err
			}))
			request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/login",
				strings.NewReader(strings.Repeat("x", maxJSONBodyBytes+1)))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			handler.ServeHTTP(httptest.NewRecorder(), request)

			var oversized *http.MaxBytesError
			if !errors.As(readErr, &oversized) {
				t.Fatalf("read error = %v, want *http.MaxBytesError", readErr)
			}
			if read > maxJSONBodyBytes {
				t.Fatalf("handler read %d bytes, want at most %d", read, maxJSONBodyBytes)
			}
		})
	}
}
