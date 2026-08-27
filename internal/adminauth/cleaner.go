package adminauth

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
)

const (
	cleanerAdvisoryLockKey int64 = 0x7067626173650004
	cleanerBatchSize             = int64(1000)
	cleanerRetention             = 24 * time.Hour
	cleanerInterval              = time.Hour
)

// Cleaner periodically removes stale administrator rate-limit rows.
type Cleaner struct {
	pool  *database.Pool
	clock Clock
}

// NewCleaner constructs an administrator authentication state cleaner.
func NewCleaner(pool *database.Pool, clock Clock) *Cleaner {
	return &Cleaner{pool: pool, clock: normalizedClock(clock)}
}

// Run cleans immediately, then repeats hourly until the context is canceled.
func (c *Cleaner) Run(ctx context.Context) error {
	if c == nil || c.pool == nil {
		return fmt.Errorf("administrator authentication cleaner pool is nil")
	}
	if err := c.clean(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(cleanerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := c.clean(ctx); err != nil {
				return err
			}
		}
	}
}

func (c *Cleaner) clean(ctx context.Context) error {
	cutoff := c.clock.Now().Add(-cleanerRetention)
	for {
		var deleted int64
		err := c.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", cleanerAdvisoryLockKey); err != nil {
				return fmt.Errorf("lock administrator authentication cleaner: %w", err)
			}
			tag, err := tx.Exec(ctx, `
				with stale as (
					select ctid
					from pgbase_meta.admin_rate_limits
					where updated_at < $1
					order by updated_at, ctid
					limit 1000
					for update skip locked
				)
				delete from pgbase_meta.admin_rate_limits as limits
				using stale
				where limits.ctid = stale.ctid`, cutoff)
			if err != nil {
				return fmt.Errorf("delete stale administrator rate limits: %w", err)
			}
			deleted = tag.RowsAffected()
			return nil
		})
		if err != nil {
			return err
		}
		if deleted < cleanerBatchSize {
			return nil
		}
	}
}
