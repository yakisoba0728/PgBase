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
	"github.com/jackc/pgx/v5/pgconn"
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
		"operation":"create","name":"posts","fields":[{"name":"title","type":"text","required":true}]
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
	body := `{"operation":"create","name":"posts","fields":[{"name":"title","type":"text"}],"checksum":"fixed-checksum","baseVersion":7}`

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

	missingBaseVersion := httptest.NewRequest(http.MethodPost, "/api/admin/collections/apply", strings.NewReader(`{"operation":"create","name":"posts","fields":[],"checksum":"fixed-checksum"}`))
	missingBaseVersion.Host = "admin.example.com"
	missingBaseVersion.Header.Set("Content-Type", "application/json")
	missingBaseVersion.Header.Set("Origin", "https://admin.example.com")
	missingBaseVersion.Header.Set("X-PgBase-CSRF", adminauth.DeriveCSRFToken(testTokenSecret, "fixed-session-token"))
	missingBaseVersion.AddCookie(&http.Cookie{Name: adminCookieName, Value: "fixed-session-token"})
	missingBaseResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingBaseResponse, missingBaseVersion)
	if missingBaseResponse.Code != http.StatusBadRequest || schemas.applyCalls != 0 {
		t.Fatalf("missing baseVersion status=%d apply calls=%d body=%s", missingBaseResponse.Code, schemas.applyCalls, missingBaseResponse.Body.String())
	}
	assertErrorCode(t, missingBaseResponse.Body.Bytes(), "invalid_request")

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
	if schemas.applyCalls != 1 || schemas.applyInput.Name != "posts" || schemas.applyChecksum != "fixed-checksum" || schemas.applyBaseVersion != 7 || schemas.applyAdministratorID != administrator.ID {
		t.Fatalf("apply call input=%+v checksum=%q baseVersion=%d administrator=%s", schemas.applyInput, schemas.applyChecksum, schemas.applyBaseVersion, schemas.applyAdministratorID)
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
		schemas := &fakeSchemaService{planErr: &schema.ValidationError{Details: []schema.ValidationDetail{{Field: "fields.0.default", Code: "invalid_default"}}}}
		handler := NewRouter(schemaTestDependencies(auth, schemas))
		request := httptest.NewRequest(http.MethodPost, "/api/admin/collections/plan", strings.NewReader(`{"operation":"create","name":"posts"}`))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: adminCookieName, Value: "fixed-session-token"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var body struct {
			Error struct {
				Code    string              `json:"code"`
				Message string              `json:"message"`
				Details map[string][]string `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Error.Code != "validation_failed" || body.Error.Message != "Schema input is invalid" ||
			len(body.Error.Details) != 1 || len(body.Error.Details["fields.0.default"]) != 1 || body.Error.Details["fields.0.default"][0] != "invalid_default" {
			t.Fatalf("validation response = %#v", body.Error)
		}
	})

	t.Run("conflicts are stable", func(t *testing.T) {
		auth := &fakeAdminAuth{authenticateAdmin: validSession().Administrator}
		schemas := &fakeSchemaService{applyErr: schema.ErrConflict}
		handler := NewRouter(schemaTestDependencies(auth, schemas))
		request := httptest.NewRequest(http.MethodPost, "/api/admin/collections/apply", strings.NewReader(`{"operation":"create","name":"posts","checksum":"x","baseVersion":0}`))
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

	t.Run("retryable database errors are service unavailable", func(t *testing.T) {
		auth := &fakeAdminAuth{authenticateAdmin: validSession().Administrator}
		schemas := &fakeSchemaService{catalogErr: &pgconn.PgError{Code: "40P01", Message: "deadlock detected"}}
		handler := NewRouter(schemaTestDependencies(auth, schemas))
		request := httptest.NewRequest(http.MethodGet, "/api/admin/collections", nil)
		request.AddCookie(&http.Cookie{Name: adminCookieName, Value: "fixed-session-token"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "1" {
			t.Fatalf("status=%d retry-after=%q body=%s", response.Code, response.Header().Get("Retry-After"), response.Body.String())
		}
		assertErrorCode(t, response.Body.Bytes(), "schema_unavailable")
	})
}

func TestSchemaRoutesDispatchUpdateAndDeleteOperations(t *testing.T) {
	administrator := validSession().Administrator
	auth := &fakeAdminAuth{authenticateAdmin: administrator}
	createdAt := time.Date(2026, time.August, 28, 4, 5, 6, 0, time.UTC)
	schemas := &fakeSchemaService{applied: schema.Collection{
		ID: uuid.MustParse("019551d0-e9c0-7000-8000-000000000010"), Name: "posts", TableName: "posts", CreatedAt: createdAt,
	}}
	handler := NewRouter(schemaTestDependencies(auth, schemas))
	csrfToken := adminauth.DeriveCSRFToken(testTokenSecret, "fixed-session-token")
	authenticated := func(method, path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if method != http.MethodGet {
			request.Host = "admin.example.com"
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", "https://admin.example.com")
			request.Header.Set("X-PgBase-CSRF", csrfToken)
		}
		request.AddCookie(&http.Cookie{Name: adminCookieName, Value: "fixed-session-token"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	response := authenticated(http.MethodPost, "/api/admin/collections/plan", `{"operation":"update","name":"posts","fields":[{"name":"views","type":"numeric"}]}`)
	if response.Code != http.StatusOK || schemas.planCalls != 1 || schemas.planUpdateInput.Name != "posts" ||
		len(schemas.planUpdateInput.Fields) != 1 || schemas.planUpdateInput.Fields[0].Type != schema.TypeNumeric {
		t.Fatalf("update plan status=%d calls=%d input=%+v body=%s", response.Code, schemas.planCalls, schemas.planUpdateInput, response.Body.String())
	}

	response = authenticated(http.MethodPost, "/api/admin/collections/apply", `{"operation":"update","name":"posts","fields":[{"name":"views","type":"numeric"}],"checksum":"fixed-checksum","baseVersion":3}`)
	if response.Code != http.StatusOK || schemas.applyCalls != 1 || schemas.applyUpdateInput.Name != "posts" ||
		schemas.applyChecksum != "fixed-checksum" || schemas.applyBaseVersion != 3 || schemas.applyAdministratorID != administrator.ID {
		t.Fatalf("update apply status=%d calls=%d input=%+v body=%s", response.Code, schemas.applyCalls, schemas.applyUpdateInput, response.Body.String())
	}

	response = authenticated(http.MethodPost, "/api/admin/collections/plan", `{"operation":"delete","name":"posts"}`)
	if response.Code != http.StatusOK || schemas.planCalls != 2 || schemas.planDeleteName != "posts" {
		t.Fatalf("delete plan status=%d calls=%d name=%q body=%s", response.Code, schemas.planCalls, schemas.planDeleteName, response.Body.String())
	}

	response = authenticated(http.MethodPost, "/api/admin/collections/apply", `{"operation":"delete","name":"posts","checksum":"fixed-checksum","baseVersion":3}`)
	if response.Code != http.StatusOK || schemas.applyCalls != 2 || schemas.deletedName != "posts" {
		t.Fatalf("delete apply status=%d calls=%d name=%q body=%s", response.Code, schemas.applyCalls, schemas.deletedName, response.Body.String())
	}
	var deleted struct {
		Name    string `json:"name"`
		Deleted bool   `json:"deleted"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &deleted); err != nil {
		t.Fatal(err)
	}
	if deleted.Name != "posts" || !deleted.Deleted {
		t.Fatalf("delete response = %+v", deleted)
	}

	response = authenticated(http.MethodPost, "/api/admin/collections/plan", `{"operation":"rename","name":"posts"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown operation status=%d body=%s", response.Code, response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "invalid_request")

	response = authenticated(http.MethodPost, "/api/admin/collections/plan", `{"name":"posts"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing operation status=%d body=%s", response.Code, response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "invalid_request")

	response = authenticated(http.MethodPost, "/api/admin/collections/plan", `{"operation":"update","name":"posts"}`)
	if response.Code != http.StatusBadRequest || schemas.planCalls != 2 {
		t.Fatalf("update without fields status=%d plan calls=%d body=%s", response.Code, schemas.planCalls, response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "invalid_request")

	response = authenticated(http.MethodPost, "/api/admin/collections/apply", `{"operation":"update","name":"posts","checksum":"fixed-checksum","baseVersion":3}`)
	if response.Code != http.StatusBadRequest || schemas.applyCalls != 2 {
		t.Fatalf("update apply without fields status=%d apply calls=%d body=%s", response.Code, schemas.applyCalls, response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "invalid_request")

	response = authenticated(http.MethodPost, "/api/admin/collections/plan", `{"operation":"update","name":"posts","fields":[]}`)
	if response.Code != http.StatusOK || schemas.planCalls != 3 || len(schemas.planUpdateInput.Fields) != 0 {
		t.Fatalf("explicit empty fields status=%d plan calls=%d input=%+v body=%s", response.Code, schemas.planCalls, schemas.planUpdateInput, response.Body.String())
	}
}

func TestSchemaRoutesMapUnsupportedChangesAndMissingCollections(t *testing.T) {
	t.Run("unsupported schema change is unprocessable", func(t *testing.T) {
		auth := &fakeAdminAuth{authenticateAdmin: validSession().Administrator}
		schemas := &fakeSchemaService{planErr: &schema.ValidationError{Details: []schema.ValidationDetail{{Field: "fields.0.type", Code: "unsupported_schema_change"}}}}
		handler := NewRouter(schemaTestDependencies(auth, schemas))
		request := httptest.NewRequest(http.MethodPost, "/api/admin/collections/plan", strings.NewReader(`{"operation":"update","name":"posts","fields":[]}`))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: adminCookieName, Value: "fixed-session-token"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		assertErrorCode(t, response.Body.Bytes(), "validation_failed")
	})

	t.Run("unknown collections are not found", func(t *testing.T) {
		auth := &fakeAdminAuth{authenticateAdmin: validSession().Administrator}
		schemas := &fakeSchemaService{planErr: schema.ErrCollectionNotFound}
		handler := NewRouter(schemaTestDependencies(auth, schemas))
		request := httptest.NewRequest(http.MethodPost, "/api/admin/collections/plan", strings.NewReader(`{"operation":"delete","name":"ghosts"}`))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: adminCookieName, Value: "fixed-session-token"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		assertErrorCode(t, response.Body.Bytes(), "not_found")
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
	applyBaseVersion     int64
	applyAdministratorID uuid.UUID
	planUpdateInput      schema.UpdateCollectionInput
	applyUpdateInput     schema.UpdateCollectionInput
	planDeleteName       string
	applyDeleteName      string
	deletedName          string
	applyDeleted         bool
}

func (f *fakeSchemaService) PlanCreate(_ context.Context, input schema.CreateCollectionInput) (schema.Plan, error) {
	f.planCalls++
	f.planInput = input
	return f.plan, f.planErr
}

func (f *fakeSchemaService) ApplyCreate(_ context.Context, input schema.CreateCollectionInput, checksum string, baseVersion int64, administratorID uuid.UUID) (schema.Collection, error) {
	f.applyCalls++
	f.applyInput = input
	f.applyChecksum = checksum
	f.applyBaseVersion = baseVersion
	f.applyAdministratorID = administratorID
	return f.applied, f.applyErr
}

func (f *fakeSchemaService) PlanUpdate(_ context.Context, input schema.UpdateCollectionInput) (schema.Plan, error) {
	f.planCalls++
	f.planUpdateInput = input
	return f.plan, f.planErr
}

func (f *fakeSchemaService) ApplyUpdate(_ context.Context, input schema.UpdateCollectionInput, checksum string, baseVersion int64, administratorID uuid.UUID) (schema.Collection, error) {
	f.applyCalls++
	f.applyUpdateInput = input
	f.applyChecksum = checksum
	f.applyBaseVersion = baseVersion
	f.applyAdministratorID = administratorID
	return f.applied, f.applyErr
}

func (f *fakeSchemaService) PlanDelete(_ context.Context, name string) (schema.Plan, error) {
	f.planCalls++
	f.planDeleteName = name
	return f.plan, f.planErr
}

func (f *fakeSchemaService) ApplyDelete(_ context.Context, name string, checksum string, baseVersion int64, administratorID uuid.UUID) error {
	f.applyCalls++
	f.applyDeleteName = name
	f.applyChecksum = checksum
	f.applyBaseVersion = baseVersion
	f.applyAdministratorID = administratorID
	f.deletedName = name
	f.applyDeleted = true
	return f.applyErr
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
