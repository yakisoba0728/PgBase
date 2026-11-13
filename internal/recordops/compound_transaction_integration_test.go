//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/query"
)

func TestBorrowedBatchAndTruncateFollowOwnerOutcome(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	if _, err := records.members.writer(ctx); err != nil {
		t.Fatal(err)
	}
	hook := &countingHook{}
	records.SetHooks([]MutationHook{hook})
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	outcome := NewTransactionOutcome()
	scope := WithTransactionOutcome(ctx, tx, outcome)
	result, err := records.Batch(scope, BatchRequest{Principal: principal, RequestID: "borrowed-batch", Operations: []BatchOperation{
		{Create: &CreateRequest{Collection: "records", Input: map[string]json.RawMessage{"title": json.RawMessage(`"first"`)}}},
		{Create: &CreateRequest{Collection: "records", Input: map[string]json.RawMessage{"title": json.RawMessage(`"second"`)}}},
	}})
	if err != nil || len(result) != 2 {
		t.Fatalf("borrowed batch = %#v, %v", result, err)
	}
	var count int
	if err := pool.QueryRow(ctx, "select count(*) from app.records").Scan(&count); err != nil || count != 0 {
		t.Fatalf("batch committed independently: %d, %v", count, err)
	}
	truncated, err := records.Truncate(scope, TruncateRequest{Principal: principal, RequestID: "borrowed-truncate", Collection: "records"})
	if err != nil || truncated.RecordsDeleted != 2 {
		t.Fatalf("borrowed truncate = %#v, %v", truncated, err)
	}
	if hook.after != 0 || hook.afterError != 0 {
		t.Fatalf("hooks ran before owner completion: %+v", hook)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	outcome.Finish(ctx, nil)
	outcome.Finish(ctx, nil)
	if hook.after != 4 || hook.afterError != 0 {
		t.Fatalf("commit hooks = %+v", hook)
	}
}

func TestFailedBorrowedBatchRollsBackSavepointAndReportsFailure(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	if _, err := records.members.writer(ctx); err != nil {
		t.Fatal(err)
	}
	hook := &countingHook{}
	records.SetHooks([]MutationHook{hook})
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	outcome := NewTransactionOutcome()
	scope := WithTransactionOutcome(ctx, tx, outcome)
	_, err = records.Batch(scope, BatchRequest{Principal: principal, RequestID: "failed-borrowed-batch", Operations: []BatchOperation{
		{Create: &CreateRequest{Collection: "records", Input: map[string]json.RawMessage{"title": json.RawMessage(`"duplicate"`)}}},
		{Create: &CreateRequest{Collection: "records", Input: map[string]json.RawMessage{"title": json.RawMessage(`"duplicate"`)}}},
	}})
	if err == nil {
		t.Fatal("duplicate batch accepted")
	}
	var count int
	if err := tx.QueryRow(ctx, "select count(*) from app.records").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed savepoint retained writes or aborted parent: %d, %v", count, err)
	}
	if hook.after != 0 || hook.afterError != 0 {
		t.Fatalf("hooks ran before owner completion: %+v", hook)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	outcome.Finish(ctx, nil)
	if hook.after != 0 || hook.afterError == 0 {
		t.Fatalf("failed savepoint reported success: %+v", hook)
	}
}

func TestBorrowedMutationRequiresHookOutcomeAndFreshDeleteSnapshot(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	if _, err := records.members.writer(ctx); err != nil {
		t.Fatal(err)
	}
	hook := &countingHook{}
	records.SetHooks([]MutationHook{hook})
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = records.Create(WithTransaction(ctx, tx), CreateRequest{Principal: principal, RequestID: "unmanaged-hook-write", Collection: "records", Input: map[string]json.RawMessage{"title": json.RawMessage(`"not committed"`)}})
	if !errors.Is(err, ErrTransactionOutcomeRequired) || hook.before != 0 {
		t.Fatalf("unmanaged hooks admitted: %v, %+v", err, hook)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		scope := WithTransactionOutcome(ctx, tx, NewTransactionOutcome())
		_, err := records.Truncate(scope, TruncateRequest{Principal: principal, RequestID: "stale-truncate", Collection: "records"})
		if !errors.Is(err, database.ErrTransactionIsolation) {
			t.Fatalf("stale deletion snapshot admitted: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBorrowedTransactionDoesNotRegisterLazyWriterBehindItsOwnSchemaLock(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := schemas.EnsureCurrent(ctx, tx); err != nil {
		t.Fatal(err)
	}
	_, err = records.Create(WithTransaction(ctx, tx), CreateRequest{Principal: principal, RequestID: "unprepared-external-writer", Collection: "records", Input: map[string]json.RawMessage{"title": json.RawMessage(`"not inserted"`)}})
	if !IsCode(err, CodeUnavailable) || ctx.Err() != nil {
		t.Fatalf("lazy registration waited behind owner's schema pin: %v, context=%v", err, ctx.Err())
	}
}

func TestTruncateRootBudgetRejectsBeforeAnyMutation(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `insert into app.records(id,title,created_at,updated_at)
		select md5(value::text)::uuid, 'truncate-budget-' || value::text, current_timestamp, current_timestamp
		from generate_series(1,$1::integer) as input(value)`, maximumDeleteHydrationRows+1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = records.Truncate(ctx, TruncateRequest{Principal: principal, RequestID: "truncate-over-root-budget", Collection: "records"})
	var invalid *query.ValidationError
	if !errors.As(err, &invalid) || len(invalid.Details) != 1 || invalid.Details[0].Code != "truncate_budget_exceeded" {
		t.Fatalf("unbounded truncate accepted: %v", err)
	}
	var roots, outbox int64
	if err := pool.QueryRow(ctx, "select (select count(*) from app.records), (select count(*) from pgbase_meta.record_outbox)").Scan(&roots, &outbox); err != nil || roots != maximumDeleteHydrationRows+1 || outbox != 0 {
		t.Fatalf("budget failure mutated records/outbox: %d/%d, %v", roots, outbox, err)
	}
}
