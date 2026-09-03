package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

// The recovery contract is what a guarded client codes against after its
// response was already lost, so its wire shape is pinned byte for byte:
// admission, CORS, the identity headers, every rejection shape, and the
// undecided answer that carries the recovery URL.
func TestSchemaOperationWireGolden(t *testing.T) {
	operationID := uuid.MustParse(testOperationID)
	committed := schema.OperationReceipt{
		OperationID: operationID, Operation: schema.SchemaOperationCollectionCreate,
		CollectionID: uuid.MustParse("019551d0-e9c0-7000-8000-0000000000bb"),
		Status:       schema.OperationCommitted, BaseVersion: 7, CommittedVersion: 8,
		CreatedAt: time.Date(2026, time.September, 1, 2, 3, 4, 0, time.UTC),
		UpdatedAt: time.Date(2026, time.September, 1, 2, 3, 9, 0, time.UTC),
	}

	tests := []struct {
		name    string
		schemas *fakeSchemaService
		make    func() *http.Request
	}{
		{
			name:    "operation status committed",
			schemas: &fakeSchemaService{statusReceipt: committed},
			make: func() *http.Request {
				return goldenOperationRequest(http.MethodGet, "/api/admin/schema-operations/"+testOperationID)
			},
		},
		{
			name:    "operation status not found",
			schemas: &fakeSchemaService{statusErr: schema.ErrOperationNotFound},
			make: func() *http.Request {
				return goldenOperationRequest(http.MethodGet, "/api/admin/schema-operations/"+testOperationID)
			},
		},
		{
			name:    "operation status malformed id",
			schemas: &fakeSchemaService{},
			make: func() *http.Request {
				return goldenOperationRequest(http.MethodGet, "/api/admin/schema-operations/not-a-uuid")
			},
		},
		{
			name:    "operation status unauthorized",
			schemas: &fakeSchemaService{},
			make: func() *http.Request {
				request := httptest.NewRequest(http.MethodGet, "/api/admin/schema-operations/"+testOperationID, nil)
				request.Host = "admin.example.com"
				request.Header.Set("Origin", "https://admin.example.com")
				return request
			},
		},
		{
			name: "operation history page",
			schemas: &fakeSchemaService{listPage: schema.OperationPage{
				Items:      []schema.OperationReceipt{committed},
				NextCursor: "opaque-owner-scoped-cursor",
			}},
			make: func() *http.Request {
				return goldenOperationRequest(http.MethodGet, "/api/admin/schema-operations?limit=1")
			},
		},
		{
			name:    "operation history limit rejected",
			schemas: &fakeSchemaService{},
			make: func() *http.Request {
				return goldenOperationRequest(http.MethodGet, "/api/admin/schema-operations?limit=101")
			},
		},
		{
			name:    "operation history cursor rejected",
			schemas: &fakeSchemaService{listErr: schema.ErrInvalidOperation},
			make: func() *http.Request {
				return goldenOperationRequest(http.MethodGet, "/api/admin/schema-operations?cursor=forged")
			},
		},
		{
			name: "legacy apply keeps its body and gains the compatibility header",
			schemas: &fakeSchemaService{applyResult: schema.ApplyResult{Collection: schema.Collection{
				ID: uuid.MustParse("019551d0-e9c0-7000-8000-000000000010"), Name: "posts", TableName: "posts",
				CreatedAt: time.Date(2026, time.August, 28, 4, 5, 6, 0, time.UTC),
			}}},
			make: func() *http.Request { return goldenApplyRequest(false) },
		},
		{
			name: "guarded apply created",
			schemas: &fakeSchemaService{applyResult: schema.ApplyResult{Collection: schema.Collection{
				ID: uuid.MustParse("019551d0-e9c0-7000-8000-000000000010"), Name: "posts", TableName: "posts",
				CreatedAt: time.Date(2026, time.August, 28, 4, 5, 6, 0, time.UTC),
			}}},
			make: func() *http.Request { return goldenApplyRequest(true) },
		},
		{
			name: "guarded apply replayed",
			schemas: &fakeSchemaService{applyResult: schema.ApplyResult{
				Replayed: true, Receipt: committed,
			}},
			make: func() *http.Request { return goldenApplyRequest(true) },
		},
		{
			name:    "guarded apply identity bound to a different request",
			schemas: &fakeSchemaService{applyErr: schema.ErrOperationConflict},
			make:    func() *http.Request { return goldenApplyRequest(true) },
		},
		{
			name: "guarded apply terminal failure",
			schemas: &fakeSchemaService{
				applyErr: schema.ErrOperationFailed,
				applyResult: schema.ApplyResult{Receipt: schema.OperationReceipt{
					OperationID: operationID, Status: schema.OperationFailed,
					FailureCode: schema.OperationFailureApplyFailed,
				}},
			},
			make: func() *http.Request { return goldenApplyRequest(true) },
		},
		{
			name:    "guarded apply outcome undecided",
			schemas: &fakeSchemaService{applyErr: &schema.OperationRecoveryError{}},
			make:    func() *http.Request { return goldenApplyRequest(true) },
		},
		{
			name:    "guarded apply duplicate operation id",
			schemas: &fakeSchemaService{},
			make: func() *http.Request {
				request := goldenApplyRequest(true)
				request.Header.Add(operationIDHeader, "019551d0-e9c0-7000-8000-0000000000ab")
				return request
			},
		},
		{
			name:    "guarded apply malformed idempotency key",
			schemas: &fakeSchemaService{},
			make: func() *http.Request {
				request := goldenApplyRequest(true)
				request.Header.Set(idempotencyKeyHeader, "too short")
				return request
			},
		},
		{
			name:    "apply preflight advertises the identity headers",
			schemas: &fakeSchemaService{},
			make: func() *http.Request {
				request := httptest.NewRequest(http.MethodOptions, "/api/admin/collections/apply", nil)
				request.Host = "admin.example.com"
				request.Header.Set("Origin", "https://admin.example.com")
				request.Header.Set("Access-Control-Request-Method", http.MethodPost)
				request.Header.Set("Access-Control-Request-Headers", "Content-Type, PgBase-Operation-ID, Idempotency-Key")
				return request
			},
		},
	}

	golden := wireGolden{Exchanges: make([]wireExchange, 0, len(tests))}
	for _, test := range tests {
		var logs bytes.Buffer
		dependencies := schemaTestDependencies(
			&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, test.schemas)
		dependencies.CORSAllowedOrigins = []string{"https://admin.example.com"}
		dependencies.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
		response := httptest.NewRecorder()
		request := test.make()
		mustRouter(t, dependencies).ServeHTTP(response, request)
		golden.Exchanges = append(golden.Exchanges,
			captureWireExchange(t, test.name, request.Method, response, logs.Bytes()))
	}

	normalizeWireGolden(&golden)
	assertJSONGolden(t, "testdata/schema_operations_wire.golden.json", golden)
}

func goldenOperationRequest(method, target string) *http.Request {
	request := operationRequest(method, target)
	request.Header.Set("Origin", "https://admin.example.com")
	return request
}

func goldenApplyRequest(guarded bool) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/admin/collections/apply", strings.NewReader(testApplyBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", "fixed-request-id")
	protectSchemaMutationRequest(request)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: testSessionToken})
	if guarded {
		request.Header.Set(operationIDHeader, testOperationID)
		request.Header.Set(idempotencyKeyHeader, testIdempotency)
	}
	return request
}
