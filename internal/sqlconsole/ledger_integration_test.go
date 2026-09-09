package sqlconsole

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestLedgerOwnsIdempotentRedactedLifecycleAndHistory(t *testing.T) {
	fixture := newPoolFixture(t)
	ledger, err := NewLedger(fixture.owner)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("n8-ledger-test-secret-that-is-long-enough")
	request := Request{ExecutionID: uuid.New(), Mode: ModeRead, Statements: []Statement{{SQL: "select 1"}}}
	identity := ledgerIdentity(fixture.actorID, request, secret)

	pending, created, err := ledger.CreatePending(context.Background(), identity)
	if err != nil || !created || pending.Status != StatusPending {
		t.Fatalf("CreatePending = %#v, %v, %v", pending, created, err)
	}
	replayed, created, err := ledger.CreatePending(context.Background(), identity)
	if err != nil || created || replayed.ID != request.ExecutionID {
		t.Fatalf("replay = %#v, %v, %v", replayed, created, err)
	}
	conflict := identity
	conflict.Request.Statements[0].SQL = "select 2"
	conflict.RequestHash = RequestDigest(secret, conflict.Request)
	if _, _, err := ledger.CreatePending(context.Background(), conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict error = %v", err)
	}

	running, err := ledger.MarkRunning(context.Background(), fixture.actorID, request.ExecutionID)
	if err != nil || running.Status != StatusRunning || running.StartedAt.IsZero() {
		t.Fatalf("MarkRunning = %#v, %v", running, err)
	}
	committed := false
	running.Status = StatusSucceeded
	running.Result = &Result{Rows: 1, Bytes: 37, Committed: &committed}
	if err := ledger.Finish(context.Background(), fixture.actorID, running); err != nil {
		t.Fatal(err)
	}
	durable, err := ledger.Get(context.Background(), fixture.actorID, request.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if durable.Status != StatusSucceeded || durable.Result == nil || durable.Result.Rows != 1 || durable.Result.Bytes != 37 || durable.Result.Committed == nil || *durable.Result.Committed {
		t.Fatalf("durable execution = %#v", durable)
	}
	if durable.Result.Statements != nil {
		t.Fatal("durable ledger exposed statement results")
	}
	page, err := ledger.List(context.Background(), fixture.actorID, Cursor{})
	if err != nil || len(page.Executions) != 1 || page.Executions[0].ID != request.ExecutionID {
		t.Fatalf("List = %#v, %v", page, err)
	}
}

func TestLedgerReconcilesReceiptAndAbandonsUnstartedWork(t *testing.T) {
	fixture := newPoolFixture(t)
	ledger, _ := NewLedger(fixture.owner)
	secret := []byte("n8-ledger-reconcile-secret-long-enough")

	committedRequest := Request{
		ExecutionID: uuid.New(), Mode: ModeWrite, IdempotencyKey: "ledger-committed-000000000001",
		Statements: []Statement{{SQL: "insert into ext.items(id,amount,label) values($1,$2,$3)"}},
	}
	committedIdentity := ledgerIdentity(fixture.actorID, committedRequest, secret)
	if _, _, err := ledger.CreatePending(context.Background(), committedIdentity); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.MarkRunning(context.Background(), fixture.actorID, committedRequest.ExecutionID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.owner.Exec(context.Background(), `insert into pgbase_meta.sql_console_write_receipts
		(execution_id,administrator_id,idempotency_hash,request_hash,row_count,affected_rows,result_bytes,committed_at)
		values($1,$2,$3,$4,2,3,41,current_timestamp)`, committedRequest.ExecutionID, fixture.actorID,
		committedIdentity.IdempotencyHash[:], committedIdentity.RequestHash[:]); err != nil {
		t.Fatal(err)
	}

	abandonedRequest := Request{ExecutionID: uuid.New(), Mode: ModeRead, Statements: []Statement{{SQL: "select 1"}}}
	if _, _, err := ledger.CreatePending(context.Background(), ledgerIdentity(fixture.actorID, abandonedRequest, secret)); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	committed, err := ledger.Get(context.Background(), fixture.actorID, committedRequest.ExecutionID)
	if err != nil || committed.Status != StatusSucceeded || committed.Result == nil || committed.Result.Committed == nil || !*committed.Result.Committed {
		t.Fatalf("reconciled committed = %#v, %v", committed, err)
	}
	abandoned, err := ledger.Get(context.Background(), fixture.actorID, abandonedRequest.ExecutionID)
	if err != nil || abandoned.Status != StatusFailed || abandoned.Failure == nil || abandoned.Failure.Code != "abandoned" {
		t.Fatalf("reconciled abandoned = %#v, %v", abandoned, err)
	}
}

func ledgerIdentity(actorID uuid.UUID, request Request, secret []byte) LedgerIdentity {
	return LedgerIdentity{
		AdministratorID:  actorID,
		Request:          request,
		RequestHash:      RequestDigest(secret, request),
		QueryFingerprint: QueryFingerprint(secret, request.Statements),
		IdempotencyHash:  IdempotencyDigest(secret, request.IdempotencyKey),
	}
}
