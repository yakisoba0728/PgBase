//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestReadsShareTheWriteTransactionAndRespectRollback(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	definition := schema.CreateCollectionInput{
		Name:     "transaction_text_records",
		RecordID: &schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 32, Pattern: `^[A-Za-z0-9]+$`},
		Fields:   []schema.FieldInput{{Name: "title", Type: schema.TypeText, Required: true}},
	}
	plan, err := schemas.Preview(ctx, schema.NewCreateChange(definition))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(definition), plan, principal.ID)); err != nil {
		t.Fatal(err)
	}
	source := schema.CreateCollectionInput{Name: "transaction_relation_source", Fields: []schema.FieldInput{
		{Name: "target", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: definition.Name, Cardinality: schema.RelationSingle}},
	}}
	plan, err = schemas.Preview(ctx, schema.NewCreateChange(source))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(source), plan, principal.ID)); err != nil {
		t.Fatal(err)
	}
	// Register the writer before the transaction pins the schema.
	if _, err := records.members.writer(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}

	defer tx.Rollback(ctx)
	txctx := WithTransaction(ctx, tx)
	created, err := records.Create(txctx, CreateRequest{
		Principal: principal, RequestID: "transaction-create", Collection: definition.Name,
		Input: map[string]json.RawMessage{"id": json.RawMessage(`"Uncommitted"`), "title": json.RawMessage(`"visible inside"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := records.Validate(txctx, ValidateRequest{Principal: principal, RequestID: "transaction-validate", Collection: source.Name,
		Input: map[string]json.RawMessage{"target": json.RawMessage(`"Uncommitted"`)}}); err != nil {
		t.Fatalf("validation did not see uncommitted relation identity: %v", err)
	}
	get := GetRequest{Principal: principal, RequestID: "transaction-get", Collection: definition.Name, PublicID: "Uncommitted"}
	got, err := records.Get(txctx, get)
	if err != nil || got.Data["id"] != "Uncommitted" || got.ETag != created.Record.ETag {
		t.Fatalf("get own write = %#v, %v", got, err)
	}
	list := ListRequest{Principal: principal, RequestID: "transaction-list", Collection: definition.Name, Total: true}
	page, err := records.List(txctx, list)
	if err != nil || len(page.Items) != 1 || page.Items[0]["id"] != "Uncommitted" || page.TotalItems == nil || *page.TotalItems != 1 {
		t.Fatalf("list own write = %#v, %v", page, err)
	}
	if count, err := records.Count(txctx, list); err != nil || count != 1 {
		t.Fatalf("count own write = %d, %v", count, err)
	}
	compat, err := records.ListPocketBase(txctx, PocketBaseListRequest{ListRequest: list, Page: 1, PerPage: 10})
	if err != nil || compat.TotalItems != 1 || len(compat.Items) != 1 || compat.Items[0]["id"] != "Uncommitted" {
		t.Fatalf("adapter own write = %#v, %v", compat, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := records.Get(ctx, get); !errors.Is(err, query.ErrRecordNotFound) {
		t.Fatalf("rolled back record visible: %v", err)
	}
	if count, err := records.Count(ctx, list); err != nil || count != 0 {
		t.Fatalf("count after rollback = %d, %v", count, err)
	}
	if _, err := tx.Exec(ctx, "select 1"); !errors.Is(err, pgx.ErrTxClosed) {
		t.Fatalf("transaction not closed: %v", err)
	}
}
