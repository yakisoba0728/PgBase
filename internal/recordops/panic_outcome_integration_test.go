//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestPanickingMutationReportsRollbackInsteadOfSuccess(t *testing.T) {
	for _, ownership := range []string{"owned", "borrowed"} {
		for _, operation := range []string{"create", "update", "batch"} {
			t.Run(ownership+"/"+operation, func(t *testing.T) {
				pool, _, records, principal := newRecordOperationsTest(t)
				ctx := t.Context()
				// Register the writer before borrowing the pinned transaction.
				seed, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "panic-seed", Collection: "records", Input: batchInput("seed")})
				if err != nil {
					t.Fatal(err)
				}
				hook := &countingHook{}
				records.SetHooks([]MutationHook{hook})
				cause := &struct{ message string }{"validator failure"}
				records.SetTransactionValidators([]TransactionValidator{independentValidator(func(context.Context, pgx.Tx, BatchRequest, []MutationResult) error {
					panic(cause)
				})})
				outcome := NewTransactionOutcome()
				var owner pgx.Tx
				if ownership == "borrowed" {
					owner, err = pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer owner.Rollback(context.Background())
					ctx = WithTransactionOutcome(ctx, owner, outcome)
				}
				func() {
					defer func() {
						if got := recover(); got != cause {
							t.Fatalf("panic = %v, want original cause %v", got, cause)
						}
					}()
					switch operation {
					case "create":
						_, err = records.Create(ctx, CreateRequest{Principal: principal, RequestID: "panic-create", Collection: "records", Input: batchInput("should-rollback")})
					case "update":
						_, err = records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "panic-update", Collection: "records", ID: seed.InternalID, Input: map[string]json.RawMessage{"title": json.RawMessage(`"should-rollback"`)}})
					case "batch":
						_, err = records.Batch(ctx, BatchRequest{Principal: principal, RequestID: "panic-batch", Operations: []BatchOperation{{Create: &CreateRequest{Collection: "records", Input: batchInput("should-rollback")}}}})
					}
					t.Fatalf("operation returned instead of preserving panic: %v", err)
				}()
				if owner != nil {
					if hook.after != 0 || hook.afterError != 0 {
						t.Fatalf("callbacks ran before outer outcome: %+v", hook)
					}
					// The failed savepoint is already rolled back. An owner may
					// recover and commit other work; that cannot turn this into success.
					if err := owner.Commit(ctx); err != nil {
						t.Fatal(err)
					}
					outcome.Finish(ctx, nil)
					outcome.Finish(ctx, nil)
				}
				var count int
				if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM app.records WHERE title='should-rollback'`).Scan(&count); err != nil || count != 0 {
					t.Fatalf("panic retained writes: count=%d error=%v", count, err)
				}
				for _, table := range []string{"app.records", "pgbase_meta.record_revisions", "pgbase_meta.record_audit_events", "pgbase_meta.record_outbox"} {
					if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 1 {
						t.Fatalf("panic altered committed artifacts: %s count=%d error=%v", table, count, err)
					}
				}
				if hook.before != 1 || hook.after != 0 || hook.afterError != 1 {
					t.Fatalf("rolled-back operation hook outcome: %+v", hook)
				}
			})
		}
	}
}
