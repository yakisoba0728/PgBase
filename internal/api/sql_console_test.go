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
	"github.com/webisnoteasy/PgBase/internal/sqlconsole"
)

func TestSQLConsoleRoutesUseExistingAdminOriginSessionAndCSRFChain(t *testing.T) {
	session := validSession()
	auth := &fakeAdminAuth{
		authenticateAdmin: session.Administrator, authenticateExpiry: session.ExpiresAt,
	}
	service := &fakeSQLConsoleService{}
	handlers, err := NewSQLConsoleHandlers(service, []byte("n8-api-session-secret-that-is-long-enough"), nil)
	if err != nil {
		t.Fatal(err)
	}
	publicURL := mustURL("https://pgbase.example")
	root := http.NewServeMux()
	root.Handle("POST "+SQLConsoleQueriesPath, adminSessionMiddleware(auth, publicURL, nil, http.HandlerFunc(handlers.Submit)))
	root.Handle("GET "+SQLConsoleQueriesPath+"/{id}", adminSameOriginReadMiddleware(auth, publicURL, nil, http.HandlerFunc(handlers.Get)))

	body := `{"statements":[{"sql":"select $1","params":[{"kind":"integer","text":"9007199254740993"}]}]}`
	tests := []struct {
		name   string
		origin string
		cookie bool
		csrf   bool
		status int
		code   string
	}{
		{name: "anonymous", origin: "https://pgbase.example", status: http.StatusUnauthorized, code: "authentication_required"},
		{name: "foreign origin", origin: "https://foreign.example", cookie: true, csrf: true, status: http.StatusForbidden, code: "origin_rejected"},
		{name: "missing csrf", origin: "https://pgbase.example", cookie: true, status: http.StatusForbidden, code: "csrf_rejected"},
		{name: "admitted", origin: "https://pgbase.example", cookie: true, csrf: true, status: http.StatusAccepted},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, SQLConsoleQueriesPath, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", test.origin)
			if test.cookie {
				request.AddCookie(&http.Cookie{Name: adminCookieName, Value: session.RawToken})
			}
			if test.csrf {
				request.Header.Set("X-PgBase-CSRF", session.CSRFToken)
			}
			response := httptest.NewRecorder()
			root.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.code != "" {
				assertErrorCode(t, response.Body.Bytes(), test.code)
			}
		})
	}
	if service.submitCalls != 1 || service.lastActor.AdministratorID != session.Administrator.ID || service.lastActor.SessionKey == (sqlconsole.SessionKey{}) {
		t.Fatalf("service submission = %+v", service)
	}
	if service.lastRequest.Statements[0].Parameters[0].Text != "9007199254740993" {
		t.Fatalf("request = %#v", service.lastRequest)
	}

	get := httptest.NewRequest(http.MethodGet, SQLConsoleQueriesPath+"/"+service.lastRequest.ExecutionID.String(), nil)
	get.Header.Set("Origin", "https://foreign.example")
	get.AddCookie(&http.Cookie{Name: adminCookieName, Value: session.RawToken})
	getResponse := httptest.NewRecorder()
	root.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusForbidden {
		t.Fatalf("cross-origin GET status=%d body=%s", getResponse.Code, getResponse.Body.String())
	}
}

func TestSQLConsoleRouteRegistryPinsOptionalAdmissionAndBounds(t *testing.T) {
	handlers, err := NewSQLConsoleHandlers(&fakeSQLConsoleService{}, testTokenSecret, nil)
	if err != nil {
		t.Fatal(err)
	}
	routes := declareRoutes(completeDependencies(Dependencies{SQLConsole: handlers}), false)
	want := map[string]struct {
		access   accessPolicy
		response int
		request  int64
	}{
		"POST " + SQLConsoleQueriesPath:                  {accessAdminSession, MaximumSQLConsoleResponseBytes, MaximumSQLConsoleRequestBytes},
		"GET " + SQLConsoleQueriesPath:                   {accessAdminSameOriginRead, MaximumSQLConsoleHistoryBytes, 0},
		"GET " + SQLConsoleQueriesPath + "/{id}":         {accessAdminSameOriginRead, MaximumSQLConsoleResponseBytes, 0},
		"POST " + SQLConsoleQueriesPath + "/{id}/cancel": {accessAdminSession, MaximumSQLConsoleResponseBytes, 0},
	}
	for _, declared := range routes {
		expected, ok := want[declared.muxPattern()]
		if !ok {
			continue
		}
		if declared.access != expected.access || declared.response.maximumBytes != expected.response ||
			declared.maximumRequestBytes != expected.request || declared.deadline != deadlineDefault {
			t.Errorf("route %s policy = access %s response %d request %d deadline %d",
				declared, declared.access, declared.response.maximumBytes, declared.maximumRequestBytes, declared.deadline)
		}
		delete(want, declared.muxPattern())
	}
	if len(want) != 0 {
		t.Fatalf("SQL console routes missing: %v", want)
	}
	if err := validateRoutes(routes); err != nil {
		t.Fatal(err)
	}
}

func TestSQLConsoleSubmitMapsIdentityTimeoutAndExactFailureWire(t *testing.T) {
	service := &fakeSQLConsoleService{}
	handlers, _ := NewSQLConsoleHandlers(service, []byte("n8-api-session-secret-that-is-long-enough"), nil)
	executionID := uuid.New()
	request := requestWithAdminPrincipal(t, http.MethodPost, SQLConsoleQueriesPath,
		`{"mode":"write","dryRun":true,"timeoutMs":1234,"statements":[{"sql":"update ext.items set amount=$1","params":[{"kind":"decimal","text":"1.2300"}]}]}`)
	request.Header.Set(operationIDHeader, executionID.String())
	request.Header.Set(idempotencyKeyHeader, "n8-http-write-00000000000001")
	response := httptest.NewRecorder()
	handlers.Submit(response, request)
	if response.Code != http.StatusAccepted || response.Header().Get("Location") != SQLConsoleQueriesPath+"/"+executionID.String() {
		t.Fatalf("status=%d location=%q body=%s", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	if service.lastRequest.ExecutionID != executionID || service.lastRequest.Timeout != 1234*time.Millisecond || !service.lastRequest.DryRun || service.lastRequest.IdempotencyKey == "" {
		t.Fatalf("mapped request = %#v", service.lastRequest)
	}

	index := 0
	dto := sqlConsoleExecutionResponse(sqlconsole.Execution{
		ID: executionID, Mode: sqlconsole.ModeRead, Status: sqlconsole.StatusFailed, CreatedAt: time.Now(),
		Failure: &sqlconsole.Failure{Code: "postgres_error", Message: "Execution failed.", SQLState: "42601", StatementIndex: index, HasStatement: true, Position: 8},
	})
	encoded, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"statementIndex":0`) || !strings.Contains(string(encoded), `"position":8`) {
		t.Fatalf("failure wire = %s", encoded)
	}
}

func TestSQLConsoleHistoryReturnsCapabilitiesAndCanonicalCursor(t *testing.T) {
	service := &fakeSQLConsoleService{}
	next := sqlconsole.Cursor{CreatedAt: time.Now().UTC().Truncate(time.Microsecond), ID: uuid.New()}
	service.page = sqlconsole.Page{Executions: []sqlconsole.Execution{{
		ID: uuid.New(), Mode: sqlconsole.ModeRead, Status: sqlconsole.StatusSucceeded, CreatedAt: time.Now().UTC(),
	}}, Next: &next}
	handlers, _ := NewSQLConsoleHandlers(service, []byte("n8-api-session-secret-that-is-long-enough"), []SQLConsoleWriteTarget{{
		Schema: "ext", Table: "items", Columns: []string{"amount", "label"},
	}})
	request := requestWithAdminPrincipal(t, http.MethodGet, SQLConsoleQueriesPath, "")
	response := httptest.NewRecorder()
	handlers.List(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		NextCursor   string                  `json:"nextCursor"`
		WriteEnabled bool                    `json:"writeEnabled"`
		WriteTargets []SQLConsoleWriteTarget `json:"writeTargets"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.WriteEnabled || len(body.WriteTargets) != 1 || body.NextCursor == "" {
		t.Fatalf("history response = %#v", body)
	}
	decoded, err := decodeSQLConsoleCursor(body.NextCursor)
	if err != nil || decoded != next {
		t.Fatalf("decoded cursor = %#v, %v", decoded, err)
	}

	invalid := requestWithAdminPrincipal(t, http.MethodGet, SQLConsoleQueriesPath+"?cursor=forged", "")
	invalidResponse := httptest.NewRecorder()
	handlers.List(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("forged cursor status=%d body=%s", invalidResponse.Code, invalidResponse.Body.String())
	}
}

func TestSQLConsoleSubmitRejectsDuplicateIdentityHeadersAndUnknownJSON(t *testing.T) {
	handlers, _ := NewSQLConsoleHandlers(&fakeSQLConsoleService{}, []byte("n8-api-session-secret-that-is-long-enough"), nil)
	tests := []struct {
		name   string
		body   string
		mutate func(*http.Request)
	}{
		{name: "unknown JSON", body: `{"statements":[{"sql":"select 1"}],"rawSession":"secret"}`},
		{name: "duplicate operation", body: `{"statements":[{"sql":"select 1"}]}`, mutate: func(request *http.Request) {
			request.Header.Add(operationIDHeader, uuid.NewString())
			request.Header.Add(operationIDHeader, uuid.NewString())
		}},
		{name: "duplicate idempotency", body: `{"mode":"write","statements":[{"sql":"delete from ext.items"}]}`, mutate: func(request *http.Request) {
			request.Header.Add(idempotencyKeyHeader, "first-key-000000000000")
			request.Header.Add(idempotencyKeyHeader, "second-key-00000000000")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := requestWithAdminPrincipal(t, http.MethodPost, SQLConsoleQueriesPath, test.body)
			if test.mutate != nil {
				test.mutate(request)
			}
			response := httptest.NewRecorder()
			handlers.Submit(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

type fakeSQLConsoleService struct {
	submitCalls int
	lastActor   sqlconsole.Actor
	lastRequest sqlconsole.Request
	page        sqlconsole.Page
}

func (service *fakeSQLConsoleService) Submit(_ context.Context, actor sqlconsole.Actor, request sqlconsole.Request) (sqlconsole.Execution, error) {
	service.submitCalls++
	service.lastActor = actor
	service.lastRequest = request
	return sqlconsole.Execution{ID: request.ExecutionID, Mode: request.Mode, DryRun: request.DryRun, Status: sqlconsole.StatusPending, CreatedAt: time.Now().UTC()}, nil
}

func (service *fakeSQLConsoleService) Get(_ context.Context, _ sqlconsole.Actor, id uuid.UUID) (sqlconsole.Execution, error) {
	if id == uuid.Nil {
		return sqlconsole.Execution{}, sqlconsole.ErrNotFound
	}
	return sqlconsole.Execution{ID: id, Mode: sqlconsole.ModeRead, Status: sqlconsole.StatusRunning, CreatedAt: time.Now().UTC()}, nil
}

func (service *fakeSQLConsoleService) Cancel(ctx context.Context, actor sqlconsole.Actor, id uuid.UUID) (sqlconsole.Execution, error) {
	return service.Get(ctx, actor, id)
}

func (service *fakeSQLConsoleService) List(context.Context, sqlconsole.Actor, sqlconsole.Cursor) (sqlconsole.Page, error) {
	return service.page, nil
}

func requestWithAdminPrincipal(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	session := validSession()
	principal := AdminPrincipal{
		Administrator: session.Administrator, RawSession: session.RawToken,
		ExpiresAt: session.ExpiresAt, CSRFToken: session.CSRFToken,
	}
	return request.WithContext(context.WithValue(request.Context(), adminPrincipalKey{}, principal))
}

var _ SQLConsoleService = (*fakeSQLConsoleService)(nil)
