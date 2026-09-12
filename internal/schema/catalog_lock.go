package schema

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// LockCatalogGenerationShared prevents supported schema changes from crossing
// the caller's catalog validation and execution gate. Direct owner DDL remains
// outside PgBase's mutation contract.
func LockCatalogGenerationShared(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `select pg_advisory_xact_lock_shared($1)`, schemaAdvisoryLockKey)
	return err
}
