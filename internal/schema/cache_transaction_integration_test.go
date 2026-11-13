//go:build integration

package schema

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestCallerCatalogCandidatesStayLocalUntilCommittedRead(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	ctx := context.Background()
	collection := applyFreshPlan(t, service, CreateCollectionInput{
		Name: "catalog_transaction", Fields: []FieldInput{{Name: "title", Type: TypeText}},
	}, actor)
	before, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{"rollback", "commit failure"} {
		t.Run(failure, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, `UPDATE pgbase_meta.fields SET help='uncommitted' WHERE collection_id=$1`, collection.ID); err != nil {
				t.Fatal(err)
			}
			version, err := NewStore(tx).IncrementVersion(ctx)
			if err != nil {
				t.Fatal(err)
			}
			local, err := service.EnsureCurrent(ctx, tx)
			if err != nil || local.Version != version || local.Collections[collection.Name].Fields[0].Help != "uncommitted" {
				t.Fatalf("transaction-local catalog = %#v, %v", local, err)
			}
			if service.cache.Load().Version != before.Version || service.cache.find(version) != nil {
				t.Fatal("caller catalog escaped into shared cache before commit")
			}
			var visible string
			if err := pool.QueryRow(ctx, `SELECT help FROM pgbase_meta.fields WHERE collection_id=$1`, collection.ID).Scan(&visible); err != nil || visible != "" {
				t.Fatalf("another reader saw uncommitted catalog = %q, %v", visible, err)
			}
			if failure == "commit failure" {
				if _, err := tx.Exec(ctx, "SELECT 1/0"); err == nil {
					t.Fatal("failed to abort caller transaction")
				}
				if err := tx.Commit(ctx); err == nil {
					t.Fatal("aborted transaction committed")
				}
			} else if err := tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			fresh, err := service.Catalog(ctx)
			if err != nil || fresh.Version != before.Version || fresh.Collections[collection.Name].Fields[0].Help != "" {
				t.Fatalf("failed transaction contaminated catalog = %#v, %v", fresh, err)
			}
		})
	}
	// Reuse the rolled-back version with different metadata. The old candidate
	// must never be selected by the subsequent committed reader.
	var committedVersion int64
	err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE pgbase_meta.fields SET help='committed' WHERE collection_id=$1`, collection.ID); err != nil {
			return err
		}
		var err error
		committedVersion, err = NewStore(tx).IncrementVersion(ctx)
		if err != nil {
			return err
		}
		_, err = service.EnsureCurrent(ctx, tx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if service.cache.Load().Version != before.Version {
		t.Fatal("caller commit unexpectedly published transaction-local candidate")
	}
	fresh, err := service.Catalog(ctx)
	if err != nil || fresh.Version != committedVersion || fresh.Collections[collection.Name].Fields[0].Help != "committed" {
		t.Fatalf("committed reader reused failed candidate = %#v, %v", fresh, err)
	}
	if service.cache.Load().Version != committedVersion {
		t.Fatal("transaction-owning reader did not publish committed catalog")
	}
}
