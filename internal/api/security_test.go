package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/adminauth"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/internal/query"
	"github.com/webisnoteasy/PgBase/internal/schema"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

const securityPassword = "correct horse battery staple 2026!"

func TestSecuritySQLValuesRemainParametersAndErrorsStaySecretFree(t *testing.T) {
	t.Parallel()

	const attack = "security-value-7f31'); DROP TABLE pgbase_meta.admins; --"
	records := &securityCompilerRecords{
		collection: schema.Collection{
			Name:      "records",
			TableName: "records",
			Fields: []schema.Field{
				{Name: "title", ColumnName: "title", Type: schema.TypeText, Required: true, Ordinal: 0},
				{Name: "metadata", ColumnName: "metadata", Type: schema.TypeJSONB, Ordinal: 1},
			},
		},
		createErr: errors.New("database rejected hostile fixture " + attack),
	}
	auth := &fakeAdminAuth{authenticateAdmin: validSession().Administrator}
	var logs bytes.Buffer
	handler := NewRouter(Dependencies{
		Logger:      observability.NewJSONLogger(&logs),
		AdminAuth:   auth,
		Records:     records,
		PublicURL:   mustURL("http://pgbase.test"),
		TokenSecret: testTokenSecret,
	})
	body := `{"title":` + mustMarshalSecurityString(t, attack) + `,"metadata":{"note":` + mustMarshalSecurityString(t, attack) + `}}`
	request := securityRequest(t, http.MethodPost, "http://pgbase.test/api/v1/collections/records/records", body)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: validSession().RawToken})
	request.Header.Set("Origin", "http://pgbase.test")
	request.Header.Set("X-PgBase-CSRF", adminauth.DeriveCSRFToken(testTokenSecret, validSession().RawToken))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assertSecurityError(t, response, http.StatusInternalServerError, "internal_error", attack, "DROP TABLE")
	if strings.Contains(records.statement.SQL, attack) || strings.Contains(records.statement.SQL, "DROP TABLE") {
		t.Fatalf("compiled SQL contains request value: %q", records.statement.SQL)
	}
	if len(records.statement.Args) != 5 || records.statement.Args[3] != attack {
		t.Fatalf("compiled arguments = %#v, want hostile value only as argument", records.statement.Args)
	}
	assertSecuritySecretsAbsent(t, logs.String(), attack, "DROP TABLE")
}

func TestSecuritySchemaIdentifiersAreRejectedBeforeDDLPlanning(t *testing.T) {
	t.Parallel()

	const attack = `records"; DROP TABLE pgbase_meta.admins; --`
	auth := &fakeAdminAuth{authenticateAdmin: validSession().Administrator}
	var logs bytes.Buffer
	handler := NewRouter(Dependencies{
		Logger:    observability.NewJSONLogger(&logs),
		AdminAuth: auth,
		Schema:    securityPlannerService{},
	})
	body := `{"name":` + mustMarshalSecurityString(t, attack) + `,"fields":[{"name":"title","type":"text"}]}`
	request := securityRequest(t, http.MethodPost, "http://pgbase.test/api/admin/collections/plan", body)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: validSession().RawToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assertSecurityError(t, response, http.StatusBadRequest, "validation_failed", attack, "DROP TABLE")
	assertSecuritySecretsAbsent(t, logs.String(), attack, "DROP TABLE")
}

func TestSecurityStrictJSONBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{
			name: "unknown field",
			body: `{"email":"admin@example.test","password":"correct horse battery staple","role":"security-unknown-field-7f31"}`,
		},
		{
			name: "oversized body",
			body: `{"email":"admin@example.test","password":"security-oversized-body-7f31` + strings.Repeat("x", maxJSONBodyBytes) + `"}`,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			auth := &fakeAdminAuth{loginSession: validSession()}
			var logs bytes.Buffer
			handler := NewRouter(Dependencies{Logger: observability.NewJSONLogger(&logs), AdminAuth: auth, PublicURL: mustURL("http://pgbase.test"), TokenSecret: testTokenSecret})
			request := securityRequest(t, http.MethodPost, "http://pgbase.test/api/admin/login", test.body)
			request.Header.Set("Origin", "http://pgbase.test")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			assertSecurityError(t, response, http.StatusBadRequest, "invalid_request", "security-", "7f31")
			assertSecuritySecretsAbsent(t, logs.String(), "security-", "7f31")
			if auth.loginCalls != 0 {
				t.Fatalf("Login() calls = %d, want 0", auth.loginCalls)
			}
		})
	}
}

func TestSecurityAdminSessionOriginAndCSRFBypassesAreRejected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		method         string
		path           string
		origin         []string
		cookie         string
		csrf           string
		remoteAddress  string
		forwardedProto string
		trustedProxy   bool
		authError      error
		wantStatus     int
		wantCode       string
	}{
		{name: "missing session", method: http.MethodGet, path: "/api/admin/session", wantStatus: http.StatusUnauthorized, wantCode: "authentication_required"},
		{name: "forged session", method: http.MethodGet, path: "/api/admin/session", cookie: "security-forged-session-7f31", authError: adminauth.ErrInvalidCredentials, wantStatus: http.StatusUnauthorized, wantCode: "authentication_required"},
		{name: "missing origin", method: http.MethodDelete, path: "/api/admin/session", cookie: validSession().RawToken, csrf: validCSRF(), wantStatus: http.StatusForbidden, wantCode: "origin_rejected"},
		{name: "cross origin", method: http.MethodDelete, path: "/api/admin/session", origin: []string{"https://evil.example"}, cookie: validSession().RawToken, csrf: validCSRF(), wantStatus: http.StatusForbidden, wantCode: "origin_rejected"},
		{name: "duplicate origin", method: http.MethodDelete, path: "/api/admin/session", origin: []string{"http://pgbase.test", "http://pgbase.test"}, cookie: validSession().RawToken, csrf: validCSRF(), wantStatus: http.StatusForbidden, wantCode: "origin_rejected"},
		{name: "untrusted forwarded proto", method: http.MethodDelete, path: "/api/admin/session", origin: []string{"https://pgbase.test"}, cookie: validSession().RawToken, csrf: validCSRF(), remoteAddress: "198.51.100.8:4000", forwardedProto: "https", trustedProxy: true, wantStatus: http.StatusForbidden, wantCode: "origin_rejected"},
		{name: "missing csrf", method: http.MethodDelete, path: "/api/admin/session", origin: []string{"http://pgbase.test"}, cookie: validSession().RawToken, wantStatus: http.StatusForbidden, wantCode: "csrf_rejected"},
		{name: "wrong csrf", method: http.MethodDelete, path: "/api/admin/session", origin: []string{"http://pgbase.test"}, cookie: validSession().RawToken, csrf: "security-wrong-csrf-7f31", wantStatus: http.StatusForbidden, wantCode: "csrf_rejected"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			auth := &fakeAdminAuth{authenticateAdmin: validSession().Administrator, authenticateErr: test.authError}
			var logs bytes.Buffer
			dependencies := Dependencies{
				Logger:      observability.NewJSONLogger(&logs),
				AdminAuth:   auth,
				PublicURL:   mustURL("http://pgbase.test"),
				TokenSecret: testTokenSecret,
			}
			if test.trustedProxy {
				dependencies.PublicURL = nil
				dependencies.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
			}
			handler := NewRouter(dependencies)
			request := httptest.NewRequest(test.method, "http://pgbase.test"+test.path, nil)
			if test.remoteAddress != "" {
				request.RemoteAddr = test.remoteAddress
			}
			if test.forwardedProto != "" {
				request.Header.Set("X-Forwarded-Proto", test.forwardedProto)
			}
			for _, origin := range test.origin {
				request.Header.Add("Origin", origin)
			}
			if test.cookie != "" {
				request.AddCookie(&http.Cookie{Name: adminCookieName, Value: test.cookie})
			}
			if test.csrf != "" {
				request.Header.Set("X-PgBase-CSRF", test.csrf)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			assertSecurityError(t, response, test.wantStatus, test.wantCode, test.cookie, test.csrf, "security-")
			assertSecuritySecretsAbsent(t, logs.String(), test.cookie, test.csrf, "security-")
		})
	}
}

func TestSecurityForwardedAddressesRespectTrustedProxyBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		remoteAddress string
		forwardedFor  string
		wantSubject   string
		sensitive     []string
	}{
		{
			name:          "untrusted peer cannot spoof its address",
			remoteAddress: "198.51.100.8:4000",
			forwardedFor:  "203.0.113.99",
			wantSubject:   "198.51.100.8",
			sensitive:     []string{"203.0.113.99"},
		},
		{
			name:          "trusted proxy chain selects the first untrusted address",
			remoteAddress: "10.0.0.9:4000",
			forwardedFor:  "203.0.113.50, 10.0.0.5",
			wantSubject:   "203.0.113.50",
			sensitive:     []string{"203.0.113.50", "10.0.0.5"},
		},
		{
			name:          "malformed trusted proxy chain falls back to the peer",
			remoteAddress: "10.0.0.9:4000",
			forwardedFor:  "203.0.113.50, security-malformed-forwarded-7f31",
			wantSubject:   "10.0.0.9",
			sensitive:     []string{"203.0.113.50", "security-malformed-forwarded-7f31"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			auth := &fakeAdminAuth{loginErr: adminauth.ErrInvalidCredentials}
			var logs bytes.Buffer
			handler := NewRouter(Dependencies{
				Logger:         observability.NewJSONLogger(&logs),
				AdminAuth:      auth,
				PublicURL:      mustURL("http://pgbase.test"),
				TokenSecret:    testTokenSecret,
				TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
			})
			request := securityRequest(t, http.MethodPost, "http://pgbase.test/api/admin/login", `{"email":"admin@example.test","password":"correct horse battery staple"}`)
			request.Header.Set("Origin", "http://pgbase.test")
			request.Header.Set("X-Forwarded-For", test.forwardedFor)
			request.RemoteAddr = test.remoteAddress
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			sensitive := append([]string{"security-", test.forwardedFor}, test.sensitive...)
			assertSecurityError(t, response, http.StatusUnauthorized, "invalid_credentials", sensitive...)
			assertSecuritySecretsAbsent(t, logs.String(), sensitive...)
			if auth.loginCalls != 1 {
				t.Fatalf("Login() calls = %d, want 1", auth.loginCalls)
			}
			if auth.loginSubject != test.wantSubject {
				t.Fatalf("Login() subject = %q, want %q", auth.loginSubject, test.wantSubject)
			}
		})
	}
}

func TestSecuritySetupCannotBeReused(t *testing.T) {
	pool := newSecurityDatabase(t)
	const setupToken = "security-setup-token-7f31-0123456789abcdef"
	const secondPassword = "security-second-password-7f31"
	auth := adminauth.NewService(pool, adminauth.NewArgon2idPasswords(), testTokenSecret, setupToken, time.Hour, nil)
	var logs bytes.Buffer
	handler := NewRouter(Dependencies{
		Logger:      observability.NewJSONLogger(&logs),
		AdminAuth:   auth,
		PublicURL:   mustURL("http://pgbase.test"),
		TokenSecret: testTokenSecret,
	})

	first := securityRequest(t, http.MethodPost, "http://pgbase.test/api/admin/setup", `{"setupToken":"`+setupToken+`","email":"admin@example.test","password":"`+securityPassword+`"}`)
	first.Header.Set("Origin", "http://pgbase.test")
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusCreated {
		t.Fatalf("first setup status = %d, body = %s", firstResponse.Code, firstResponse.Body.String())
	}

	second := securityRequest(t, http.MethodPost, "http://pgbase.test/api/admin/setup", `{"setupToken":"`+setupToken+`","email":"second@example.test","password":"`+secondPassword+`"}`)
	second.Header.Set("Origin", "http://pgbase.test")
	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, second)
	assertSecurityError(t, secondResponse, http.StatusConflict, "setup_unavailable", setupToken, secondPassword)
	assertSecuritySecretsAbsent(t, logs.String(), setupToken, secondPassword)
}

func TestSecurityMalformedPasswordHashReturnsGenericCredentialsError(t *testing.T) {
	pool := newSecurityDatabase(t)
	const setupToken = "security-setup-token-7f31-0123456789abcdef"
	const malformedHash = "security-malformed-hash-7f31-postgres://admin:secret@db/private"
	auth := adminauth.NewService(pool, adminauth.NewArgon2idPasswords(), testTokenSecret, setupToken, time.Hour, nil)
	if _, err := auth.Setup(context.Background(), setupToken, "admin@example.test", securityPassword, "setup-client"); err != nil {
		t.Fatalf("seed administrator: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `update pgbase_meta.admins set password_hash = $1 where email = $2`, malformedHash, "admin@example.test"); err != nil {
		t.Fatalf("replace password hash: %v", err)
	}
	var logs bytes.Buffer
	handler := NewRouter(Dependencies{
		Logger:      observability.NewJSONLogger(&logs),
		AdminAuth:   auth,
		PublicURL:   mustURL("http://pgbase.test"),
		TokenSecret: testTokenSecret,
	})
	request := securityRequest(t, http.MethodPost, "http://pgbase.test/api/admin/login", `{"email":"admin@example.test","password":"`+securityPassword+`"}`)
	request.Header.Set("Origin", "http://pgbase.test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assertSecurityError(t, response, http.StatusUnauthorized, "invalid_credentials", malformedHash, securityPassword, "postgres://", "7f31")
	assertSecuritySecretsAbsent(t, logs.String(), malformedHash, securityPassword, "postgres://", "7f31")
}

func TestSecurityTamperedCursorIsRejectedWithoutDisclosure(t *testing.T) {
	pool := newSecurityDatabase(t)
	const setupToken = "security-setup-token-7f31-0123456789abcdef"
	auth := adminauth.NewService(pool, adminauth.NewArgon2idPasswords(), testTokenSecret, setupToken, time.Hour, nil)
	session, err := auth.Setup(context.Background(), setupToken, "admin@example.test", securityPassword, "setup-client")
	if err != nil {
		t.Fatalf("seed administrator: %v", err)
	}
	schemas := schema.NewService(pool, &schema.Cache{}, schema.Planner{})
	records := query.NewService(pool, schemas, testTokenSecret)
	codec := query.NewCursorCodec(testTokenSecret)
	valid, err := codec.Encode(query.PageCursor{
		Version:   1,
		CreatedAt: time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
		ID:        uuid.MustParse("018f1f09-7b44-7cc1-98e8-6f9a46a17d21"),
	})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	tampered := valid[:5] + differentSecurityCharacter(valid[5]) + valid[6:]
	var logs bytes.Buffer
	handler := NewRouter(Dependencies{
		Logger:      observability.NewJSONLogger(&logs),
		AdminAuth:   auth,
		Records:     records,
		PublicURL:   mustURL("http://pgbase.test"),
		TokenSecret: testTokenSecret,
	})
	request := httptest.NewRequest(http.MethodGet, "http://pgbase.test/api/v1/collections/records/records?cursor="+url.QueryEscape(tampered), nil)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: session.RawToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assertSecurityError(t, response, http.StatusBadRequest, "invalid_cursor", tampered)
	assertSecuritySecretsAbsent(t, logs.String(), tampered)
}

func TestSecurityJSONErrorsAndStructuredLogsRedactSecrets(t *testing.T) {
	t.Parallel()

	const databaseURL = "postgres://admin:security-db-password-7f31@db.example/private?sslmode=require"
	const authorization = "Bearer security-authorization-7f31"
	const sqlText = "SELECT security_private FROM pgbase_meta.admins"
	hostile := errors.New(databaseURL + " " + authorization + " " + sqlText)
	auth := &fakeAdminAuth{authenticateAdmin: validSession().Administrator}
	var requestLogs bytes.Buffer
	handler := NewRouter(Dependencies{
		Logger:      observability.NewJSONLogger(&requestLogs),
		AdminAuth:   auth,
		Records:     securityErrorRecords{err: hostile},
		TokenSecret: testTokenSecret,
	})
	request := httptest.NewRequest(http.MethodGet, "http://pgbase.test/api/v1/collections/records/records/018f1f09-7b44-7cc1-98e8-6f9a46a17d21", nil)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: validSession().RawToken})
	request.Header.Set("Authorization", authorization)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assertSecurityError(t, response, http.StatusInternalServerError, "internal_error", databaseURL, "security-db-password-7f31", authorization, sqlText)
	assertSecuritySecretsAbsent(t, requestLogs.String(), databaseURL, "security-db-password-7f31", authorization, "security-authorization-7f31", sqlText)

	var redactionLogs bytes.Buffer
	logger := observability.NewJSONLogger(&redactionLogs)
	logger.Error("database operation failed", "error", errors.New(databaseURL+" "+authorization), "authorization", authorization)
	assertSecuritySecretsAbsent(t, redactionLogs.String(), databaseURL, "security-db-password-7f31", authorization, "security-authorization-7f31")
}

type securityCompilerRecords struct {
	collection schema.Collection
	statement  query.Statement
	createErr  error
}

func (*securityCompilerRecords) List(context.Context, string, string, int) (query.RecordPage, error) {
	panic("unexpected List call")
}

func (*securityCompilerRecords) Get(context.Context, string, uuid.UUID) (map[string]any, error) {
	panic("unexpected Get call")
}

func (r *securityCompilerRecords) Create(_ context.Context, _ string, input map[string]json.RawMessage) (map[string]any, error) {
	statement, err := query.CompileInsert(r.collection, input, uuid.MustParse("018f1f09-7b44-7cc1-98e8-6f9a46a17d21"), time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC))
	if err != nil {
		return nil, err
	}
	r.statement = statement
	return nil, r.createErr
}

func (*securityCompilerRecords) Update(context.Context, string, uuid.UUID, map[string]json.RawMessage) (map[string]any, error) {
	panic("unexpected Update call")
}

func (*securityCompilerRecords) Delete(context.Context, string, uuid.UUID) error {
	panic("unexpected Delete call")
}

type securityPlannerService struct{}

func (securityPlannerService) PlanCreate(_ context.Context, input schema.CreateCollectionInput) (schema.Plan, error) {
	return (schema.Planner{}).PlanCreate(0, input)
}

func (securityPlannerService) ApplyCreate(context.Context, schema.CreateCollectionInput, string, int64, uuid.UUID) (schema.Collection, error) {
	panic("unexpected ApplyCreate call")
}

func (securityPlannerService) Catalog(context.Context) (schema.Catalog, error) {
	panic("unexpected Catalog call")
}

type securityErrorRecords struct{ err error }

func (r securityErrorRecords) List(context.Context, string, string, int) (query.RecordPage, error) {
	return query.RecordPage{}, r.err
}

func (r securityErrorRecords) Get(context.Context, string, uuid.UUID) (map[string]any, error) {
	return nil, r.err
}

func (r securityErrorRecords) Create(context.Context, string, map[string]json.RawMessage) (map[string]any, error) {
	return nil, r.err
}

func (r securityErrorRecords) Update(context.Context, string, uuid.UUID, map[string]json.RawMessage) (map[string]any, error) {
	return nil, r.err
}

func (r securityErrorRecords) Delete(context.Context, string, uuid.UUID) error { return r.err }

func newSecurityDatabase(t *testing.T) *database.Pool {
	t.Helper()
	pool, err := database.Open(context.Background(), testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatalf("open security database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(context.Background()); err != nil {
		t.Fatalf("migrate security database: %v", err)
	}
	return pool
}

func securityRequest(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func mustMarshalSecurityString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return string(encoded)
}

func validCSRF() string {
	return adminauth.DeriveCSRFToken(testTokenSecret, validSession().RawToken)
}

func differentSecurityCharacter(value byte) string {
	if value == 'A' {
		return "B"
	}
	return "A"
}

func assertSecurityError(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantCode string, secrets ...string) {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, wantStatus, response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), wantCode)
	assertSecuritySecretsAbsent(t, response.Body.String(), secrets...)
}

func assertSecuritySecretsAbsent(t *testing.T, output string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(output, secret) {
			t.Fatalf("output leaked %q: %s", secret, output)
		}
	}
}
