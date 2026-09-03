package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type fakeSystemStatus struct{ status SystemStatus }

func (f fakeSystemStatus) Status(context.Context) SystemStatus { return f.status }

func TestSystemStatusRequiresAdministratorSession(t *testing.T) {
	handler := mustRouter(t, Dependencies{System: fakeSystemStatus{status: SystemStatus{Version: "v1"}}})
	request := httptest.NewRequest(http.MethodGet, "/api/admin/system", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	assertErrorCode(t, response.Body.Bytes(), "authentication_required")
}

func TestSystemStatusReturnsInjectedRedactedReadOnlyStatus(t *testing.T) {
	status := SystemStatus{
		Version: "v1.2.3", DatabaseReachable: true, MigrationCurrent: 4, MigrationLatest: 4,
		MigrationDirty: true, CatalogVersion: 9, ListenerReady: true,
		Unavailable:   []SystemStatusProbe{{Component: "listener", Code: "unavailable"}},
		SampledAt:     "2026-09-01T00:00:00Z",
		LastSuccessAt: "2026-08-31T23:59:00Z",
	}
	auth := &fakeAdminAuth{authenticateAdmin: validSession().Administrator}
	dependencies := testDependencies(auth, nil)
	dependencies.System = fakeSystemStatus{status: status}
	handler := mustRouter(t, dependencies)
	request := httptest.NewRequest(http.MethodGet, "/api/admin/system", nil)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	var got SystemStatus
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, status) {
		t.Fatalf("status = %#v, want %#v", got, status)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"unavailable":[{"component":"listener","code":"unavailable"}]`)) {
		t.Fatalf("response omitted the unavailable probe: %s", response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"migrationDirty":true`)) {
		t.Fatalf("response omitted dirty migration state: %s", response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
}

func TestRouterRefusesToBuildWithoutEverySystemModule(t *testing.T) {
	// A missing module used to reach the handler and answer an internal error
	// on every request. Wiring is decided once, so it fails once, at
	// construction, and names what the caller forgot.
	for _, testCase := range []struct {
		name string
		want string
	}{
		{name: "Readiness", want: "Readiness"},
		{name: "AdminAuth", want: "AdminAuth"},
		{name: "Schema", want: "Schema"},
		{name: "Records", want: "Records"},
		{name: "System", want: "System"},
		{name: "SchemaHealth", want: "SchemaHealth"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dependencies := completeDependencies(Dependencies{})
			switch testCase.name {
			case "Readiness":
				dependencies.Readiness = nil
			case "AdminAuth":
				dependencies.AdminAuth = nil
			case "Schema":
				dependencies.Schema = nil
			case "Records":
				dependencies.Records = nil
			case "System":
				dependencies.System = nil
			case "SchemaHealth":
				dependencies.SchemaHealth = nil
			}
			handler, err := NewRouter(dependencies)
			if err == nil || handler != nil {
				t.Fatalf("NewRouter without %s = %v, %v, want a construction error", testCase.name, handler, err)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("construction error = %q, want it to name %s", err, testCase.want)
			}
		})
	}
}
