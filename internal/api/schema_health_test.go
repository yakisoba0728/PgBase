package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type fakeSchemaHealth struct{ health SchemaHealth }

func (f fakeSchemaHealth) Health(context.Context) SchemaHealth { return f.health }

func driftedHealth() SchemaHealth {
	return SchemaHealth{
		State: SchemaHealthDrift,
		Findings: []SchemaFinding{
			{Severity: "error", Code: "collection_index_missing", Message: "A catalog field index is missing"},
		},
		FindingCount:  1,
		Unavailable:   []SystemStatusProbe{},
		SampledAt:     "2026-09-01T00:05:00Z",
		LastSuccessAt: "2026-09-01T00:05:00Z",
	}
}

func schemaHealthRequest(origin string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/admin/system/health", nil)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: "fixed-session-token"})
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	return request
}

func authenticatedHealthRouter(t *testing.T, health SchemaHealth, corsOrigins ...string) http.Handler {
	t.Helper()
	dependencies := testDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, mustURL("https://admin.example.com"))
	dependencies.SchemaHealth = fakeSchemaHealth{health: health}
	dependencies.CORSAllowedOrigins = corsOrigins
	return mustRouter(t, dependencies)
}

func TestSchemaHealthRequiresAnAuthenticatedAdministrator(t *testing.T) {
	handler := mustRouter(t, Dependencies{SchemaHealth: fakeSchemaHealth{health: driftedHealth()}})
	request := httptest.NewRequest(http.MethodGet, "/api/admin/system/health", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	assertErrorCode(t, response.Body.Bytes(), "authentication_required")
	if strings.Contains(response.Body.String(), "collection_index_missing") {
		t.Fatalf("an unauthenticated reader saw the diagnosis: %s", response.Body.String())
	}
}

// A configured CORS allowance is what would otherwise let another site read
// this report with the administrator's own cookie, so the allowance is granted
// here and the read must still be refused.
func TestSchemaHealthRefusesACrossOriginReaderEvenWhenCORSAllowsTheOrigin(t *testing.T) {
	handler := authenticatedHealthRouter(t, driftedHealth(), "https://evil.example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, schemaHealthRequest("https://evil.example.com"))
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "origin_rejected")
	if strings.Contains(response.Body.String(), "collection_index_missing") {
		t.Fatalf("a cross-origin reader saw the diagnosis: %s", response.Body.String())
	}
}

// A browser sends no Origin on a same-origin GET and the deployment's own
// origin on the cases where it does send one, so both have to be admitted or
// the administrator page cannot read its own status.
func TestSchemaHealthAdmitsTheSameOriginReader(t *testing.T) {
	for _, origin := range []string{"", "https://admin.example.com"} {
		handler := authenticatedHealthRouter(t, driftedHealth())
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, schemaHealthRequest(origin))
		if response.Code != http.StatusOK {
			t.Fatalf("Origin %q: status = %d, body=%s", origin, response.Code, response.Body.String())
		}
	}
}

func TestSchemaHealthServesTheCachedRedactedDiagnosis(t *testing.T) {
	want := driftedHealth()
	want.Retries = 2
	want.Unavailable = []SystemStatusProbe{{Component: "doctor", Code: "timeout"}}
	handler := authenticatedHealthRouter(t, want)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, schemaHealthRequest(""))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	var got SchemaHealth
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("health = %#v, want %#v", got, want)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
}

// An empty diagnosis has to serialize as an empty list rather than null, or a
// client cannot iterate the finding list of a healthy database.
func TestSchemaHealthSerializesAnEmptyDiagnosisAsEmptyLists(t *testing.T) {
	handler := authenticatedHealthRouter(t, SchemaHealth{State: SchemaHealthHealthy, LastSuccessAt: "2026-09-01T00:05:00Z"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, schemaHealthRequest(""))
	body := response.Body.String()
	if !strings.Contains(body, `"findings":[]`) || !strings.Contains(body, `"unavailable":[]`) {
		t.Fatalf("healthy body = %s, want empty lists", body)
	}
	if !strings.Contains(body, `"state":"healthy"`) {
		t.Fatalf("healthy body = %s, want a healthy state", body)
	}
}

// The router refuses to deliver a body larger than the route declared, so the
// declared cap has to leave room for a full capped report. This is the check
// that would fail if the finding cap were raised without the budget.
func TestSchemaHealthResponseCapFitsAFullyCappedReport(t *testing.T) {
	health := SchemaHealth{
		State:        SchemaHealthDrift,
		FindingCount: MaximumSchemaFindings,
		Unavailable:  []SystemStatusProbe{{Component: "doctor", Code: "failed"}},
		SampledAt:    "2026-09-01T00:05:00Z",
	}
	for index := 0; index < MaximumSchemaFindings; index++ {
		health.Findings = append(health.Findings, SchemaFinding{
			Severity: "error",
			Code:     "relation_multi_edge_cardinality_trigger_mismatch",
			Message:  "A many relation edge cardinality trigger is missing or inconsistent",
		})
	}
	handler := authenticatedHealthRouter(t, health)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, schemaHealthRequest(""))
	if response.Code != http.StatusOK {
		t.Fatalf("a fully capped report was rejected by its own response cap: %d %s", response.Code, response.Body.String())
	}
	if response.Body.Len() > maximumSchemaHealthResponseBytes {
		t.Fatalf("capped report = %d bytes, over the declared %d", response.Body.Len(), maximumSchemaHealthResponseBytes)
	}
}
