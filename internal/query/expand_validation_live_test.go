//go:build integration

package query

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func TestExpandedReadValidatesPlanBeforeRootHydrationBudget(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	createExpandGraph(t, ctx, pool, schemas, records, 1)
	page, err := records.List(ctx, "expand_posts", "", 1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	records.expandLimits.maxRows = 0
	principal := Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID}
	recordID := uuid.MustParse(page.Items[0]["id"].(string))

	t.Run("get", func(t *testing.T) {
		_, err := records.GetExpanded(ctx, GetRequest{
			Principal: principal, Collection: "expand_posts", ID: recordID, Expand: "missing_relation",
		})
		assertValidationDetail(t, err, "expand", "expand_invalid_path")
	})

	t.Run("list", func(t *testing.T) {
		_, err := records.ListExpanded(ctx, ListRequest{
			Principal: principal, Collection: "expand_posts", Limit: 1, Expand: "missing_relation",
		})
		assertValidationDetail(t, err, "expand", "expand_invalid_path")
	})

	t.Run("same transaction executor", func(t *testing.T) {
		err := records.inTxCatalog(ctx, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
			statement := CompileSelectByID(collection, recordID)
			base, err := EncodeRecord(collection, tx.QueryRow(ctx, statement.SQL, statement.Args...))
			if err != nil {
				return err
			}
			_, err = records.ExpandBaseRecords(ctx, tx, catalog, collection, []map[string]any{base}, ExpandRequest{
				Principal: principal, Expand: "missing_relation",
			})
			return err
		}, "expand_posts")
		var validationError *ValidationError
		if !errors.As(err, &validationError) {
			t.Fatalf("ExpandBaseRecords error = %v, want validation", err)
		}
		assertValidationDetail(t, err, "expand", "expand_invalid_path")
	})
}
