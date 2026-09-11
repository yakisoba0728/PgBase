package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordops"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestRecordRoutesRequireAdministratorAndCSRF(t *testing.T) {
	records := &fakeRecordService{}
	unauthenticated := mustRouter(t, recordTestDependencies(&fakeAdminAuth{}, records))
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

	handler := mustRouter(t, recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))
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
	recordData := map[string]any{"id": id.String(), "title": "hello"}
	records := &fakeRecordService{
		page:   query.RecordPage{Items: []map[string]any{recordData}, NextCursor: "next"},
		record: recordops.Record{Data: recordData, ETag: `"pgb1.current"`, Version: 1},
	}
	handler := mustRouter(t, recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))

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
	if records.listRequest.Collection != "posts" || records.listRequest.Cursor != "signed" || records.listRequest.Limit != 2 ||
		records.getRequest.PublicID != id.String() || records.updateRequest.PublicID != id.String() || records.deleteRequest.PublicID != id.String() {
		t.Fatalf("service calls=%+v", records)
	}
	request := authenticatedRecordRequest(http.MethodGet, "/api/v1/collections/posts/records?limit=999", "", true)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || records.listRequest.Limit != 200 {
		t.Fatalf("capped list status=%d limit=%d", response.Code, records.listRequest.Limit)
	}
	request = authenticatedRecordRequest(http.MethodGet, "/api/v1/collections/posts/records", "", true)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || records.listRequest.Limit != 50 {
		t.Fatalf("default list status=%d limit=%d", response.Code, records.listRequest.Limit)
	}
}

func TestPocketBasePageAdapterHasExactEnvelopeAndInputs(t *testing.T) {
	records := &fakePocketBaseRecordService{
		fakeRecordService: &fakeRecordService{},
		page:              recordops.PocketBasePage{Page: 3, PerPage: 2, TotalItems: -1, TotalPages: -1, Items: []map[string]any{{"id": "one"}}},
	}
	handler := mustRouter(t, recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))
	request := authenticatedRecordRequest(http.MethodGet, "/api/v1/collections/posts/records?compat=pocketbase&page=3&perPage=2&skipTotal=true&sort=%40rowid", "", true)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != `{"page":3,"perPage":2,"totalItems":-1,"totalPages":-1,"items":[{"id":"one"}]}`+"\n" {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if records.listCalls != 0 || records.pbCalls != 1 || records.pbRequest.Page != 3 || records.pbRequest.PerPage != 2 || !records.pbRequest.SkipTotal || records.pbRequest.Sort != "@rowid" {
		t.Fatalf("calls native=%d pb=%d request=%+v", records.listCalls, records.pbCalls, records.pbRequest)
	}

	request = authenticatedRecordRequest(http.MethodGet, "/api/v1/collections/posts/records?compat=pocketbase&page=nope", "", true)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || records.pbCalls != 1 {
		t.Fatalf("malformed page status=%d calls=%d body=%s", response.Code, records.pbCalls, response.Body.String())
	}
}

func TestRecordRoutesRejectMalformedInputs(t *testing.T) {
	handler := mustRouter(t, recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, &fakeRecordService{getErr: &recordops.Error{Code: recordops.CodeInvalidRecordID}}))
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

func TestRecordRoutesRejectDuplicateKeysAfterLargeNumberBeforeServiceCall(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPatch} {
		records := &fakeRecordService{}
		handler := mustRouter(t, recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))
		path := "/api/v1/collections/posts/records"
		if method == http.MethodPatch {
			path += "/aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
		}
		request := authenticatedRecordRequest(method, path, `{"payload":1e400,"title":"safe","title":"last"}`, true)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", method, response.Code, response.Body.String())
		}
		assertErrorCode(t, response.Body.Bytes(), "invalid_request")
		if records.createCalls != 0 || records.updateCalls != 0 {
			t.Fatalf("%s service calls create=%d update=%d", method, records.createCalls, records.updateCalls)
		}
	}
}

func TestRecordListForwardsFilterAndSortParams(t *testing.T) {
	records := &fakeRecordService{page: query.RecordPage{Items: []map[string]any{}}}
	handler := mustRouter(t, recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))
	request := authenticatedRecordRequest(http.MethodGet,
		"/api/v1/collections/posts/records?filter="+url.QueryEscape(`(title ~ 'x' && rank >= 2)`)+
			"&sort="+url.QueryEscape("-rank,created_at")+"&expand="+url.QueryEscape("owner,reviewers"), "", true)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if records.listRequest.Filter != `(title ~ 'x' && rank >= 2)` || records.listRequest.Sort != "-rank,created_at" ||
		records.listRequest.Expand != "owner,reviewers" {
		t.Fatalf("forwarded params filter=%q sort=%q expand=%q",
			records.listRequest.Filter, records.listRequest.Sort, records.listRequest.Expand)
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

func TestRecordReadsForwardFieldsProjection(t *testing.T) {
	id := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	records := &fakeRecordService{
		page:   query.RecordPage{Items: []map[string]any{{"id": id.String(), "title": "hello"}}},
		record: recordops.Record{Data: map[string]any{"id": id.String(), "title": "hello"}, ETag: `"pgb1.current"`},
	}
	handler := mustRouter(t, recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))

	list := authenticatedRecordRequest(http.MethodGet, "/api/v1/collections/posts/records?fields=title%2Cid", "", true)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK || records.listRequest.Fields != "title,id" {
		t.Fatalf("list status=%d fields=%q body=%s", listResponse.Code, records.listRequest.Fields, listResponse.Body.String())
	}

	get := authenticatedRecordRequest(http.MethodGet, "/api/v1/collections/posts/records/"+id.String()+"?fields=title%2Cid", "", true)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || records.getRequest.Fields != "title,id" {
		t.Fatalf("get status=%d fields=%q body=%s", getResponse.Code, records.getRequest.Fields, getResponse.Body.String())
	}
}

func TestRecordGetForwardsStrictlyParsedExpandParam(t *testing.T) {
	id := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	records := &fakeRecordService{record: recordops.Record{
		Data: map[string]any{"id": id.String()}, ETag: `"pgb1.current"`, Version: 1,
	}}
	handler := mustRouter(t, recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))

	request := authenticatedRecordRequest(http.MethodGet,
		"/api/v1/collections/posts/records/"+id.String()+"?expand="+url.QueryEscape("owner,reviewers"), "", true)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || records.getRequest.Expand != "owner,reviewers" {
		t.Fatalf("get expand status=%d request=%+v body=%s", response.Code, records.getRequest, response.Body.String())
	}

	records.getCalls = 0
	request = authenticatedRecordRequest(http.MethodGet,
		"/api/v1/collections/posts/records/"+id.String(), "", true)
	request.URL.RawQuery = "expand=owner%"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || records.getCalls != 0 {
		t.Fatalf("malformed get expand status=%d calls=%d body=%s", response.Code, records.getCalls, response.Body.String())
	}
}

func TestRecordListRejectsMalformedQueryEncodingBeforeServiceCall(t *testing.T) {
	for _, rawQuery := range []string{
		"filter=n%3D1%",
		"sort=-rank%zz",
		"cursor=abc%",
		"limit=5%",
		"filter=title%3D1&sort=%",
		"unknown=%",
		"filter=title;sort=rank",
	} {
		t.Run(rawQuery, func(t *testing.T) {
			records := &fakeRecordService{}
			handler := mustRouter(t, recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))
			request := authenticatedRecordRequest(http.MethodGet, "/api/v1/collections/posts/records", "", true)
			request.URL.RawQuery = rawQuery
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertErrorCode(t, response.Body.Bytes(), "invalid_request")
			if records.listCalls != 0 {
				t.Fatalf("List() calls = %d, want 0", records.listCalls)
			}
		})
	}
}

func TestRecordReadsRejectDuplicateSingletonQueryParametersBeforeServiceCall(t *testing.T) {
	for _, parameter := range []string{"limit", "cursor", "filter", "sort", "expand", "fields"} {
		t.Run("list/"+parameter, func(t *testing.T) {
			records := &fakeRecordService{}
			handler := mustRouter(t, recordTestDependencies(
				&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))
			request := authenticatedRecordRequest(http.MethodGet,
				"/api/v1/collections/posts/records?"+parameter+"=first&"+parameter+"=second", "", true)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || records.listCalls != 0 {
				t.Fatalf("status=%d list calls=%d body=%s", response.Code, records.listCalls, response.Body.String())
			}
			assertErrorCode(t, response.Body.Bytes(), "invalid_request")
		})
	}

	t.Run("get/expand", func(t *testing.T) {
		records := &fakeRecordService{}
		handler := mustRouter(t, recordTestDependencies(
			&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))
		request := authenticatedRecordRequest(http.MethodGet,
			"/api/v1/collections/posts/records/aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee?expand=owner&expand=reviewers", "", true)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || records.getCalls != 0 {
			t.Fatalf("status=%d get calls=%d body=%s", response.Code, records.getCalls, response.Body.String())
		}
		assertErrorCode(t, response.Body.Bytes(), "invalid_request")
	})

	t.Run("get/fields", func(t *testing.T) {
		records := &fakeRecordService{}
		handler := mustRouter(t, recordTestDependencies(
			&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))
		request := authenticatedRecordRequest(http.MethodGet,
			"/api/v1/collections/posts/records/aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee?fields=id&fields=title", "", true)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || records.getCalls != 0 {
			t.Fatalf("status=%d get calls=%d body=%s", response.Code, records.getCalls, response.Body.String())
		}
		assertErrorCode(t, response.Body.Bytes(), "invalid_request")
	})

	t.Run("unrecognized duplicates remain ignored", func(t *testing.T) {
		records := &fakeRecordService{page: query.RecordPage{Items: []map[string]any{}}}
		handler := mustRouter(t, recordTestDependencies(
			&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))
		request := authenticatedRecordRequest(http.MethodGet,
			"/api/v1/collections/posts/records?future=first&future=second", "", true)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || records.listCalls != 1 {
			t.Fatalf("status=%d list calls=%d body=%s", response.Code, records.listCalls, response.Body.String())
		}
	})
}

func TestRecordCreateRejectsMalformedOrDuplicateIdempotencyKeyBeforeServiceCall(t *testing.T) {
	tests := []struct {
		name   string
		values []string
	}{
		{name: "too short", values: []string{"short"}},
		{name: "too long", values: []string{strings.Repeat("a", 129)}},
		{name: "space", values: []string{"key-with-space- 2026"}},
		{name: "comma", values: []string{"key-one-12345678,key-two-12345678"}},
		{name: "non ascii", values: []string{"idempotency-key-안전"}},
		{name: "duplicate field lines", values: []string{"first-key-123456", "second-key-12345"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			records := &fakeRecordService{}
			handler := mustRouter(t, recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))
			request := authenticatedRecordRequest(http.MethodPost, "/api/v1/collections/posts/records", `{}`, true)
			for _, value := range test.values {
				request.Header.Add("Idempotency-Key", value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertErrorCode(t, response.Body.Bytes(), "invalid_idempotency_key")
			if records.createCalls != 0 {
				t.Fatalf("Create() calls = %d, want 0", records.createCalls)
			}
		})
	}
}

func TestRecordMutationsParseBoundedIfMatch(t *testing.T) {
	id := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	newRecord := recordops.Record{Data: map[string]any{"id": id}, ETag: `"pgb1.next"`, Version: 2}
	tests := []struct {
		name       string
		header     string
		wantAny    bool
		wantValues []string
	}{
		{name: "missing"},
		{name: "wildcard", header: " \t* \t", wantAny: true},
		{name: "strong and weak tags", header: `"pgb1.current", W/"weak", "another"`, wantValues: []string{`"pgb1.current"`, `W/"weak"`, `"another"`}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			records := &fakeRecordService{record: newRecord}
			handler := mustRouter(t, recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))
			request := authenticatedRecordRequest(http.MethodPatch, "/api/v1/collections/posts/records/"+id, `{"title":"next"}`, true)
			if test.header != "" {
				request.Header.Set("If-Match", test.header)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			got := records.updateRequest.IfMatch
			if got.Present != (test.header != "") || got.Any != test.wantAny || strings.Join(got.Values, "|") != strings.Join(test.wantValues, "|") {
				t.Fatalf("IfMatch=%+v", got)
			}
		})
	}
}

func TestRecordMutationsRejectMalformedOrDuplicateIfMatch(t *testing.T) {
	tags := make([]string, 9)
	for index := range tags {
		tags[index] = `"tag-` + strconv.Itoa(index) + `"`
	}
	tests := []struct {
		name   string
		values []string
	}{
		{name: "empty", values: []string{""}},
		{name: "unclosed", values: []string{`"pgb1.current`}},
		{name: "lowercase weak prefix", values: []string{`w/"pgb1.current"`}},
		{name: "wildcard mixed with tag", values: []string{`*, "pgb1.current"`}},
		{name: "trailing comma", values: []string{`"pgb1.current",`}},
		{name: "too many tags", values: []string{strings.Join(tags, ",")}},
		{name: "too many bytes", values: []string{`"` + strings.Repeat("a", 1023) + `"`}},
		{name: "duplicate field lines", values: []string{`"one"`, `"two"`}},
	}
	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		for _, test := range tests {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				records := &fakeRecordService{}
				if method == http.MethodPatch {
					records.updateErr = &recordops.Error{Code: recordops.CodeInvalidPrecondition}
				} else {
					records.deleteErr = &recordops.Error{Code: recordops.CodeInvalidPrecondition}
				}
				handler := mustRouter(t, recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))
				body := ""
				if method == http.MethodPatch {
					body = `{}`
				}
				request := authenticatedRecordRequest(method, "/api/v1/collections/posts/records/aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", body, true)
				for _, value := range test.values {
					request.Header.Add("If-Match", value)
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusBadRequest {
					t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
				}
				assertErrorCode(t, response.Body.Bytes(), "invalid_precondition")
				wantUpdateCalls, wantDeleteCalls := 0, 0
				if method == http.MethodPatch {
					wantUpdateCalls = 1
				} else {
					wantDeleteCalls = 1
				}
				if records.updateCalls != wantUpdateCalls || records.deleteCalls != wantDeleteCalls {
					t.Fatalf("service calls update=%d delete=%d, want %d/%d", records.updateCalls, records.deleteCalls, wantUpdateCalls, wantDeleteCalls)
				}
				var got recordops.EntityTagSet
				if method == http.MethodPatch {
					got = records.updateRequest.IfMatch
				} else {
					got = records.deleteRequest.IfMatch
				}
				if !got.Present || got.Any || len(got.Values) != 0 {
					t.Fatalf("invalid If-Match sentinel = %#v", got)
				}
			})
		}
	}
}

func TestRecordPatchRejectsMalformedBodyBeforeMalformedIfMatch(t *testing.T) {
	records := &fakeRecordService{}
	handler := mustRouter(t, recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))
	request := authenticatedRecordRequest(http.MethodPatch,
		"/api/v1/collections/posts/records/aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", `{`, true)
	request.Header.Set("If-Match", "not-an-entity-tag")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "invalid_request")
	if records.updateCalls != 0 {
		t.Fatalf("update calls = %d, want 0", records.updateCalls)
	}
}

func TestRecordPatchRejectsTypeMismatchBeforeMalformedIfMatch(t *testing.T) {
	records := &fakeRecordService{updateErr: &query.ValidationError{Details: []query.ValidationDetail{{
		Field: "title", Code: "type_mismatch",
	}}}}
	handler := mustRouter(t, recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))
	request := authenticatedRecordRequest(http.MethodPatch,
		"/api/v1/collections/posts/records/aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", `{"title":123}`, true)
	request.Header.Set("If-Match", "not-an-entity-tag")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "validation_failed")
	if records.updateCalls != 1 || !records.updateRequest.IfMatch.Present ||
		records.updateRequest.IfMatch.Any || len(records.updateRequest.IfMatch.Values) != 0 {
		t.Fatalf("update request = %#v calls=%d", records.updateRequest, records.updateCalls)
	}
}

func TestRecordResponsesExposeEntityTagReplayLocationAndCompatibility(t *testing.T) {
	id := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	record := recordops.Record{Data: map[string]any{"id": id, "title": "hello"}, ETag: `"pgb1.response"`, Version: 3}
	auth := &fakeAdminAuth{authenticateAdmin: validSession().Administrator}

	t.Run("get", func(t *testing.T) {
		response := serveRecordRequest(t, &fakeRecordService{record: record}, auth,
			authenticatedRecordRequest(http.MethodGet, "/api/v1/collections/posts/records/"+id, "", true))
		if response.Code != http.StatusOK || response.Header().Get("ETag") != record.ETag || !strings.Contains(response.Body.String(), `"title":"hello"`) {
			t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
		}
	})

	t.Run("replayed create", func(t *testing.T) {
		records := &fakeRecordService{createResult: recordops.MutationResult{Record: &record, Replayed: true}}
		request := authenticatedRecordRequest(http.MethodPost, "/api/v1/collections/posts/records", `{"title":"hello"}`, true)
		request.Header.Set("Idempotency-Key", "create-draft-12345678")
		response := serveRecordRequest(t, records, auth, request)
		if response.Code != http.StatusCreated || response.Header().Get("ETag") != record.ETag ||
			response.Header().Get("Location") != "/api/v1/collections/posts/records/"+id ||
			response.Header().Get("Idempotency-Replayed") != "true" || response.Header().Get("Deprecation") != "" {
			t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
		}
	})

	t.Run("create compatibility", func(t *testing.T) {
		records := &fakeRecordService{createResult: recordops.MutationResult{Record: &record, Compatibility: recordops.CompatibilityMissingIdempotencyKey}}
		response := serveRecordRequest(t, records, auth,
			authenticatedRecordRequest(http.MethodPost, "/api/v1/collections/posts/records", `{"title":"hello"}`, true))
		assertCompatibilityHeaders(t, response, string(recordops.CompatibilityMissingIdempotencyKey))
		if response.Header().Get("Idempotency-Replayed") != "false" {
			t.Fatalf("Idempotency-Replayed=%q", response.Header().Get("Idempotency-Replayed"))
		}
	})

	t.Run("mutation fields projection keeps Location identity", func(t *testing.T) {
		records := &fakeRecordService{createResult: recordops.MutationResult{Record: &record}}
		request := authenticatedRecordRequest(http.MethodPost, "/api/v1/collections/posts/records?fields=title", `{"title":"hello"}`, true)
		response := serveRecordRequest(t, records, auth, request)
		if response.Code != http.StatusCreated || response.Header().Get("Location") != "/api/v1/collections/posts/records/"+id || strings.Contains(response.Body.String(), `"id"`) || !strings.Contains(response.Body.String(), `"title":"hello"`) {
			t.Fatalf("status=%d location=%q body=%s", response.Code, response.Header().Get("Location"), response.Body.String())
		}
	})

	t.Run("update compatibility", func(t *testing.T) {
		records := &fakeRecordService{updateResult: recordops.MutationResult{Record: &record, Compatibility: recordops.CompatibilityLastWriteWins}}
		response := serveRecordRequest(t, records, auth,
			authenticatedRecordRequest(http.MethodPatch, "/api/v1/collections/posts/records/"+id, `{"title":"hello"}`, true))
		assertCompatibilityHeaders(t, response, string(recordops.CompatibilityLastWriteWins))
		if response.Header().Get("ETag") != record.ETag {
			t.Fatalf("ETag=%q", response.Header().Get("ETag"))
		}
	})

	t.Run("delete compatibility", func(t *testing.T) {
		records := &fakeRecordService{deleteResult: recordops.MutationResult{Compatibility: recordops.CompatibilityLastWriteWins}}
		response := serveRecordRequest(t, records, auth,
			authenticatedRecordRequest(http.MethodDelete, "/api/v1/collections/posts/records/"+id, "", true))
		if response.Code != http.StatusNoContent {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		assertCompatibilityHeaders(t, response, string(recordops.CompatibilityLastWriteWins))
	})
}

func TestRecordRoutesMapStableRecordOperationErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantETag   string
		wantRetry  string
	}{
		{name: "invalid key", err: &recordops.Error{Code: recordops.CodeInvalidIdempotencyKey}, wantStatus: 400, wantCode: "invalid_idempotency_key"},
		{name: "different payload", err: &recordops.Error{Code: recordops.CodeIdempotencyConflict}, wantStatus: 409, wantCode: "idempotency_conflict"},
		{name: "in progress", err: &recordops.Error{Code: recordops.CodeIdempotencyInProgress}, wantStatus: 409, wantCode: "idempotency_in_progress"},
		{name: "invalid precondition", err: &recordops.Error{Code: recordops.CodeInvalidPrecondition}, wantStatus: 400, wantCode: "invalid_precondition"},
		{name: "stale precondition", err: &recordops.Error{Code: recordops.CodePreconditionFailed, CurrentETag: `"pgb1.current"`}, wantStatus: 412, wantCode: "precondition_failed", wantETag: `"pgb1.current"`},
		{name: "membership unavailable", err: &recordops.Error{Code: recordops.CodeUnavailable}, wantStatus: 503, wantCode: "record_unavailable", wantRetry: "1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			records := &fakeRecordService{updateErr: test.err}
			request := authenticatedRecordRequest(http.MethodPatch, "/api/v1/collections/posts/records/aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", `{}`, true)
			request.Header.Set("If-Match", `"pgb1.stale"`)
			response := serveRecordRequest(t, records, &fakeAdminAuth{authenticateAdmin: validSession().Administrator}, request)
			if response.Code != test.wantStatus || response.Header().Get("ETag") != test.wantETag ||
				response.Header().Get("Retry-After") != test.wantRetry {
				t.Fatalf("status=%d ETag=%q Retry-After=%q body=%s", response.Code,
					response.Header().Get("ETag"), response.Header().Get("Retry-After"), response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
			}
			assertErrorCode(t, response.Body.Bytes(), test.wantCode)
		})
	}
}

func TestRecordMutationRoutesMapMembershipAvailability(t *testing.T) {
	availability := &recordops.Error{Code: recordops.CodeUnavailable}
	id := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	tests := []struct {
		name    string
		method  string
		path    string
		body    string
		records *fakeRecordService
	}{
		{name: "create", method: http.MethodPost, path: "/api/v1/collections/posts/records", body: `{}`,
			records: &fakeRecordService{createErr: availability}},
		{name: "update", method: http.MethodPatch, path: "/api/v1/collections/posts/records/" + id, body: `{}`,
			records: &fakeRecordService{updateErr: availability}},
		{name: "delete", method: http.MethodDelete, path: "/api/v1/collections/posts/records/" + id,
			records: &fakeRecordService{deleteErr: availability}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := serveRecordRequest(t, test.records,
				&fakeAdminAuth{authenticateAdmin: validSession().Administrator},
				authenticatedRecordRequest(test.method, test.path, test.body, true))
			if response.Code != http.StatusServiceUnavailable ||
				response.Header().Get("Retry-After") != "1" ||
				response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d Retry-After=%q Cache-Control=%q body=%s", response.Code,
					response.Header().Get("Retry-After"), response.Header().Get("Cache-Control"), response.Body.String())
			}
			assertErrorCode(t, response.Body.Bytes(), "record_unavailable")
			if strings.Contains(response.Body.String(), "membership") || strings.Contains(response.Body.String(), "writer") {
				t.Fatalf("membership state leaked through API: %s", response.Body.String())
			}
		})
	}
}

func TestRecordPrincipalComesOnlyFromAuthenticatedContext(t *testing.T) {
	administrator := validSession().Administrator
	spoofed := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	record := recordops.Record{Data: map[string]any{"id": "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"}, ETag: `"pgb1.created"`, Version: 1}
	records := &fakeRecordService{record: record}
	request := authenticatedRecordRequest(http.MethodPost, "/api/v1/collections/posts/records",
		`{"actorId":"`+spoofed.String()+`","actorKind":"owner","title":"hello"}`, true)
	request.Header.Set("Idempotency-Key", "actor-bound-key-12345")
	request.Header.Set("X-Actor-ID", spoofed.String())
	request.Header.Set("X-Request-ID", "request_actor_test")
	response := serveRecordRequest(t, records, &fakeAdminAuth{authenticateAdmin: administrator}, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	got := records.createRequest
	if got.Principal.Kind != recordops.PrincipalAdministrator || got.Principal.ID != administrator.ID ||
		got.Principal.ID == spoofed || got.RequestID != "request_actor_test" || got.IdempotencyKey != "actor-bound-key-12345" {
		t.Fatalf("request metadata=%+v", got)
	}
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
			handler := mustRouter(t, recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))
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
	handler := mustRouter(t, recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, records))
	request := authenticatedRecordRequest(http.MethodGet, "/api/v1/collections/posts/records/aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", "", true)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("status=%d retry-after=%q body=%s", response.Code, response.Header().Get("Retry-After"), response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "record_unavailable")
}

type fakeRecordService struct {
	page                                                       recordops.RecordPage
	record                                                     recordops.Record
	createResult, updateResult, deleteResult                   recordops.MutationResult
	listErr, getErr, createErr, updateErr, deleteErr           error
	listRequest                                                recordops.ListRequest
	getRequest                                                 recordops.GetRequest
	createRequest                                              recordops.CreateRequest
	updateRequest                                              recordops.UpdateRequest
	deleteRequest                                              recordops.DeleteRequest
	truncateRequest                                            recordops.TruncateRequest
	truncateResult                                             recordops.TruncateResult
	truncateErr                                                error
	listCalls, getCalls, createCalls, updateCalls, deleteCalls int
}

type fakePocketBaseRecordService struct {
	*fakeRecordService
	page      recordops.PocketBasePage
	pbRequest recordops.PocketBaseListRequest
	pbCalls   int
}

func (f *fakePocketBaseRecordService) ListPocketBase(_ context.Context, request recordops.PocketBaseListRequest) (recordops.PocketBasePage, error) {
	f.pbCalls++
	f.pbRequest = request
	return f.page, nil
}

func (f *fakeRecordService) Truncate(_ context.Context, request recordops.TruncateRequest) (recordops.TruncateResult, error) {
	f.truncateRequest = request
	return f.truncateResult, f.truncateErr
}

func (f *fakeRecordService) List(_ context.Context, request recordops.ListRequest) (recordops.RecordPage, error) {
	f.listCalls++
	f.listRequest = request
	return f.page, f.listErr
}
func (f *fakeRecordService) Get(_ context.Context, request recordops.GetRequest) (recordops.Record, error) {
	f.getCalls++
	f.getRequest = request
	return f.record, f.getErr
}
func (f *fakeRecordService) Create(_ context.Context, request recordops.CreateRequest) (recordops.MutationResult, error) {
	f.createCalls++
	f.createRequest = request
	result := f.createResult
	if result.Record == nil && f.record.Data != nil {
		result.Record = &f.record
	}
	return result, f.createErr
}
func (f *fakeRecordService) Update(_ context.Context, request recordops.UpdateRequest) (recordops.MutationResult, error) {
	f.updateCalls++
	f.updateRequest = request
	result := f.updateResult
	if result.Record == nil && f.record.Data != nil {
		result.Record = &f.record
	}
	return result, f.updateErr
}
func (f *fakeRecordService) Delete(_ context.Context, request recordops.DeleteRequest) (recordops.MutationResult, error) {
	f.deleteCalls++
	f.deleteRequest = request
	return f.deleteResult, f.deleteErr
}

func recordTestDependencies(auth AdminAuth, records RecordService) Dependencies {
	return Dependencies{AdminAuth: auth, Records: records, PublicURL: mustURL("https://admin.example.com")}
}

func serveRecordRequest(t *testing.T, records RecordService, auth AdminAuth, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	mustRouter(t, recordTestDependencies(auth, records)).ServeHTTP(response, request)
	return response
}

func assertCompatibilityHeaders(t *testing.T, response *httptest.ResponseRecorder, compatibility string) {
	t.Helper()
	if response.Header().Get("PgBase-Compatibility") != compatibility || response.Header().Get("Deprecation") != "" {
		t.Fatalf("compatibility=%q deprecation=%q", response.Header().Get("PgBase-Compatibility"), response.Header().Get("Deprecation"))
	}
}

func authenticatedRecordRequest(method, path, body string, csrf bool) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Host = "admin.example.com"
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"})
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodPost || method == http.MethodPatch || method == http.MethodDelete {
		request.Header.Set("Origin", "https://admin.example.com")
		if csrf {
			request.Header.Set("X-PgBase-CSRF", "6nY1MEBlcXJ8G0ZYhHnSS7QrtrSIeotz4iAyPNYTbF4")
		}
	}
	return request
}
