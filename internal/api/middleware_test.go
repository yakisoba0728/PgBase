package api

import (
	"bytes"
	"context"
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
	handler := mustRouter(t, Dependencies{Logger: logger})
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

// An atomic route holds its whole response, so a panic must unwind past a
// buffer that is never committed and leave the recovery handler a connection
// nothing has reached. This composes the two exactly as NewRouter does.
func TestAtomicResponseModeDiscardsPartialResponseBeforeWriting500(t *testing.T) {
	handler := recoveryMiddleware(slog.New(slog.NewJSONHandler(io.Discard, nil)),
		applyResponseMode(atomicJSON(1<<10), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Partial", "must-not-leak")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("partial secret"))
			panic("boom")
		})))
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

// A response over its cap must be refused before the client can observe any
// part of it: not the status the handler chose, not a header it set, and not
// one byte of the oversized body.
func TestAtomicResponseCapRefusesBeforeAnyHeaderOrBodyIsCommitted(t *testing.T) {
	const limit = 64
	handler := applyResponseMode(atomicJSON(limit), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"must-not-leak"`)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(bytes.Repeat([]byte("Q"), limit+1))
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/atomic", nil))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if got := response.Header().Get("ETag"); got != "" {
		t.Fatalf("handler header survived an overflowing response: ETag = %q", got)
	}
	if strings.Contains(response.Body.String(), "QQ") {
		t.Fatalf("partial oversized body reached the client: %q", response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "response_too_large")
}

// The cap is a memory bound, not a stricter budget than the kernel's: a body
// of exactly the declared cap has to be delivered whole.
func TestAtomicResponseCapAdmitsABodyAtExactlyTheCap(t *testing.T) {
	const limit = 64
	handler := applyResponseMode(atomicJSON(limit), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("s"), limit))
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/atomic", nil))

	if response.Code != http.StatusOK || response.Body.Len() != limit {
		t.Fatalf("status = %d body = %d bytes, want %d and %d", response.Code, response.Body.Len(), http.StatusOK, limit)
	}
}

// A cap-exceeding write has to fail the handler too. A handler that kept
// producing body after the router had already decided to refuse it would spend
// the memory the cap exists to refuse.
func TestAtomicResponseCapFailsTheHandlerWriteItRefuses(t *testing.T) {
	var (
		firstErr  error
		secondErr error
	)
	handler := applyResponseMode(atomicJSON(8), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, firstErr = w.Write(bytes.Repeat([]byte("s"), 9))
		_, secondErr = w.Write([]byte("s"))
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/atomic", nil))

	if !errors.Is(firstErr, errResponseTooLarge) || !errors.Is(secondErr, errResponseTooLarge) {
		t.Fatalf("handler writes = %v, %v, want both %v", firstErr, secondErr, errResponseTooLarge)
	}
}

// A passthrough route streams. Its bytes must reach the connection while the
// handler is still running, which is the property that makes it unsuitable for
// buffering and the reason it declares no cap.
func TestPassthroughResponseModeStreamsToTheConnection(t *testing.T) {
	response := httptest.NewRecorder()
	streamed := false
	handler := applyResponseMode(passthrough(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("first chunk"))
		streamed = response.Body.String() == "first chunk"
	}))
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/stream", nil))

	if !streamed {
		t.Fatalf("passthrough route buffered instead of streaming: %q", response.Body.String())
	}
}

// http.ResponseController.Flush walks Unwrap, and the buffer holds the real
// connection so a write deadline can reach it. Without a no-op Flush the walk
// would commit a 200 header on the connection for a body the buffer has not
// decided to send, which is exactly the partial header the cap forbids.
func TestAtomicResponseModeDoesNotLetAFlushCommitTheConnection(t *testing.T) {
	response := httptest.NewRecorder()
	handler := applyResponseMode(atomicJSON(1<<10), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush an atomic response: %v", err)
		}
		if response.Flushed {
			t.Error("flush reached the connection before the buffer committed")
		}
		w.WriteHeader(http.StatusCreated)
	}))
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/atomic", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d from the first write", response.Code, http.StatusOK)
	}
}

// oversizedReadinessError makes a route produce a legal-looking body larger
// than the cap its registry entry declares, which is the only way to observe
// from outside that NewRouter actually installs the declared response policy.
type oversizedReadinessError struct{ components []string }

func (oversizedReadinessError) Error() string { return "not ready" }

func (e oversizedReadinessError) Components() []string { return e.components }

type unreadyWith struct{ err error }

func (r unreadyWith) Ready(context.Context) error { return r.err }

// The cap only protects the server if the router installs it. This drives a
// real registered route end to end, so removing the response policy from
// NewRouter is caught here rather than only in the wrapper's own unit tests.
func TestRouterEnforcesTheDeclaredCapOnARegisteredRoute(t *testing.T) {
	components := make([]string, 0, 4096)
	for len(components) < 4096 {
		components = append(components, strings.Repeat("component-sentinel", 2))
	}
	handler := mustRouter(t, Dependencies{
		Readiness: unreadyWith{err: oversizedReadinessError{components: components}},
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body = %d bytes", response.Code, http.StatusInternalServerError, response.Body.Len())
	}
	if response.Body.Len() > maximumHealthResponseBytes {
		t.Fatalf("refused response body = %d bytes, want under the %d byte cap", response.Body.Len(), maximumHealthResponseBytes)
	}
	if strings.Contains(response.Body.String(), "component-sentinel") {
		t.Fatalf("oversized body reached the client: %q", response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "response_too_large")
}

func TestCORSRejectsWildcardConfiguration(t *testing.T) {
	handler := mustRouter(t, Dependencies{CORSAllowedOrigins: []string{"*"}})
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
	handler := mustRouter(t, Dependencies{CORSAllowedOrigins: []string{"https://admin.example"}})
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
	handler := mustRouter(t, Dependencies{CORSAllowedOrigins: []string{"https://admin.example"}})
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
	if got := response.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("allow credentials = %q, want no credentialed CORS", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Idempotency-Key") || !strings.Contains(got, "If-Match") {
		t.Fatalf("allow headers = %q", got)
	}
	if got := response.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(got, "ETag") ||
		!strings.Contains(got, "Idempotency-Replayed") || !strings.Contains(got, "PgBase-Compatibility") ||
		!strings.Contains(got, "Location") || !strings.Contains(got, "Retry-After") || strings.Contains(got, "Deprecation") {
		t.Fatalf("expose headers = %q", got)
	}
}

func TestCORSAllowlistDoesNotGrantCrossOriginMutationAuthority(t *testing.T) {
	session := validSession()
	auth := &fakeAdminAuth{authenticateAdmin: session.Administrator}
	dependencies := testDependencies(auth, mustURL("https://api.example"))
	dependencies.CORSAllowedOrigins = []string{"https://admin.example"}
	handler := mustRouter(t, dependencies)

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

func TestCORSAllowlistDoesNotGrantCookieAuthenticatedReadAuthority(t *testing.T) {
	session := validSession()
	auth := &fakeAdminAuth{authenticateAdmin: session.Administrator}
	dependencies := testDependencies(auth, mustURL("https://api.example.com"))
	dependencies.CORSAllowedOrigins = []string{"https://reports.example.com"}
	handler := mustRouter(t, dependencies)

	request := httptest.NewRequest(http.MethodGet, "/api/admin/session", nil)
	request.Header.Set("Origin", "https://reports.example.com")
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: session.RawToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "origin_rejected")
	if auth.authenticateCalls != 0 {
		t.Fatalf("Authenticate() calls = %d, want 0 before origin rejection", auth.authenticateCalls)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "https://reports.example.com" {
		t.Fatalf("allow origin = %q", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("allow credentials = %q, want empty", got)
	}
}

func TestCORSResponseVaryIsCacheSafe(t *testing.T) {
	handler := mustRouter(t, Dependencies{CORSAllowedOrigins: []string{"https://admin.example"}})
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
			handler := requestBodyLimitMiddleware(maxJSONBodyBytes, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
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
