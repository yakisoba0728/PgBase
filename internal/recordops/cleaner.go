package recordops

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/webisnoteasy/PgBase/internal/database"
)

const (
	defaultCleanerInterval = 5 * time.Minute
	defaultCleanerBatch    = 256
	defaultCleanerYield    = 10 * time.Millisecond
)

// IdempotencyCleaner bounds completed replay state without participating in
// request readiness. Every pass is small and SKIP LOCKED, so any number of
// pgBase instances can run one concurrently without electing a leader.
type IdempotencyCleaner struct {
	pool       *database.Pool
	logger     *slog.Logger
	interval   time.Duration
	batchSize  int
	batchYield time.Duration
}

func NewIdempotencyCleaner(pool *database.Pool, loggers ...*slog.Logger) *IdempotencyCleaner {
	var logger *slog.Logger
	if len(loggers) > 0 {
		logger = loggers[0]
	}
	return &IdempotencyCleaner{
		pool: pool, logger: logger, interval: defaultCleanerInterval,
		batchSize: defaultCleanerBatch, batchYield: defaultCleanerYield,
	}
}

// Run cleans immediately and then on a fixed cadence. A failed pass is logged
// and retried on the next tick; it never changes application readiness.
func (c *IdempotencyCleaner) Run(ctx context.Context) error {
	if c == nil || c.pool == nil || c.interval <= 0 || c.batchSize <= 0 || c.batchYield < 0 {
		return errors.New("record idempotency cleaner is unavailable")
	}
	c.drainWithoutAffectingReadiness(ctx)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			c.drainWithoutAffectingReadiness(ctx)
		}
	}
}

func (c *IdempotencyCleaner) drainWithoutAffectingReadiness(ctx context.Context) {
	for {
		deleted, err := c.Clean(ctx)
		if err != nil {
			if ctx.Err() == nil && c.logger != nil {
				c.logger.WarnContext(ctx, "record idempotency cleanup failed")
			}
			return
		}
		if deleted < int64(c.batchSize) {
			return
		}
		timer := time.NewTimer(c.batchYield)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		}
	}
}

// Clean performs one bounded cleanup pass and is exposed for operational tests
// and one-shot maintenance commands.
func (c *IdempotencyCleaner) Clean(ctx context.Context) (int64, error) {
	if c == nil || c.pool == nil || c.batchSize <= 0 {
		return 0, errors.New("record idempotency cleaner is unavailable")
	}
	tag, err := c.pool.Exec(ctx, `WITH expired AS MATERIALIZED (
		SELECT ctid
		FROM pgbase_meta.record_idempotency
		WHERE expires_at <= statement_timestamp()
		ORDER BY expires_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	)
	DELETE FROM pgbase_meta.record_idempotency AS result
	USING expired
	WHERE result.ctid = expired.ctid`, c.batchSize)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
