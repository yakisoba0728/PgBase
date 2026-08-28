package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
	request.Header.Set("Access-Control-Request-Headers", "Content-Type")
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
