package authpolicy

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
)

const cleanupBatchSize = 1000

// DeleteExpiredRows sweeps a timestamp column of an authentication sidecar.
// Each statement commits a bounded batch and leaves currently locked rows for
// a later sweep.
func DeleteExpiredRows(ctx context.Context, pool *database.Pool, table, column string, retentionSeconds int64) error {
	qualified := pgx.Identifier{"pgbase_meta", table}.Sanitize()
	timestamp := pgx.Identifier{column}.Sanitize()
	statement := fmt.Sprintf(`
		with stale as (
			select ctid from %s
			where %s <= statement_timestamp() - make_interval(secs=>$1)
			order by %s
			limit %d
			for update skip locked
		)
		delete from %s as entries using stale where entries.ctid=stale.ctid`,
		qualified, timestamp, timestamp, cleanupBatchSize, qualified)
	for {
		tag, err := pool.Exec(ctx, statement, retentionSeconds)
		if err != nil {
			return fmt.Errorf("clean %s by %s: %w", table, column, err)
		}
		if tag.RowsAffected() < cleanupBatchSize {
			return nil
		}
	}
}
