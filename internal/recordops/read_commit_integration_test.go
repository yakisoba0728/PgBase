//go:build integration

package recordops

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// Only a read transaction owned by the service may skip the WAL flush on
// COMMIT. Mutations and reads inside a caller's transaction stay durable.
func TestReadTxRelaxesCommitOnlyWhenOwned(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := recordOperationsTestContext(t)
	definition := schema.CreateCollectionInput{Name: "read_commit", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}}}
	plan, err := schemas.Preview(ctx, schema.NewCreateChange(definition))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(definition), plan, principal.ID)); err != nil {
		t.Fatal(err)
	}
	setting := func(tx pgx.Tx, _ schema.Catalog, _ schema.Collection) (value string, err error) {
		err = tx.QueryRow(ctx, "show synchronous_commit").Scan(&value)
		return value, err
	}
	check := func(name, want string, run func(func(pgx.Tx, schema.Catalog, schema.Collection) error) error) {
		t.Helper()
		var got string
		if err := run(func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) (err error) {
			got, err = setting(tx, catalog, collection)
			return err
		}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != want {
			t.Fatalf("%s: synchronous_commit=%q, want %q", name, got, want)
		}
	}
	check("owned read", "off", func(op func(pgx.Tx, schema.Catalog, schema.Collection) error) error {
		return records.readTx(ctx, definition.Name, op)
	})
	check("owned update", "on", func(op func(pgx.Tx, schema.Catalog, schema.Collection) error) error {
		return records.updateTx(ctx, definition.Name, op)
	})
	check("borrowed read", "on", func(op func(pgx.Tx, schema.Catalog, schema.Collection) error) error {
		return pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
			return records.readTx(WithTransaction(ctx, tx), definition.Name, op)
		})
	})
	// The relaxed setting must not leak to the next user of the pooled session.
	var after string
	if err := pool.QueryRow(ctx, "show synchronous_commit").Scan(&after); err != nil || after != "on" {
		t.Fatalf("after read: synchronous_commit=%q err=%v", after, err)
	}
}
