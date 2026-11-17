//go:build integration

package query

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/membership"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestRootPageRechecksResponseAfterHydration(t *testing.T) {
	_, pool, schemas, records := newR0QueryServiceTest(t)
	ctx := t.Context()
	collection := applyR0QueryCollection(t, schemas, schema.CreateCollectionInput{
		Name: "response_measure", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}},
	})
	if _, err := records.Create(ctx, collection.Name, map[string]json.RawMessage{"title": json.RawMessage(`"before hydration"`)}); err != nil {
		t.Fatal(err)
	}
	err := pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		catalog, err := schemas.EnsureCurrent(ctx, tx)
		if err != nil {
			return err
		}
		collection = catalog.Collections[collection.Name]
		plan, err := scalarRootPlan(collection, nil, nil)
		if err != nil {
			return err
		}
		plan.catalogVersion, plan.principal = catalog.Version, testReadPrincipal
		traversal, err := membership.ResolveTraversal(ctx, tx, collection.ID)
		if err != nil {
			return err
		}
		_, err = records.readRootPageInTx(ctx, tx, rootPageReadRequest{
			plan: plan, limit: 50, operationTime: traversal.TransactionTime, traversal: traversal,
			hydrate: func(items []map[string]any) error {
				// The preflight accepted the original map. Hydration can change
				// its wire representation, so cached preflight sizes are invalid.
				items[0]["hydrated"] = math.NaN()
				return nil
			},
		})
		return err
	})
	var unsupported *json.UnsupportedValueError
	if !errors.As(err, &unsupported) {
		t.Fatalf("changed hydration result bypassed serialization validation: %v", err)
	}
}
