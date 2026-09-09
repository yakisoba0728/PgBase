package query

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/cursor"
	"github.com/webisnoteasy/PgBase/internal/membership"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

// SQL views have identities computed by their query, not record creation
// ordinals. Their signed cursors bind schema, filter, sort and access policy but
// page across current snapshots. Newly matching rows may appear between pages.
func (s *Service) resolveReadTraversal(ctx context.Context, tx pgx.Tx, collection schema.Collection) (membership.Traversal, error) {
	if !collection.IsSQLView() {
		return s.resolveTraversal(ctx, tx, collection.AccessCollectionID())
	}
	var traversal membership.Traversal
	err := tx.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&traversal.TransactionTime)
	traversal.TransactionTime = traversal.TransactionTime.UTC()
	return traversal, err
}

// Validate the UUID key within every read snapshot: changing source data can
// invalidate a formerly unique view definition. Fail rather than returning an
// arbitrary row or a cursor which silently skips duplicate identities.
func validateSQLViewIdentity(ctx context.Context, tx pgx.Tx, collection schema.Collection) error {
	if !collection.IsSQLView() {
		return nil
	}
	table := pgx.Identifier{"app", collection.TableName}.Sanitize()
	var invalid bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+table+` GROUP BY id HAVING id IS NULL OR id='00000000-0000-0000-0000-000000000000'::uuid OR count(*)>1)`).Scan(&invalid)
	if err != nil {
		return err
	}
	if invalid {
		return fmt.Errorf("%w: SQL view identity is null or duplicated", schema.ErrConflict)
	}
	return nil
}
func sqlViewDescriptor(plan rootPlan, descriptor *cursor.V5Descriptor) bool {
	return plan.order().root.IsSQLView() && descriptor != nil && descriptor.MembershipGeneration == 1 && descriptor.HighWater == 0
}
