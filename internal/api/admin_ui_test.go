package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRouterDelegatesOnlyAdminPathsToEmbeddedUI(t *testing.T) {
	ui := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("embedded-admin-ui"))
	})
	handler := mustRouter(t, Dependencies{AdminUI: ui})

	for _, path := range []string{"/admin", "/admin/", "/admin/collections"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != "embedded-admin-ui" {
			t.Fatalf("%s status=%d body=%q", path, response.Code, response.Body.String())
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/api/missing", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "embedded-admin-ui") || !strings.Contains(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("API status=%d content-type=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
}
