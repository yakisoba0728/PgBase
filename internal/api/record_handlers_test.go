package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/webisnoteasy/PgBase/internal/query"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func TestRecordRoutesRequireAdministratorAndCSRF(t *testing.T) {
	records := &fakeRecordService{}
	unauthenticated := NewRouter(recordTestDependencies(&fakeAdminAuth{}, records))
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/collections/posts/records"},
		{http.MethodPost, "/api/v1/collections/posts/records"},
		{http.MethodGet, "/api/v1/collections/posts/records/aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"},
		{http.MethodPatch, "/api/v1/collections/posts/records/aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"},
		{http.MethodDelete, "/api/v1/collections/posts/records/aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"},
	} {
		request := httptest.NewRequest(route.method, route.path, nil)
		request.Host = "admin.example.com"
		if route.method == http.MethodPost || route.method == http.MethodPatch || route.method == http.MethodDelete {
			request.Header.Set("Origin", "https://admin.example.com")
		}
		response := httptest.NewRecorder()
		unauthenticated.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s %s status=%d body=%s", route.method, route.path, response.Code, response.Body.String())
		}
	}

	handler := NewRouter(recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))
	for _, route := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/collections/posts/records", `{}`},
		{http.MethodPatch, "/api/v1/collections/posts/records/aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", `{}`},
		{http.MethodDelete, "/api/v1/collections/posts/records/aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", ""},
	} {
		request := authenticatedRecordRequest(route.method, route.path, route.body, false)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("missing CSRF %s status=%d body=%s", route.method, response.Code, response.Body.String())
		}
	}
}

func TestRecordRoutesExposeFiveCRUDContracts(t *testing.T) {
	id := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	record := map[string]any{"id": id.String(), "title": "hello"}
	records := &fakeRecordService{page: query.RecordPage{Items: []map[string]any{record}, NextCursor: "next"}, record: record}
	handler := NewRouter(recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))

	tests := []struct {
		method, path, body string
		want               int
	}{
		{http.MethodGet, "/api/v1/collections/posts/records?limit=2&cursor=signed", "", http.StatusOK},
		{http.MethodPost, "/api/v1/collections/posts/records", `{"title":"hello"}`, http.StatusCreated},
		{http.MethodGet, "/api/v1/collections/posts/records/" + id.String(), "", http.StatusOK},
		{http.MethodPatch, "/api/v1/collections/posts/records/" + id.String(), `{"title":"updated"}`, http.StatusOK},
		{http.MethodDelete, "/api/v1/collections/posts/records/" + id.String(), "", http.StatusNoContent},
	}
	for _, test := range tests {
		request := authenticatedRecordRequest(test.method, test.path, test.body, true)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Errorf("%s %s status=%d body=%s", test.method, test.path, response.Code, response.Body.String())
		}
	}
	if records.listCollection != "posts" || records.listCursor != "signed" || records.listLimit != 2 || records.getID != id || records.updateID != id || records.deleteID != id {
		t.Fatalf("service calls=%+v", records)
	}
	request := authenticatedRecordRequest(http.MethodGet, "/api/v1/collections/posts/records?limit=999", "", true)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || records.listLimit != 200 {
		t.Fatalf("capped list status=%d limit=%d", response.Code, records.listLimit)
	}
	request = authenticatedRecordRequest(http.MethodGet, "/api/v1/collections/posts/records", "", true)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || records.listLimit != 50 {
		t.Fatalf("default list status=%d limit=%d", response.Code, records.listLimit)
	}
}

func TestRecordRoutesRejectMalformedInputs(t *testing.T) {
	handler := NewRouter(recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, &fakeRecordService{}))
	for _, test := range []struct {
		name, method, path, body, contentType string
	}{
		{"bad json", http.MethodPost, "/api/v1/collections/posts/records", `{`, "application/json"},
		{"wrong content type", http.MethodPatch, "/api/v1/collections/posts/records/aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", `{}`, "text/plain"},
		{"bad id", http.MethodGet, "/api/v1/collections/posts/records/not-a-uuid", "", ""},
		{"bad limit", http.MethodGet, "/api/v1/collections/posts/records?limit=nope", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := authenticatedRecordRequest(test.method, test.path, test.body, true)
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestRecordListForwardsFilterAndSortParams(t *testing.T) {
	records := &fakeRecordService{page: query.RecordPage{Items: []map[string]any{}}}
	handler := NewRouter(recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))
	request := authenticatedRecordRequest(http.MethodGet,
		"/api/v1/collections/posts/records?filter="+url.QueryEscape(`(title = 'x' && rank >= 2)`)+"&sort="+url.QueryEscape("-rank,created_at"), "", true)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if records.listFilter != `(title = 'x' && rank >= 2)` || records.listSort != "-rank,created_at" {
		t.Fatalf("forwarded params filter=%q sort=%q", records.listFilter, records.listSort)
	}

	records.listErr = &query.ValidationError{Details: []query.ValidationDetail{{Field: "filter", Code: "unknown_field"}}}
	request = authenticatedRecordRequest(http.MethodGet, "/api/v1/collections/posts/records?filter=ghost%20%3D%201", "", true)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("filter validation status=%d body=%s", response.Code, response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "validation_failed")
}

func TestRecordRoutesMapSafeProductErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
		code string
	}{
		{"collection missing", query.ErrCollectionNotFound, 404, "not_found"},
		{"record missing", query.ErrRecordNotFound, 404, "not_found"},
		{"unique", &pgconn.PgError{Code: "23505", Message: `duplicate SQL INSERT secret`}, 409, "record_conflict"},
		{"schema", schema.ErrConflict, 409, "schema_conflict"},
		{"validation", &query.ValidationError{Details: []query.ValidationDetail{{Field: "title", Code: "required"}}}, 422, "validation_failed"},
		{"timeout", context.DeadlineExceeded, 503, "record_unavailable"},
		{"cursor", &query.InvalidCursorError{}, 400, "invalid_cursor"},
		{"unknown", errors.New(`postgres://admin:secret@db/private SQLSTATE 99999 SELECT token`), 500, "internal_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			records := &fakeRecordService{getErr: test.err}
			handler := NewRouter(recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))
			request := authenticatedRecordRequest(http.MethodGet, "/api/v1/collections/posts/records/aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", "", true)
			request.Header.Set("X-Request-ID", "fixed_request_id")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertErrorCode(t, response.Body.Bytes(), test.code)
			if strings.Contains(response.Body.String(), "SQL") || strings.Contains(response.Body.String(), "secret") || !strings.Contains(response.Body.String(), "fixed_request_id") {
				t.Fatalf("unsafe or incomplete envelope: %s", response.Body.String())
			}
		})
	}
}

func TestRecordRoutesExposeRetryPolicyForTransientDatabaseFailure(t *testing.T) {
	records := &fakeRecordService{getErr: &pgconn.PgError{Code: "40001", Message: "serialization failure"}}
	handler := NewRouter(recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))
	request := authenticatedRecordRequest(http.MethodGet, "/api/v1/collections/posts/records/aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", "", true)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("status=%d retry-after=%q body=%s", response.Code, response.Header().Get("Retry-After"), response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "record_unavailable")
}

type fakeRecordService struct {
	page                                             query.RecordPage
	record                                           map[string]any
	listErr, getErr, createErr, updateErr, deleteErr error
	listCollection, listCursor, listFilter, listSort string
	listLimit                                        int
	getID, updateID, deleteID                        uuid.UUID
	createCalls                                      int
}

func (f *fakeRecordService) List(_ context.Context, collection, cursor string, limit int, filter, sort string) (query.RecordPage, error) {
	f.listCollection, f.listCursor, f.listLimit, f.listFilter, f.listSort = collection, cursor, limit, filter, sort
	return f.page, f.listErr
}
func (f *fakeRecordService) Get(_ context.Context, _ string, id uuid.UUID) (map[string]any, error) {
	f.getID = id
	return f.record, f.getErr
}
func (f *fakeRecordService) Create(_ context.Context, _ string, _ map[string]json.RawMessage) (map[string]any, error) {
	f.createCalls++
	return f.record, f.createErr
}
func (f *fakeRecordService) Update(_ context.Context, _ string, id uuid.UUID, _ map[string]json.RawMessage) (map[string]any, error) {
	f.updateID = id
	return f.record, f.updateErr
}
func (f *fakeRecordService) Delete(_ context.Context, _ string, id uuid.UUID) error {
	f.deleteID = id
	return f.deleteErr
}

func recordTestDependencies(auth AdminAuth, records RecordService) Dependencies {
	return Dependencies{AdminAuth: auth, Records: records, PublicURL: mustURL("https://admin.example.com"), TokenSecret: testTokenSecret}
}

func authenticatedRecordRequest(method, path, body string, csrf bool) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Host = "admin.example.com"
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: "fixed-session-token"})
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodPost || method == http.MethodPatch || method == http.MethodDelete {
		request.Header.Set("Origin", "https://admin.example.com")
		if csrf {
			request.Header.Set("X-PgBase-CSRF", "4LccAvSIPPQQ4q-qU6zW3cQ4E1sUfZmE2_lisE5NTqo")
		}
	}
	return request
}
