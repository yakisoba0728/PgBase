package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestMetadataOnlySchemaPlanSerializesEmptySQLArray(t *testing.T) {
	auth := &fakeAdminAuth{authenticateAdmin: validSession().Administrator}
	schemas := &fakeSchemaService{plan: schema.Plan{
		BaseVersion: 11,
		Checksum:    "metadata-only-checksum",
		Definition: schema.PlannedCollection{
			Name: "posts", TableName: "posts",
			Fields: []schema.PlannedField{{Name: "headline", Type: schema.TypeText}},
		},
		Statements: nil,
	}}
	handler := mustRouter(t, schemaTestDependencies(auth, schemas))
	request := httptest.NewRequest(http.MethodPost, "/api/admin/collections/plan", strings.NewReader(`{"operation":"update","name":"posts","fields":[{"name":"headline","type":"text"}]}`))
	request.Header.Set("Content-Type", "application/json")
	protectSchemaMutationRequest(request)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: validSession().RawToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if string(body["sql"]) != "[]" {
		t.Fatalf("sql=%s body=%s", body["sql"], response.Body.String())
	}
}

func TestSQLViewUpdatePlanAcceptsOnlyDefinition(t *testing.T) {
	auth := &fakeAdminAuth{authenticateAdmin: validSession().Administrator}
	schemas := &fakeSchemaService{plan: schema.Plan{BaseVersion: 4, Checksum: "sql-view-update", Definition: schema.PlannedCollection{Name: "totals", TableName: "totals"}}}
	handler := mustRouter(t, schemaTestDependencies(auth, schemas))
	request := httptest.NewRequest(http.MethodPost, "/api/admin/collections/plan", strings.NewReader(`{"operation":"update","name":"totals","view":{"sql":"SELECT id, total FROM app.source"}}`))
	request.Header.Set("Content-Type", "application/json")
	protectSchemaMutationRequest(request)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: validSession().RawToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	want := schema.NewUpdateChange(schema.UpdateCollectionInput{Name: "totals", View: &schema.ViewInput{SQL: "SELECT id, total FROM app.source"}})
	if response.Code != http.StatusOK || !reflect.DeepEqual(schemas.planChange, want) {
		t.Fatalf("status=%d change=%+v body=%s", response.Code, schemas.planChange, response.Body.String())
	}
}

func TestSQLViewQueryAliasMapsToViewSQLAndRejectsAmbiguity(t *testing.T) {
	auth := &fakeAdminAuth{authenticateAdmin: validSession().Administrator}
	schemas := &fakeSchemaService{plan: schema.Plan{BaseVersion: 4, Checksum: "sql-view-alias", Definition: schema.PlannedCollection{Name: "totals", TableName: "totals"}}}
	handler := mustRouter(t, schemaTestDependencies(auth, schemas))
	for _, body := range []string{
		`{"operation":"create","name":"totals","fields":[],"viewQuery":"SELECT id, total FROM app.source"}`,
		`{"operation":"update","name":"totals","viewQuery":"SELECT id, total FROM app.source"}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/admin/collections/plan", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		protectSchemaMutationRequest(request)
		request.AddCookie(&http.Cookie{Name: adminCookieName, Value: validSession().RawToken})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("body=%s status=%d response=%s", body, response.Code, response.Body.String())
		}
	}
	want := schema.NewUpdateChange(schema.UpdateCollectionInput{Name: "totals", View: &schema.ViewInput{SQL: "SELECT id, total FROM app.source"}})
	if !reflect.DeepEqual(schemas.planChange, want) {
		t.Fatalf("viewQuery change=%+v", schemas.planChange)
	}
	conflict := httptest.NewRequest(http.MethodPost, "/api/admin/collections/plan", strings.NewReader(`{"operation":"update","name":"totals","view":{"sql":"SELECT 1"},"viewQuery":"SELECT 1"}`))
	conflict.Header.Set("Content-Type", "application/json")
	protectSchemaMutationRequest(conflict)
	conflict.AddCookie(&http.Cookie{Name: adminCookieName, Value: validSession().RawToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, conflict)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("ambiguous view status=%d body=%s", response.Code, response.Body.String())
	}
}
