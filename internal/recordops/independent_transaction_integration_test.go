//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type independentValidator func(context.Context, pgx.Tx, BatchRequest, []MutationResult) error

func (f independentValidator) ValidateRecords(ctx context.Context, tx pgx.Tx, req BatchRequest, results []MutationResult) error {
	return f(ctx, tx, req, results)
}

func TestIndependentBorrowedSingleValidationAndProjection(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	definition := schema.CreateCollectionInput{Name: "borrowed_text", RecordID: &schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 64, Pattern: `^[A-Za-z0-9-]+$`}, Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}}}
	plan, err := schemas.Preview(ctx, schema.NewCreateChange(definition))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(definition), plan, principal.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := records.members.writer(ctx); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"create", "update"} {
		t.Run(operation, func(t *testing.T) {
			if operation == "update" {
				if _, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "independent-seed", Collection: definition.Name, Input: map[string]json.RawMessage{"id": json.RawMessage(`"update"`), "title": json.RawMessage(`"before"`)}}); err != nil {
					t.Fatal(err)
				}
			}
			for _, failure := range []string{"none", "validation", "projection"} {
				t.Run(failure, func(t *testing.T) {
					calls := 0
					veto := errors.New("validator veto")
					records.SetTransactionValidators([]TransactionValidator{independentValidator(func(ctx context.Context, tx pgx.Tx, _ BatchRequest, results []MutationResult) error {
						calls++
						if failure == "validation" {
							return veto
						}
						if failure == "projection" {
							_, err := tx.Exec(ctx, "delete from pgbase_meta.record_identities where internal_id=$1", results[0].InternalID)
							return err
						}
						return nil
					})})
					tx, err := pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback(ctx)
					outcome := NewTransactionOutcome()
					scope := WithTransactionOutcome(ctx, tx, outcome)
					var result MutationResult
					if operation == "create" {
						result, err = records.Create(scope, CreateRequest{Principal: principal, RequestID: "borrowed-create", Collection: definition.Name, Input: map[string]json.RawMessage{"id": json.RawMessage(fmt.Sprintf("%q", failure)), "title": json.RawMessage(`"after"`)}})
					} else {
						result, err = records.Update(scope, UpdateRequest{Principal: principal, RequestID: "borrowed-update", Collection: definition.Name, PublicID: "update", Input: map[string]json.RawMessage{"title": json.RawMessage(`"after"`)}})
					}
					if calls != 1 {
						t.Fatalf("validator calls=%d, want 1", calls)
					}
					if failure == "none" {
						want := failure
						if operation == "update" {
							want = "update"
						}
						if err != nil || result.Record.Data["id"] != want {
							t.Fatalf("public result=%#v, err=%v", result, err)
						}
					} else {
						if err == nil {
							t.Fatal("failure accepted")
						}
						if failure == "validation" && !errors.Is(err, veto) {
							t.Fatal(err)
						}
						var count int
						if err := tx.QueryRow(ctx, "select count(*) from app.borrowed_text where title='after'").Scan(&count); err != nil || count != 0 {
							t.Fatalf("failed operation retained writes: count=%d, err=%v", count, err)
						}
					}
					// Commit the owner even after an operation fails: failed writes must already be gone.
					if err := tx.Commit(ctx); err != nil {
						t.Fatal(err)
					}
					outcome.Finish(ctx, nil)
					if _, err := pool.Exec(ctx, "update app.borrowed_text set title='before'"); err != nil {
						t.Fatal(err)
					}
				})
			}
			records.SetTransactionValidators(nil)
			if _, err := pool.Exec(ctx, "delete from app.borrowed_text"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestIndependentBatchReplayKeepsInternalIdentityUntilValidation(t *testing.T) {
	_, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	definition := schema.CreateCollectionInput{Name: "replay_text", RecordID: &schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 64, Pattern: `^[A-Za-z0-9-]+$`}, Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}}}
	plan, err := schemas.Preview(ctx, schema.NewCreateChange(definition))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(definition), plan, principal.ID)); err != nil {
		t.Fatal(err)
	}
	for _, publicID := range []string{"TextAlias", "01900000-0000-7000-8000-000000000001"} {
		t.Run(publicID, func(t *testing.T) {
			defer records.SetTransactionValidators(nil)
			req := CreateRequest{Principal: principal, RequestID: "independent-seed", Collection: definition.Name, IdempotencyKey: "independent-replay-" + publicID, Input: map[string]json.RawMessage{"id": json.RawMessage(fmt.Sprintf("%q", publicID)), "title": json.RawMessage(`"seed"`)}}
			seed, err := records.Create(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			records.SetTransactionValidators([]TransactionValidator{independentValidator(func(_ context.Context, _ pgx.Tx, _ BatchRequest, results []MutationResult) error {
				if results[0].Record.Data["id"] != seed.InternalID.String() {
					return fmt.Errorf("validator received public ID %v", results[0].Record.Data["id"])
				}
				return nil
			})})
			results, err := records.Batch(ctx, BatchRequest{Principal: principal, RequestID: "batch-replay", Operations: []BatchOperation{{Create: &req}}})
			if err != nil || len(results) != 1 || !results[0].Replayed || results[0].Record.Data["id"] != publicID {
				t.Fatalf("batch replay=%#v, err=%v", results, err)
			}
			records.SetTransactionValidators(nil)
		})
	}
}

func TestIndependentBorrowedSingleHidesFieldsForUsers(t *testing.T) {
	f := newUserAccessFixture(t)
	ctx := t.Context()
	definition := schema.CreateCollectionInput{Name: "borrowed_hidden", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}, {Name: "secret", Type: schema.TypeText, Hidden: true, Default: json.RawMessage(`"private"`)}}}
	plan, err := f.schemas.Preview(ctx, schema.NewCreateChange(definition))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(definition), plan, f.admin.ID)); err != nil {
		t.Fatal(err)
	}
	f.enable(t, definition.Name)
	if _, err := f.records.members.writer(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	scope := WithTransactionOutcome(ctx, tx, NewTransactionOutcome())
	request := CreateRequest{Principal: f.first, RequestID: "borrowed-hidden-create", Collection: definition.Name, IdempotencyKey: "borrowed-hidden-replay-key", Input: map[string]json.RawMessage{"title": json.RawMessage(`"first"`)}}
	created, err := f.records.Create(scope, request)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := f.records.Create(scope, request)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := f.records.Update(scope, UpdateRequest{Principal: f.first, RequestID: "borrowed-hidden-update", Collection: definition.Name, ID: created.InternalID, Input: map[string]json.RawMessage{"title": json.RawMessage(`"after"`)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []MutationResult{created, replayed, updated} {
		if _, ok := result.Record.Data["secret"]; ok {
			t.Fatalf("hidden field exposed: %#v", result.Record.Data)
		}
	}
}
