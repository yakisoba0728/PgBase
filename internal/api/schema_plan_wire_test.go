package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/webisnoteasy/PgBase/internal/schema"
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
