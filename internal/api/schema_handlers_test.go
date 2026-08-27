package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/adminauth"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func TestSchemaPlanRouteReturnsNormalizedPreviewWithAuthenticationOnly(t *testing.T) {
	auth := &fakeAdminAuth{authenticateAdmin: validSession().Administrator}
	schemas := &fakeSchemaService{plan: schema.Plan{
		Operation:   "collection_create",
		BaseVersion: 7,
		Checksum:    "fixed-checksum",
		Definition: schema.PlannedCollection{
			Name: "posts", TableName: "posts",
			Fields: []schema.PlannedField{{Name: "title", Type: schema.TypeText, Required: true, Ordinal: 0}},
		},
		Statements: []string{`CREATE TABLE "app"."posts" (...)`},
	}}
	handler := NewRouter(schemaTestDependencies(auth, schemas))
	request := httptest.NewRequest(http.MethodPost, "/api/admin/collections/plan", strings.NewReader(`{
		"name":"posts","fields":[{"name":"title","type":"text","required":true}]
	}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: "fixed-session-token"})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	var body struct {
		BaseVersion int64                    `json:"baseVersion"`
		Checksum    string                   `json:"checksum"`
		Definition  schema.PlannedCollection `json:"definition"`
		SQL         []string                 `json:"sql"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.BaseVersion != 7 || body.Checksum != "fixed-checksum" || body.Definition.Name != "posts" || len(body.SQL) != 1 {
		t.Fatalf("plan response = %+v", body)
	}
	if schemas.planCalls != 1 || schemas.planInput.Name != "posts" || auth.authenticateCalls != 1 {
		t.Fatalf("plan calls=%d input=%+v auth calls=%d", schemas.planCalls, schemas.planInput, auth.authenticateCalls)
	}
}

func TestSchemaApplyRouteRequiresCSRFAndUsesAuthenticatedAdministrator(t *testing.T) {
	administrator := validSession().Administrator
	auth := &fakeAdminAuth{authenticateAdmin: administrator}
	createdAt := time.Date(2026, time.August, 28, 4, 5, 6, 0, time.UTC)
	schemas := &fakeSchemaService{applied: schema.Collection{
		ID: uuid.MustParse("019551d0-e9c0-7000-8000-000000000010"), Name: "posts", TableName: "posts", CreatedAt: createdAt,
	}}
	handler := NewRouter(schemaTestDependencies(auth, schemas))
	body := `{"name":"posts","fields":[{"name":"title","type":"text"}],"checksum":"fixed-checksum"}`

	missingCSRF := httptest.NewRequest(http.MethodPost, "/api/admin/collections/apply", strings.NewReader(body))
	missingCSRF.Host = "admin.example.com"
	missingCSRF.Header.Set("Content-Type", "application/json")
	missingCSRF.Header.Set("Origin", "https://admin.example.com")
	missingCSRF.AddCookie(&http.Cookie{Name: adminCookieName, Value: "fixed-session-token"})
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missingCSRF)
	if missingResponse.Code != http.StatusForbidden || schemas.applyCalls != 0 {
		t.Fatalf("missing CSRF status=%d apply calls=%d body=%s", missingResponse.Code, schemas.applyCalls, missingResponse.Body.String())
	}
	assertErrorCode(t, missingResponse.Body.Bytes(), "csrf_rejected")

	request := httptest.NewRequest(http.MethodPost, "/api/admin/collections/apply", strings.NewReader(body))
	request.Host = "admin.example.com"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://admin.example.com")
	request.Header.Set("X-PgBase-CSRF", adminauth.DeriveCSRFToken(testTokenSecret, "fixed-session-token"))
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: "fixed-session-token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if schemas.applyCalls != 1 || schemas.applyInput.Name != "posts" || schemas.applyChecksum != "fixed-checksum" || schemas.applyAdministratorID != administrator.ID {
		t.Fatalf("apply call input=%+v checksum=%q administrator=%s", schemas.applyInput, schemas.applyChecksum, schemas.applyAdministratorID)
	}
	var responseBody struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &responseBody); err != nil {
		t.Fatal(err)
	}
	if responseBody.ID != schemas.applied.ID || responseBody.Name != "posts" {
		t.Fatalf("apply response = %+v", responseBody)
	}
}

func TestSchemaCatalogRouteReturnsVersionAndDefinitions(t *testing.T) {
	auth := &fakeAdminAuth{authenticateAdmin: validSession().Administrator}
	schemas := &fakeSchemaService{catalog: schema.Catalog{Version: 3, Collections: map[string]schema.Collection{
		"posts": {Name: "posts", TableName: "posts", Fields: []schema.Field{{Name: "title", ColumnName: "title", Type: schema.TypeText}}},
	}}}
	handler := NewRouter(schemaTestDependencies(auth, schemas))
	request := httptest.NewRequest(http.MethodGet, "/api/admin/collections", nil)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: "fixed-session-token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Version     int64                      `json:"version"`
		Collections map[string]json.RawMessage `json:"collections"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Version != 3 || len(body.Collections) != 1 || body.Collections["posts"] == nil {
		t.Fatalf("catalog response = %+v", body)
	}
}

func TestSchemaRoutesRequireAuthenticationAndMapSafeErrors(t *testing.T) {
	t.Run("all routes require authentication", func(t *testing.T) {
		handler := NewRouter(schemaTestDependencies(&fakeAdminAuth{}, &fakeSchemaService{}))
		for _, test := range []struct {
			method string
			path   string
			body   string
		}{
			{http.MethodPost, "/api/admin/collections/plan", `{"name":"posts"}`},
			{http.MethodPost, "/api/admin/collections/apply", `{"name":"posts","checksum":"x"}`},
			{http.MethodGet, "/api/admin/collections", ""},
		} {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			if test.method == http.MethodPost && strings.HasSuffix(test.path, "/apply") {
				request.Header.Set("Origin", "https://admin.example.com")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s status=%d body=%s", test.method, test.path, response.Code, response.Body.String())
			}
			assertErrorCode(t, response.Body.Bytes(), "authentication_required")
		}
	})

	t.Run("validation details are safe", func(t *testing.T) {
		auth := &fakeAdminAuth{authenticateAdmin: validSession().Administrator}
		schemas := &fakeSchemaService{planErr: &schema.ValidationError{Details: []schema.ValidationDetail{{Field: "fields.0.type", Code: "unsupported_type"}}}}
		handler := NewRouter(schemaTestDependencies(auth, schemas))
		request := httptest.NewRequest(http.MethodPost, "/api/admin/collections/plan", strings.NewReader(`{"name":"posts"}`))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: adminCookieName, Value: "fixed-session-token"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		assertErrorCode(t, response.Body.Bytes(), "validation_failed")
	})

	t.Run("conflicts are stable", func(t *testing.T) {
		auth := &fakeAdminAuth{authenticateAdmin: validSession().Administrator}
		schemas := &fakeSchemaService{applyErr: schema.ErrConflict}
		handler := NewRouter(schemaTestDependencies(auth, schemas))
		request := httptest.NewRequest(http.MethodPost, "/api/admin/collections/apply", strings.NewReader(`{"name":"posts","checksum":"x"}`))
		request.Host = "admin.example.com"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "https://admin.example.com")
		request.Header.Set("X-PgBase-CSRF", adminauth.DeriveCSRFToken(testTokenSecret, "fixed-session-token"))
		request.AddCookie(&http.Cookie{Name: adminCookieName, Value: "fixed-session-token"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusConflict {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		assertErrorCode(t, response.Body.Bytes(), "schema_conflict")
	})
}

type fakeSchemaService struct {
	plan                 schema.Plan
	planErr              error
	applied              schema.Collection
	applyErr             error
	catalog              schema.Catalog
	catalogErr           error
	planCalls            int
	applyCalls           int
	catalogCalls         int
	planInput            schema.CreateCollectionInput
	applyInput           schema.CreateCollectionInput
	applyChecksum        string
	applyAdministratorID uuid.UUID
}

func (f *fakeSchemaService) PlanCreate(_ context.Context, input schema.CreateCollectionInput) (schema.Plan, error) {
	f.planCalls++
	f.planInput = input
	return f.plan, f.planErr
}

func (f *fakeSchemaService) ApplyCreate(_ context.Context, input schema.CreateCollectionInput, checksum string, administratorID uuid.UUID) (schema.Collection, error) {
	f.applyCalls++
	f.applyInput = input
	f.applyChecksum = checksum
	f.applyAdministratorID = administratorID
	return f.applied, f.applyErr
}

func (f *fakeSchemaService) Catalog(context.Context) (schema.Catalog, error) {
	f.catalogCalls++
	return f.catalog, f.catalogErr
}

func schemaTestDependencies(auth AdminAuth, schemas SchemaService) Dependencies {
	return Dependencies{
		AdminAuth:   auth,
		Schema:      schemas,
		PublicURL:   mustURL("https://admin.example.com"),
		TokenSecret: testTokenSecret,
	}
}
