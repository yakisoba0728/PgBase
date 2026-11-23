package doctor

import (
	"context"
	"fmt"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/observability"
)

const requestLogCleanupBehindCode = "request_log_cleanup_behind"

// requestLogBacklogFactor is how far past its row cap the request log may
// reach before the backlog is reported. Every successful batch cleanup brings
// the id span back under the cap, so twice the cap means cleanup has not
// succeeded for at least a cap's worth of requests.
const requestLogBacklogFactor = 2

// RequestLogWarnings reports a request log that has outgrown its row cap. The
// server warns once a cleanup fails, but only in its own log; this makes a
// cleanup that keeps failing visible from the database alone.
//
// The age limit lives in encrypted runtime settings, so the check uses the
// row cap the server also enforces on every batch. It reads the id span
// rather than counting rows: the cap is enforced on ids, and min/max are
// answered from the primary key without scanning the table. maximumRows is the
// configured cap; zero or less means the default.
func RequestLogWarnings(ctx context.Context, db database.DBTX, maximumRows int) ([]Finding, error) {
	if maximumRows <= 0 {
		maximumRows = observability.DefaultRequestLogMaximumRows
	}
	var exists bool
	if err := db.QueryRow(ctx, `select pg_catalog.to_regclass('pgbase_meta.request_logs') is not null`).Scan(&exists); err != nil {
		return nil, fmt.Errorf("inspect request log table: %w", err)
	}
	if !exists {
		return []Finding{}, nil
	}
	var span int64
	var oldest *time.Time
	if err := db.QueryRow(ctx, `select coalesce(max(id)-min(id)+1, 0), min(occurred_at) from pgbase_meta.request_logs`).Scan(&span, &oldest); err != nil {
		return nil, fmt.Errorf("inspect request log size: %w", err)
	}
	if span <= int64(maximumRows)*requestLogBacklogFactor {
		return []Finding{}, nil
	}
	since := ""
	if oldest != nil {
		since = fmt.Sprintf(" back to %s", oldest.UTC().Format(time.RFC3339))
	}
	return []Finding{warning(requestLogCleanupBehindCode, fmt.Sprintf(
		"The request log spans %d entries%s, more than %d times its row cap of %d. Its cleanup has been failing, "+
			"so the table grows past its retention. Look for \"request log cleanup failed\" in the server log; "+
			"a running server removes the backlog in bounded steps once cleanup succeeds (see docs/deployment.md)",
		span, since, requestLogBacklogFactor, maximumRows))}, nil
}
