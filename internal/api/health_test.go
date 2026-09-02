package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type failingReadiness struct{}

func (failingReadiness) Ready(context.Context) error { return errors.New("db down") }

func TestReadyFailsWhenDependencyFails(t *testing.T) {
	handler := mustRouter(t, Dependencies{
		Readiness: failingReadiness{},
	})
	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
	assertErrorCode(t, response.Body.Bytes(), "not_ready")
	for _, secret := range []string{"db down", "SQLSTATE", "postgres://"} {
		if got := response.Body.String(); strings.Contains(got, secret) {
			t.Fatalf("response exposed %q: %q", secret, got)
		}
	}
}

func TestLiveReportsProcessHealth(t *testing.T) {
	handler := mustRouter(t, Dependencies{})
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestReadySucceedsWithoutDependencies(t *testing.T) {
	handler := mustRouter(t, Dependencies{})
	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
}
