//go:build integration

package query

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestExpandedReadValidatesPlanBeforeRootHydrationBudget(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	createExpandGraph(t, ctx, pool, schemas, records, 1)
	page, err := records.listScalar(ctx, "expand_posts", "", 1, "", "")
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

}
