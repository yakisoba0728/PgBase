//go:build integration

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

type liveOperationFixture struct {
	pool     *database.Pool
	schemas  *schema.Service
	handlers map[uuid.UUID]http.Handler
}

func newLiveOperationFixture(t *testing.T, administrators ...adminauth.Administrator) liveOperationFixture {
	t.Helper()
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	for _, administrator := range administrators {
		if _, err := pool.Exec(ctx, `insert into pgbase_meta.admins
			(id, email, password_hash, active, created_at, updated_at)
			values ($1, $2, 'test', true, current_timestamp, current_timestamp)`,
			administrator.ID, administrator.Email); err != nil {
			t.Fatal(err)
		}
	}
	// The production wiring passes the token secret as the cursor secret, so
	// the fixture has to as well or history could never mint a cursor.
	schemas := schema.NewService(pool, &schema.Cache{}, nil, testTokenSecret)
	fixture := liveOperationFixture{pool: pool, schemas: schemas, handlers: map[uuid.UUID]http.Handler{}}
	for _, administrator := range administrators {
		fixture.handlers[administrator.ID] = mustRouter(t, schemaTestDependencies(
			&fakeAdminAuth{authenticateAdmin: administrator}, schemas))
	}
	return fixture
}

func (f liveOperationFixture) guardedCreate(
	t *testing.T, administrator uuid.UUID, name string, operationID uuid.UUID, key string,
) *httptest.ResponseRecorder {
	t.Helper()
	definition := schema.CreateCollectionInput{
		Name: name, Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}},
	}
	plan, err := f.schemas.Preview(context.Background(), schema.NewCreateChange(definition))
	if err != nil {
		t.Fatal(err)
	}
	return f.guardedApply(t, administrator, operationID, key, fmt.Sprintf(
		`{"operation":"create","name":%q,"fields":[{"name":"title","type":"text"}],"checksum":%q,"baseVersion":%d}`,
		name, plan.Checksum, plan.BaseVersion))
}

func (f liveOperationFixture) guardedApply(
	t *testing.T, administrator uuid.UUID, operationID uuid.UUID, key, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/collections/apply", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	protectSchemaMutationRequest(request)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: testSessionToken})
	request.Header.Set(operationIDHeader, operationID.String())
	request.Header.Set(idempotencyKeyHeader, key)
	response := httptest.NewRecorder()
	f.handlers[administrator].ServeHTTP(response, request)
	return response
}

func (f liveOperationFixture) history(t *testing.T, administrator uuid.UUID, query string) (schema.OperationPage, int) {
	t.Helper()
	response := httptest.NewRecorder()
	f.handlers[administrator].ServeHTTP(response,
		operationRequest(http.MethodGet, "/api/admin/schema-operations"+query))
	if response.Code != http.StatusOK {
		return schema.OperationPage{}, response.Code
	}
	var page schema.OperationPage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode history %s: %v", response.Body.String(), err)
	}
	return page, response.Code
}

// The HTTP history is a wire adapter over ListOperations, so for one ledger
// state the two must produce the same receipts in the same order across every
// page, including the (created_at DESC, operation_id DESC) tie-break that only
// appears when two operations share a timestamp.
func TestLiveOperationHistoryWalksExactlyLikeTheKernel(t *testing.T) {
	ctx := context.Background()
	administrator := validSession().Administrator
	fixture := newLiveOperationFixture(t, administrator)

	operationIDs := make([]uuid.UUID, 0, 5)
	for index := range 5 {
		operationID := uuid.New()
		response := fixture.guardedCreate(t, administrator.ID, fmt.Sprintf("ledger_history_%d", index),
			operationID, fmt.Sprintf("ledger-history-key-%016d", index))
		if response.Code != http.StatusCreated {
			t.Fatalf("guarded create %d status = %d body = %s", index, response.Code, response.Body.String())
		}
		operationIDs = append(operationIDs, operationID)
	}

	// Two receipts are forced onto one timestamp so the tie-break is the only
	// thing that can order them; without this the wall clock does it and the
	// comparison proves nothing about tie ordering.
	if _, err := fixture.pool.Exec(ctx, `update pgbase_meta.schema_operations
		set created_at = (select min(created_at) from pgbase_meta.schema_operations)
		where operation_id = any($1)`, operationIDs[3:]); err != nil {
		t.Fatal(err)
	}

	cursor := ""
	kernelCursor := ""
	pages := 0
	for {
		query := "?limit=2"
		if cursor != "" {
			query += "&cursor=" + cursor
		}
		page, status := fixture.history(t, administrator.ID, query)
		if status != http.StatusOK {
			t.Fatalf("history page %d status = %d", pages, status)
		}
		kernelQuery, err := schema.NewOperationQuery(administrator.ID, 2, kernelCursor)
		if err != nil {
			t.Fatal(err)
		}
		kernelPage, err := fixture.schemas.ListOperations(ctx, kernelQuery)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != len(kernelPage.Items) {
			t.Fatalf("page %d has %d items, ListOperations has %d", pages, len(page.Items), len(kernelPage.Items))
		}
		for index := range page.Items {
			if page.Items[index].OperationID != kernelPage.Items[index].OperationID {
				t.Fatalf("page %d item %d = %s, ListOperations = %s",
					pages, index, page.Items[index].OperationID, kernelPage.Items[index].OperationID)
			}
		}
		if (page.NextCursor == "") != (kernelPage.NextCursor == "") {
			t.Fatalf("page %d cursor presence = %t, ListOperations = %t",
				pages, page.NextCursor != "", kernelPage.NextCursor != "")
		}
		pages++
		cursor, kernelCursor = page.NextCursor, kernelPage.NextCursor
		if cursor == "" {
			break
		}
		if pages > 10 {
			t.Fatal("history paging did not terminate")
		}
	}
	if pages != 3 {
		t.Fatalf("walked %d pages of 5 receipts at limit 2, want 3", pages)
	}
}

// A receipt belongs to the administrator who created it. Another
// administrator must not be able to tell an operation that exists from one
// that does not, nor reuse a cursor minted for someone else.
func TestLiveOperationRecoveryIsScopedToItsOwner(t *testing.T) {
	owner := validSession().Administrator
	intruder := owner
	intruder.ID = uuid.MustParse("019551d0-e9c0-7000-8000-00000000f00d")
	intruder.Email = "intruder@example.com"
	fixture := newLiveOperationFixture(t, owner, intruder)

	operationID := uuid.New()
	if response := fixture.guardedCreate(t, owner.ID, "ledger_owned", operationID, "ledger-owned-key-0000000000"); response.Code != http.StatusCreated {
		t.Fatalf("guarded create status = %d body = %s", response.Code, response.Body.String())
	}

	ownerStatus := httptest.NewRecorder()
	fixture.handlers[owner.ID].ServeHTTP(ownerStatus,
		operationRequest(http.MethodGet, "/api/admin/schema-operations/"+operationID.String()))
	if ownerStatus.Code != http.StatusOK {
		t.Fatalf("owner status = %d body = %s", ownerStatus.Code, ownerStatus.Body.String())
	}
	var receipt schema.OperationReceipt
	if err := json.Unmarshal(ownerStatus.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Status != schema.OperationCommitted || receipt.CommittedVersion == 0 {
		t.Fatalf("owner receipt = %+v, want a committed version", receipt)
	}

	existing := httptest.NewRecorder()
	fixture.handlers[intruder.ID].ServeHTTP(existing,
		operationRequest(http.MethodGet, "/api/admin/schema-operations/"+operationID.String()))
	absent := httptest.NewRecorder()
	fixture.handlers[intruder.ID].ServeHTTP(absent,
		operationRequest(http.MethodGet, "/api/admin/schema-operations/"+uuid.New().String()))
	if existing.Code != http.StatusNotFound || existing.Body.String() != absent.Body.String() {
		t.Fatalf("another administrator distinguished an existing operation: %d %s vs %d %s",
			existing.Code, existing.Body.String(), absent.Code, absent.Body.String())
	}

	if page, status := fixture.history(t, intruder.ID, ""); status != http.StatusOK || len(page.Items) != 0 {
		t.Fatalf("another administrator read %d receipts (status %d)", len(page.Items), status)
	}

	for index := range 2 {
		second := uuid.New()
		if response := fixture.guardedCreate(t, owner.ID, fmt.Sprintf("ledger_owned_more_%d", index),
			second, fmt.Sprintf("ledger-owned-more-key-%08d", index)); response.Code != http.StatusCreated {
			t.Fatalf("guarded create status = %d body = %s", response.Code, response.Body.String())
		}
	}
	ownerPage, status := fixture.history(t, owner.ID, "?limit=1")
	if status != http.StatusOK || ownerPage.NextCursor == "" {
		t.Fatalf("owner history status = %d cursor = %q", status, ownerPage.NextCursor)
	}
	reused := httptest.NewRecorder()
	fixture.handlers[intruder.ID].ServeHTTP(reused,
		operationRequest(http.MethodGet, "/api/admin/schema-operations?cursor="+ownerPage.NextCursor))
	if reused.Code != http.StatusBadRequest {
		t.Fatalf("reused cursor status = %d body = %s", reused.Code, reused.Body.String())
	}
	assertErrorCode(t, reused.Body.Bytes(), "invalid_cursor")
}

// The same operation ID and idempotency key replay the receipt they already
// own; changing either identity member or the request is a conflict. Neither
// answer may be produced from anything but the durable ledger row.
func TestLiveGuardedApplyReplaysAndRefusesADifferentRequest(t *testing.T) {
	administrator := validSession().Administrator
	fixture := newLiveOperationFixture(t, administrator)

	definition := schema.CreateCollectionInput{
		Name: "ledger_replayed", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}},
	}
	plan, err := fixture.schemas.Preview(context.Background(), schema.NewCreateChange(definition))
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(
		`{"operation":"create","name":"ledger_replayed","fields":[{"name":"title","type":"text"}],"checksum":%q,"baseVersion":%d}`,
		plan.Checksum, plan.BaseVersion)
	operationID := uuid.New()
	const key = "ledger-replay-key-000000000000"

	first := fixture.guardedApply(t, administrator.ID, operationID, key, body)
	if first.Code != http.StatusCreated || first.Header().Get("Idempotency-Replayed") != "false" {
		t.Fatalf("first apply status = %d headers = %v body = %s", first.Code, first.Header(), first.Body.String())
	}

	replay := fixture.guardedApply(t, administrator.ID, operationID, key, body)
	if replay.Code != http.StatusOK || replay.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay status = %d headers = %v body = %s", replay.Code, replay.Header(), replay.Body.String())
	}
	outcome := decodeOperationOutcome(t, replay.Body.Bytes())
	if outcome.Status != schema.OperationCommitted || outcome.OperationID != operationID {
		t.Fatalf("replay outcome = %+v", outcome)
	}

	alternateID := uuid.New()
	alternate := fixture.guardedApply(t, administrator.ID, alternateID, key, body)
	if alternate.Code != http.StatusConflict {
		t.Fatalf("alternate operation ID status = %d body = %s", alternate.Code, alternate.Body.String())
	}
	assertErrorCode(t, alternate.Body.Bytes(), "schema_operation_conflict")
	original := httptest.NewRecorder()
	fixture.handlers[administrator.ID].ServeHTTP(original,
		operationRequest(http.MethodGet, "/api/admin/schema-operations/"+operationID.String()))
	var originalReceipt schema.OperationReceipt
	if original.Code != http.StatusOK {
		t.Fatalf("original receipt status = %d body = %s", original.Code, original.Body.String())
	}
	if err := json.Unmarshal(original.Body.Bytes(), &originalReceipt); err != nil {
		t.Fatal(err)
	}
	if outcome.Receipt == nil || !reflect.DeepEqual(originalReceipt, *outcome.Receipt) {
		t.Fatalf("receipt after alternate ID = %+v, want original %+v", originalReceipt, outcome.Receipt)
	}

	otherPlan, err := fixture.schemas.Preview(context.Background(), schema.NewCreateChange(schema.CreateCollectionInput{
		Name: "ledger_conflicting", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	conflicting := fixture.guardedApply(t, administrator.ID, operationID, key, fmt.Sprintf(
		`{"operation":"create","name":"ledger_conflicting","fields":[{"name":"title","type":"text"}],"checksum":%q,"baseVersion":%d}`,
		otherPlan.Checksum, otherPlan.BaseVersion))
	if conflicting.Code != http.StatusConflict {
		t.Fatalf("conflicting request status = %d body = %s", conflicting.Code, conflicting.Body.String())
	}
	assertErrorCode(t, conflicting.Body.Bytes(), "schema_operation_conflict")
}

// A cancelled apply must never be answered from transport guesswork. The
// ledger decides, and a request that raced a real commit is told the operation
// committed rather than being handed a failure the ledger contradicts.
func TestLiveGuardedApplyAnswersACancelledRequestFromTheLedger(t *testing.T) {
	administrator := validSession().Administrator
	fixture := newLiveOperationFixture(t, administrator)

	definition := schema.CreateCollectionInput{
		Name: "ledger_cancelled", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}},
	}
	plan, err := fixture.schemas.Preview(context.Background(), schema.NewCreateChange(definition))
	if err != nil {
		t.Fatal(err)
	}
	operationID := uuid.New()
	const key = "ledger-cancel-key-000000000000"
	body := fmt.Sprintf(
		`{"operation":"create","name":"ledger_cancelled","fields":[{"name":"title","type":"text"}],"checksum":%q,"baseVersion":%d}`,
		plan.Checksum, plan.BaseVersion)
	if response := fixture.guardedApply(t, administrator.ID, operationID, key, body); response.Code != http.StatusCreated {
		t.Fatalf("apply status = %d body = %s", response.Code, response.Body.String())
	}

	// The retry arrives on a context that is already gone, which is exactly
	// the shape of a client whose first response was lost. The ledger already
	// holds the committed receipt, so the answer must be that receipt.
	request := httptest.NewRequest(http.MethodPost, "/api/admin/collections/apply", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	protectSchemaMutationRequest(request)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: testSessionToken})
	request.Header.Set(operationIDHeader, operationID.String())
	request.Header.Set(idempotencyKeyHeader, key)
	cancelled, cancel := context.WithCancel(request.Context())
	cancel()
	response := httptest.NewRecorder()
	fixture.handlers[administrator.ID].ServeHTTP(response, request.WithContext(cancelled))

	if response.Code == http.StatusInternalServerError || response.Code == http.StatusServiceUnavailable {
		t.Fatalf("cancelled apply guessed a failure: %d %s", response.Code, response.Body.String())
	}
	if response.Code != http.StatusOK {
		t.Fatalf("cancelled apply status = %d body = %s", response.Code, response.Body.String())
	}
	// A replay would have set this header. Its absence is what says the
	// answer came from the recovery read rather than from a completed apply.
	if got := response.Header().Get("Idempotency-Replayed"); got != "" {
		t.Fatalf("cancelled apply answered as a replay: Idempotency-Replayed = %q", got)
	}
	outcome := decodeOperationOutcome(t, response.Body.Bytes())
	if outcome.Status != schema.OperationCommitted {
		t.Fatalf("cancelled apply outcome = %+v, want the committed ledger state", outcome)
	}
}
