package adminauth

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
)

const (
	cleanerAdvisoryLockKey int64 = 0x7067626173650004
	cleanerBatchSize             = int64(1000)
	cleanerRetention             = 24 * time.Hour
	cleanerInterval              = time.Hour
	cleanerRetryMin              = 100 * time.Millisecond
	cleanerRetryMax              = time.Minute
)

type Cleaner struct {
	pool     *database.Pool
	clock    Clock
	cleanFn  func(context.Context) error
	retryMin time.Duration
	retryMax time.Duration
	interval time.Duration
	logger   *slog.Logger
}

func NewCleaner(pool *database.Pool, clock Clock, loggers ...*slog.Logger) *Cleaner {
	cleaner := &Cleaner{
		pool: pool, clock: normalizedClock(clock),
		retryMin: cleanerRetryMin, retryMax: cleanerRetryMax, interval: cleanerInterval,
	}
	if len(loggers) > 0 {
		cleaner.logger = loggers[0]
	}
	return cleaner
}

// Run cleans immediately, then repeats hourly until the context is canceled.
func (c *Cleaner) Run(ctx context.Context) error {
	if c == nil || (c.pool == nil && c.cleanFn == nil) {
		return fmt.Errorf("administrator authentication cleaner pool is nil")
	}
	retryMin, retryMax, interval := c.timings()
	delay := time.Duration(0)
	retry := retryMin
	for {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return ctx.Err()
			case <-timer.C:
			}
		}
		if err := c.cleanOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			delay = retry
			if c.logger != nil {
				c.logger.WarnContext(ctx, "administrator authentication cleanup failed", "error", err, "retry_in", delay)
			}
			retry *= 2
			if retry > retryMax {
				retry = retryMax
			}
			continue
		}
		delay = interval
		retry = retryMin
	}
}

func (c *Cleaner) cleanOnce(ctx context.Context) error {
	if c.cleanFn != nil {
		return c.cleanFn(ctx)
	}
	return c.cleanDatabase(ctx)
}

func (c *Cleaner) cleanDatabase(ctx context.Context) error {
	cutoff := c.clock.Now().Add(-cleanerRetention)
	if err := c.deleteRateLimits(ctx, cutoff); err != nil {
		return err
	}
	return c.deleteSessions(ctx, cutoff)
}

func (c *Cleaner) deleteRateLimits(ctx context.Context, cutoff time.Time) error {
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

func (c *Cleaner) deleteSessions(ctx context.Context, cutoff time.Time) error {
	for {
		var deleted int64
		err := c.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", cleanerAdvisoryLockKey); err != nil {
				return fmt.Errorf("lock administrator authentication cleaner: %w", err)
			}
			tag, err := tx.Exec(ctx, `
				with stale as (
					select ctid
					from pgbase_meta.admin_sessions
					where expires_at < $1 or (revoked_at is not null and revoked_at < $1)
					order by expires_at, ctid
					limit 1000
					for update skip locked
				)
				delete from pgbase_meta.admin_sessions as sessions
				using stale
				where sessions.ctid = stale.ctid`, cutoff)
			if err != nil {
				return fmt.Errorf("delete stale administrator sessions: %w", err)
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

func (c *Cleaner) timings() (time.Duration, time.Duration, time.Duration) {
	retryMin, retryMax, interval := c.retryMin, c.retryMax, c.interval
	if retryMin <= 0 {
		retryMin = cleanerRetryMin
	}
	if retryMax < retryMin {
		retryMax = retryMin
	}
	if interval <= 0 {
		interval = cleanerInterval
	}
	return retryMin, retryMax, interval
}
