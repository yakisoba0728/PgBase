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
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const (
	testOperationID  = "019551d0-e9c0-7000-8000-0000000000aa"
	testChecksum     = "3f2a1c0b5d4e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8"
	testIdempotency  = "operation-key-0123456789abcdef"
	testApplyBody    = `{"operation":"create","name":"posts","fields":[{"name":"title","type":"text"}],"checksum":"` + testChecksum + `","baseVersion":7}`
	testSessionToken = "fixed-session-token"
)

func operationRequest(method, target string) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	request.Host = "admin.example.com"
	// A fixed request ID lets two rejections be compared byte for byte; the
	// generated one is the only part of the envelope that legitimately
	// differs between them.
	request.Header.Set("X-Request-ID", "fixed-request-id")
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: testSessionToken})
	return request
}

func applyRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/collections/apply", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	protectSchemaMutationRequest(request)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: testSessionToken})
	return request
}

func schemaOperationHandler(t *testing.T, schemas *fakeSchemaService) http.Handler {
	t.Helper()
	return mustRouter(t, schemaTestDependencies(
		&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, schemas))
}

func decodeOperationOutcome(t *testing.T, body []byte) schemaOperationOutcome {
	t.Helper()
	var outcome schemaOperationOutcome
	if err := json.Unmarshal(body, &outcome); err != nil {
		t.Fatalf("decode operation outcome %s: %v", body, err)
	}
	return outcome
}

// The receipt belongs to one administrator, so the route has to send the
// authenticated administrator into the ref rather than trusting the path.
func TestSchemaOperationStatusScopesTheReceiptToTheCurrentAdministrator(t *testing.T) {
	operationID := uuid.MustParse(testOperationID)
	schemas := &fakeSchemaService{statusReceipt: schema.OperationReceipt{
		OperationID: operationID, Operation: schema.SchemaOperationCollectionCreate,
		Status: schema.OperationCommitted, BaseVersion: 7, CommittedVersion: 8,
	}}
	response := httptest.NewRecorder()
	schemaOperationHandler(t, schemas).ServeHTTP(response,
		operationRequest(http.MethodGet, "/api/admin/schema-operations/"+testOperationID))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	wantRef, err := schema.NewOperationRef(validSession().Administrator.ID, operationID)
	if err != nil {
		t.Fatal(err)
	}
	if schemas.statusRef != wantRef {
		t.Fatalf("operation ref = %+v, want the authenticated administrator and %s", schemas.statusRef, operationID)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

// A receipt is redacted by construction. This pins the exact key set so a new
// ledger column cannot reach the wire by being added to the domain struct.
func TestSchemaOperationStatusExposesOnlyTheRedactedReceiptFields(t *testing.T) {
	completedAt := time.Date(2026, time.September, 1, 2, 3, 4, 0, time.UTC)
	schemas := &fakeSchemaService{statusReceipt: schema.OperationReceipt{
		OperationID: uuid.MustParse(testOperationID), Operation: schema.SchemaOperationCollectionCreate,
		CollectionID: uuid.MustParse("019551d0-e9c0-7000-8000-0000000000bb"),
		Status:       schema.OperationCommitted, BaseVersion: 7, CommittedVersion: 8,
		CreatedAt: completedAt, UpdatedAt: completedAt, CompletedAt: &completedAt,
	}}
	response := httptest.NewRecorder()
	schemaOperationHandler(t, schemas).ServeHTTP(response,
		operationRequest(http.MethodGet, "/api/admin/schema-operations/"+testOperationID))

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"operationId": true, "operation": true, "collectionId": true, "status": true,
		"baseVersion": true, "committedVersion": true, "createdAt": true,
		"updatedAt": true, "completedAt": true,
	}
	for key := range decoded {
		if !want[key] {
			t.Fatalf("receipt exposed unexpected member %q: %s", key, response.Body.String())
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("receipt is missing members %v: %s", want, response.Body.String())
	}
}

// A tampered ID, an ID that belongs to someone else, and an ID that never
// existed have to be indistinguishable. Anything else turns the status route
// into a way to enumerate other administrators' operations.
func TestSchemaOperationStatusGivesOneShapeToEveryUnreadableID(t *testing.T) {
	notFound := &fakeSchemaService{statusErr: schema.ErrOperationNotFound}
	handler := schemaOperationHandler(t, notFound)
	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, operationRequest(http.MethodGet, "/api/admin/schema-operations/"+testOperationID))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("other-owner status = %d, body = %s", unknown.Code, unknown.Body.String())
	}
	assertErrorCode(t, unknown.Body.Bytes(), "not_found")

	for _, tampered := range []string{
		"not-a-uuid",
		"00000000-0000-0000-0000-000000000000",
		"019551d0e9c070008000-0000000000aa",
	} {
		t.Run(tampered, func(t *testing.T) {
			schemas := &fakeSchemaService{statusErr: schema.ErrOperationNotFound}
			response := httptest.NewRecorder()
			schemaOperationHandler(t, schemas).ServeHTTP(response,
				operationRequest(http.MethodGet, "/api/admin/schema-operations/"+tampered))
			if response.Code != unknown.Code || response.Body.String() != unknown.Body.String() {
				t.Fatalf("tampered ID answered %d %s, want the unreadable-ID shape %d %s",
					response.Code, response.Body.String(), unknown.Code, unknown.Body.String())
			}
			if schemas.statusCalls != 0 {
				t.Fatalf("tampered ID reached the schema module %d times", schemas.statusCalls)
			}
		})
	}
}

func TestSchemaOperationRoutesRequireAnAdministratorSession(t *testing.T) {
	for _, target := range []string{
		"/api/admin/schema-operations",
		"/api/admin/schema-operations/" + testOperationID,
	} {
		t.Run(target, func(t *testing.T) {
			schemas := &fakeSchemaService{}
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, target, nil)
			request.Host = "admin.example.com"
			schemaOperationHandler(t, schemas).ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			assertErrorCode(t, response.Body.Bytes(), "authentication_required")
			if schemas.statusCalls != 0 || schemas.listCalls != 0 {
				t.Fatalf("unauthenticated request reached the schema module: status=%d list=%d",
					schemas.statusCalls, schemas.listCalls)
			}
		})
	}
}

// The limit the route accepts and the limit SchemaKernel accepts have to be
// the same set, including the default a missing or zero limit selects.
func TestSchemaOperationHistoryBoundsTheLimitExactlyLikeTheKernel(t *testing.T) {
	administrator := validSession().Administrator.ID
	for _, testCase := range []struct {
		query     string
		wantLimit int
	}{
		{query: "", wantLimit: 20},
		{query: "?limit=1", wantLimit: 1},
		{query: "?limit=20", wantLimit: 20},
		{query: "?limit=100", wantLimit: 100},
		{query: "?limit=0", wantLimit: 20},
	} {
		t.Run("accepted"+testCase.query, func(t *testing.T) {
			schemas := &fakeSchemaService{}
			response := httptest.NewRecorder()
			schemaOperationHandler(t, schemas).ServeHTTP(response,
				operationRequest(http.MethodGet, "/api/admin/schema-operations"+testCase.query))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			want, err := schema.NewOperationQuery(administrator, testCase.wantLimit, "")
			if err != nil {
				t.Fatal(err)
			}
			if schemas.listQuery != want {
				t.Fatalf("query = %+v, want the owner scope with limit %d", schemas.listQuery, testCase.wantLimit)
			}
		})
	}

	for _, query := range []string{"?limit=101", "?limit=-1", "?limit=abc", "?limit=1.5", "?limit="} {
		t.Run("rejected"+query, func(t *testing.T) {
			schemas := &fakeSchemaService{}
			response := httptest.NewRecorder()
			schemaOperationHandler(t, schemas).ServeHTTP(response,
				operationRequest(http.MethodGet, "/api/admin/schema-operations"+query))
			if query == "?limit=" {
				// An empty value is an absent limit, not a rejected one.
				if response.Code != http.StatusOK {
					t.Fatalf("empty limit status = %d, body = %s", response.Code, response.Body.String())
				}
				return
			}
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			assertErrorCode(t, response.Body.Bytes(), "invalid_request")
			if schemas.listCalls != 0 {
				t.Fatalf("rejected limit reached the schema module %d times", schemas.listCalls)
			}
		})
	}
}

// A forged, expired, reused or oversized cursor is one answer. Splitting them
// apart would tell a caller which property of a stolen cursor still held.
func TestSchemaOperationHistoryAnswersEveryUnusableCursorTheSameWay(t *testing.T) {
	oversized := &fakeSchemaService{}
	response := httptest.NewRecorder()
	schemaOperationHandler(t, oversized).ServeHTTP(response,
		operationRequest(http.MethodGet, "/api/admin/schema-operations?cursor="+strings.Repeat("a", 600)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized cursor status = %d, body = %s", response.Code, response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "invalid_cursor")
	if oversized.listCalls != 0 {
		t.Fatalf("oversized cursor reached the schema module %d times", oversized.listCalls)
	}

	rejected := &fakeSchemaService{listErr: schema.ErrInvalidOperation}
	tampered := httptest.NewRecorder()
	schemaOperationHandler(t, rejected).ServeHTTP(tampered,
		operationRequest(http.MethodGet, "/api/admin/schema-operations?cursor=forged-cursor"))
	if tampered.Code != response.Code || tampered.Body.String() != response.Body.String() {
		t.Fatalf("tampered cursor answered %d %s, want the oversized-cursor shape %d %s",
			tampered.Code, tampered.Body.String(), response.Code, response.Body.String())
	}
}

// The route is a wire adapter: it must not re-sort, trim or re-page what the
// ledger already ordered by (created_at DESC, operation_id DESC).
func TestSchemaOperationHistoryPreservesTheKernelPageVerbatim(t *testing.T) {
	newest := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	page := schema.OperationPage{
		Items: []schema.OperationReceipt{
			{OperationID: uuid.MustParse("019551d0-e9c0-7000-8000-0000000000cc"), Status: schema.OperationCommitted, CreatedAt: newest},
			{OperationID: uuid.MustParse("019551d0-e9c0-7000-8000-0000000000bb"), Status: schema.OperationFailed, CreatedAt: newest},
			{OperationID: uuid.MustParse("019551d0-e9c0-7000-8000-0000000000aa"), Status: schema.OperationCommitted, CreatedAt: newest.Add(-time.Hour)},
		},
		NextCursor: "opaque-owner-scoped-cursor",
	}
	// Recorded before serving: a handler that reorders the kernel page in
	// place would otherwise reorder the expectation with it.
	want := make([]uuid.UUID, 0, len(page.Items))
	for _, item := range page.Items {
		want = append(want, item.OperationID)
	}
	schemas := &fakeSchemaService{listPage: page}
	response := httptest.NewRecorder()
	schemaOperationHandler(t, schemas).ServeHTTP(response,
		operationRequest(http.MethodGet, "/api/admin/schema-operations?limit=3"))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var decoded schema.OperationPage
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.NextCursor != "opaque-owner-scoped-cursor" || len(decoded.Items) != len(want) {
		t.Fatalf("page = %+v, want the kernel page", decoded)
	}
	for index, item := range decoded.Items {
		if item.OperationID != want[index] {
			t.Fatalf("item %d = %s, want %s; the route reordered the kernel page",
				index, item.OperationID, want[index])
		}
	}
}

// A headerless apply is the contract existing clients already depend on. It
// keeps its status, its body and its code path; only the header that says it
// gave up recovery is new.
func TestHeaderlessSchemaApplyKeepsTheLegacyPathBodyAndStatus(t *testing.T) {
	createdAt := time.Date(2026, time.August, 28, 4, 5, 6, 0, time.UTC)
	schemas := &fakeSchemaService{applied: schema.Collection{
		ID: uuid.MustParse("019551d0-e9c0-7000-8000-000000000010"), Name: "posts", TableName: "posts", CreatedAt: createdAt,
	}}
	response := httptest.NewRecorder()
	schemaOperationHandler(t, schemas).ServeHTTP(response, applyRequest(t, testApplyBody))

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if schemas.applyCalls != 1 || schemas.guardedApplyCalls != 0 {
		t.Fatalf("legacy apply calls = %d, guarded apply calls = %d", schemas.applyCalls, schemas.guardedApplyCalls)
	}
	if got := response.Header().Get("PgBase-Compatibility"); got != schemaApplyUnguardedCompatibility {
		t.Fatalf("PgBase-Compatibility = %q, want %q", got, schemaApplyUnguardedCompatibility)
	}
	var body collectionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != schemas.applied.ID || body.Name != "posts" {
		t.Fatalf("legacy apply body = %+v", body)
	}
}

// Half an identity, a duplicated one, or one the ledger could never store is
// a client bug. It ends before either apply path runs, so a rejected request
// never reaches a domain module.
func TestSchemaApplyRejectsAnUnusableOperationIdentityBeforeAnyDomainCall(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		prepare func(*http.Request)
	}{
		{name: "operation id without key", prepare: func(r *http.Request) {
			r.Header.Set(operationIDHeader, testOperationID)
		}},
		{name: "key without operation id", prepare: func(r *http.Request) {
			r.Header.Set(idempotencyKeyHeader, testIdempotency)
		}},
		{name: "duplicate operation id", prepare: func(r *http.Request) {
			r.Header.Add(operationIDHeader, testOperationID)
			r.Header.Add(operationIDHeader, "019551d0-e9c0-7000-8000-0000000000ab")
			r.Header.Set(idempotencyKeyHeader, testIdempotency)
		}},
		{name: "duplicate idempotency key", prepare: func(r *http.Request) {
			r.Header.Set(operationIDHeader, testOperationID)
			r.Header.Add(idempotencyKeyHeader, testIdempotency)
			r.Header.Add(idempotencyKeyHeader, testIdempotency+"-two")
		}},
		{name: "malformed operation id", prepare: func(r *http.Request) {
			r.Header.Set(operationIDHeader, "not-a-uuid")
			r.Header.Set(idempotencyKeyHeader, testIdempotency)
		}},
		{name: "nil operation id", prepare: func(r *http.Request) {
			r.Header.Set(operationIDHeader, "00000000-0000-0000-0000-000000000000")
			r.Header.Set(idempotencyKeyHeader, testIdempotency)
		}},
		{name: "idempotency key too short", prepare: func(r *http.Request) {
			r.Header.Set(operationIDHeader, testOperationID)
			r.Header.Set(idempotencyKeyHeader, "short")
		}},
		{name: "idempotency key with an illegal character", prepare: func(r *http.Request) {
			r.Header.Set(operationIDHeader, testOperationID)
			r.Header.Set(idempotencyKeyHeader, "operation key 0123456789abcdef")
		}},
		{name: "checksum that is not a plan checksum", prepare: func(r *http.Request) {
			r.Header.Set(operationIDHeader, testOperationID)
			r.Header.Set(idempotencyKeyHeader, testIdempotency)
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			body := testApplyBody
			if testCase.name == "checksum that is not a plan checksum" {
				body = `{"operation":"create","name":"posts","fields":[],"checksum":"not-hex","baseVersion":7}`
			}
			schemas := &fakeSchemaService{}
			request := applyRequest(t, body)
			testCase.prepare(request)
			response := httptest.NewRecorder()
			schemaOperationHandler(t, schemas).ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			assertErrorCode(t, response.Body.Bytes(), "invalid_request")
			if schemas.applyCalls != 0 || schemas.guardedApplyCalls != 0 {
				t.Fatalf("rejected identity reached the schema module: legacy=%d guarded=%d",
					schemas.applyCalls, schemas.guardedApplyCalls)
			}
		})
	}
}

func TestGuardedSchemaApplyReturnsTheCollectionAndTheOperationIdentity(t *testing.T) {
	createdAt := time.Date(2026, time.August, 28, 4, 5, 6, 0, time.UTC)
	schemas := &fakeSchemaService{guardedApplyResult: schema.ApplyResult{
		Collection: schema.Collection{
			ID: uuid.MustParse("019551d0-e9c0-7000-8000-000000000010"), Name: "posts",
			TableName: "posts", CreatedAt: createdAt,
		},
	}}
	request := applyRequest(t, testApplyBody)
	request.Header.Set(operationIDHeader, testOperationID)
	request.Header.Set(idempotencyKeyHeader, testIdempotency)
	response := httptest.NewRecorder()
	schemaOperationHandler(t, schemas).ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if schemas.guardedApplyCalls != 1 || schemas.applyCalls != 0 {
		t.Fatalf("guarded apply calls = %d, legacy apply calls = %d", schemas.guardedApplyCalls, schemas.applyCalls)
	}
	if got := response.Header().Get(operationIDHeader); got != testOperationID {
		t.Fatalf("%s = %q, want %q", operationIDHeader, got, testOperationID)
	}
	if got := response.Header().Get("Idempotency-Replayed"); got != "false" {
		t.Fatalf("Idempotency-Replayed = %q, want false", got)
	}
	if got := response.Header().Get("PgBase-Compatibility"); got != "" {
		t.Fatalf("guarded apply advertised the unguarded compatibility header: %q", got)
	}
}

// The ledger stores a receipt, not the collection the first attempt returned,
// so the replay says what it knows and points at the receipt.
func TestGuardedSchemaApplyReplayAnswersWithTheReceipt(t *testing.T) {
	operationID := uuid.MustParse(testOperationID)
	schemas := &fakeSchemaService{guardedApplyResult: schema.ApplyResult{
		Replayed: true,
		Receipt: schema.OperationReceipt{
			OperationID: operationID, Status: schema.OperationCommitted,
			BaseVersion: 7, CommittedVersion: 8,
		},
	}}
	request := applyRequest(t, testApplyBody)
	request.Header.Set(operationIDHeader, testOperationID)
	request.Header.Set(idempotencyKeyHeader, testIdempotency)
	response := httptest.NewRecorder()
	schemaOperationHandler(t, schemas).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Idempotency-Replayed"); got != "true" {
		t.Fatalf("Idempotency-Replayed = %q, want true", got)
	}
	outcome := decodeOperationOutcome(t, response.Body.Bytes())
	if outcome.Status != schema.OperationCommitted || outcome.StatusURL != "/api/admin/schema-operations/"+testOperationID {
		t.Fatalf("replay outcome = %+v", outcome)
	}
}

// The same identity carrying a different request is a conflict the ledger
// detects. The guarded code says which conflict it was; the legacy path keeps
// the code it has always returned.
func TestSchemaOperationConflictSeparatesGuardedFromLegacy(t *testing.T) {
	guarded := &fakeSchemaService{guardedApplyErr: schema.ErrOperationConflict}
	request := applyRequest(t, testApplyBody)
	request.Header.Set(operationIDHeader, testOperationID)
	request.Header.Set(idempotencyKeyHeader, testIdempotency)
	response := httptest.NewRecorder()
	schemaOperationHandler(t, guarded).ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("guarded status = %d, body = %s", response.Code, response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "schema_operation_conflict")

	legacy := &fakeSchemaService{applyErr: schema.ErrOperationConflict}
	legacyResponse := httptest.NewRecorder()
	schemaOperationHandler(t, legacy).ServeHTTP(legacyResponse, applyRequest(t, testApplyBody))
	if legacyResponse.Code != http.StatusConflict {
		t.Fatalf("legacy status = %d, body = %s", legacyResponse.Code, legacyResponse.Body.String())
	}
	assertErrorCode(t, legacyResponse.Body.Bytes(), "schema_conflict")
}

// An apply the ledger has not decided ends pointing at where the verdict will
// appear. Returning a failure here would be transport guesswork the ledger can
// contradict a second later.
func TestGuardedSchemaApplyEndsUndecidedWithTheRecoveryURL(t *testing.T) {
	schemas := &fakeSchemaService{guardedApplyErr: &schema.OperationRecoveryError{}}
	request := applyRequest(t, testApplyBody)
	request.Header.Set(operationIDHeader, testOperationID)
	request.Header.Set(idempotencyKeyHeader, testIdempotency)
	response := httptest.NewRecorder()
	schemaOperationHandler(t, schemas).ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	statusURL := "/api/admin/schema-operations/" + testOperationID
	if got := response.Header().Get("Location"); got != statusURL {
		t.Fatalf("Location = %q, want %q", got, statusURL)
	}
	if got := response.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
	outcome := decodeOperationOutcome(t, response.Body.Bytes())
	if outcome.Status != schema.OperationUnknown || outcome.StatusURL != statusURL {
		t.Fatalf("undecided outcome = %+v", outcome)
	}
}

// A request context that expires races the commit acknowledgement. The answer
// has to come from the ledger, so a committed operation is never reported as a
// failure and an undecided one never as anything but undecided.
func TestGuardedSchemaApplyReadsTheLedgerWhenItsOwnContextExpires(t *testing.T) {
	operationID := uuid.MustParse(testOperationID)
	statusURL := "/api/admin/schema-operations/" + testOperationID
	for _, testCase := range []struct {
		name       string
		receipt    schema.OperationReceipt
		statusErr  error
		wantStatus int
		wantState  schema.OperationStatus
		wantCode   string
	}{
		{
			name: "ledger says committed", wantStatus: http.StatusOK, wantState: schema.OperationCommitted,
			receipt: schema.OperationReceipt{OperationID: operationID, Status: schema.OperationCommitted, CommittedVersion: 8},
		},
		{
			name: "ledger says running", wantStatus: http.StatusAccepted, wantState: schema.OperationRunning,
			receipt: schema.OperationReceipt{OperationID: operationID, Status: schema.OperationRunning},
		},
		{
			name: "ledger says unknown", wantStatus: http.StatusAccepted, wantState: schema.OperationUnknown,
			receipt: schema.OperationReceipt{OperationID: operationID, Status: schema.OperationUnknown},
		},
		{
			name: "ledger says failed", wantStatus: http.StatusConflict, wantCode: "schema_operation_failed",
			receipt: schema.OperationReceipt{OperationID: operationID, Status: schema.OperationFailed,
				FailureCode: schema.OperationFailureApplyFailed},
		},
		{
			name: "ledger cannot be read", wantStatus: http.StatusAccepted, wantState: schema.OperationUnknown,
			statusErr: context.DeadlineExceeded,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			schemas := &fakeSchemaService{statusReceipt: testCase.receipt, statusErr: testCase.statusErr}
			schemas.guardedApplyHook = func(ctx context.Context) (schema.ApplyResult, error) {
				return schema.ApplyResult{}, ctx.Err()
			}
			request := applyRequest(t, testApplyBody)
			request.Header.Set(operationIDHeader, testOperationID)
			request.Header.Set(idempotencyKeyHeader, testIdempotency)
			expired, cancel := context.WithCancel(request.Context())
			cancel()
			response := httptest.NewRecorder()
			schemaOperationHandler(t, schemas).ServeHTTP(response, request.WithContext(expired))

			if response.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, testCase.wantStatus, response.Body.String())
			}
			if schemas.statusCalls != 1 {
				t.Fatalf("ledger reads = %d, want exactly one", schemas.statusCalls)
			}
			if testCase.wantCode != "" {
				assertErrorCode(t, response.Body.Bytes(), testCase.wantCode)
				return
			}
			outcome := decodeOperationOutcome(t, response.Body.Bytes())
			if outcome.Status != testCase.wantState || outcome.StatusURL != statusURL {
				t.Fatalf("outcome = %+v, want status %s", outcome, testCase.wantState)
			}
			if testCase.wantStatus == http.StatusAccepted && response.Header().Get("Location") != statusURL {
				t.Fatalf("Location = %q, want %q", response.Header().Get("Location"), statusURL)
			}
		})
	}
}

// The identity headers are useless to a browser client unless CORS both
// accepts them on the request and exposes the echoed one on the response.
func TestCORSCarriesTheOperationIdentityHeaders(t *testing.T) {
	handler := mustRouter(t, Dependencies{CORSAllowedOrigins: []string{"https://admin.example"}})
	preflight := httptest.NewRequest(http.MethodOptions, "/api/admin/collections/apply", nil)
	preflight.Header.Set("Origin", "https://admin.example")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Access-Control-Request-Headers", "Content-Type, PgBase-Operation-ID, Idempotency-Key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, preflight)

	if response.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, operationIDHeader) {
		t.Fatalf("Access-Control-Allow-Headers = %q, want it to carry %s", got, operationIDHeader)
	}
	if got := response.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(got, operationIDHeader) {
		t.Fatalf("Access-Control-Expose-Headers = %q, want it to carry %s", got, operationIDHeader)
	}
}
