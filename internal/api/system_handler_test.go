package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeSystemStatus struct{ status SystemStatus }

func (f fakeSystemStatus) Status(context.Context) SystemStatus { return f.status }

func TestSystemStatusRequiresAdministratorSession(t *testing.T) {
	handler := NewRouter(Dependencies{System: fakeSystemStatus{status: SystemStatus{Version: "v1"}}})
	request := httptest.NewRequest(http.MethodGet, "/api/admin/system", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	assertErrorCode(t, response.Body.Bytes(), "authentication_required")
}

func TestSystemStatusReturnsInjectedRedactedReadOnlyStatus(t *testing.T) {
	status := SystemStatus{Version: "v1.2.3", DatabaseReachable: true, MigrationCurrent: 4, MigrationLatest: 4, MigrationDirty: true, CatalogVersion: 9, ListenerReady: true}
	auth := &fakeAdminAuth{authenticateAdmin: validSession().Administrator}
	dependencies := testDependencies(auth, nil)
	dependencies.System = fakeSystemStatus{status: status}
	handler := NewRouter(dependencies)
	request := httptest.NewRequest(http.MethodGet, "/api/admin/system", nil)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: "fixed-session-token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	var got SystemStatus
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got != status {
		t.Fatalf("status = %#v, want %#v", got, status)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"migrationDirty":true`)) {
		t.Fatalf("response omitted dirty migration state: %s", response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
}

func TestSystemStatusUsesCommonErrorEnvelopeWhenUnavailable(t *testing.T) {
	auth := &fakeAdminAuth{authenticateAdmin: validSession().Administrator}
	handler := NewRouter(testDependencies(auth, nil))
	request := httptest.NewRequest(http.MethodGet, "/api/admin/system", nil)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: "fixed-session-token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	assertErrorCode(t, response.Body.Bytes(), "internal_error")
}
