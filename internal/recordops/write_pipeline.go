package recordops

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// writePipeline sends consecutive kernel writes that consume no earlier
// statement's result as one pgx batch: the same statements, in the same order,
// in the same transaction, in one network round trip.
//
// PostgreSQL executes the batch in order and, after the first failing
// statement, skips the rest until the batch's Sync. flush reads results in
// queue order and returns the first database error or affected-row mismatch,
// exactly the error the former one-Exec-per-statement code returned first.
// Statements behind a mismatched one may already have executed; the caller
// returns that error, so the transaction or savepoint is rolled back as before.
type writePipeline struct {
	batch  pgx.Batch
	checks []func(pgconn.CommandTag) error
}

func (p *writePipeline) queue(sql string, check func(pgconn.CommandTag) error, args ...any) {
	p.batch.Queue(sql, args...)
	p.checks = append(p.checks, check)
}

func (p *writePipeline) flush(ctx context.Context, tx pgx.Tx) (err error) {
	if len(p.checks) == 0 {
		return nil
	}
	checks := p.checks
	batch := p.batch
	p.batch, p.checks = pgx.Batch{}, nil
	results := tx.SendBatch(ctx, &batch)
	defer func() {
		closeErr := results.Close()
		if err == nil {
			err = closeErr
		}
	}()
	for _, check := range checks {
		tag, err := results.Exec()
		if err != nil {
			return err
		}
		if check != nil {
			if err := check(tag); err != nil {
				return err
			}
		}
	}
	return nil
}

// flushBefore preserves error order when preparing a later statement fails:
// the statements queued ahead of it run first and their failure wins.
func (p *writePipeline) flushBefore(ctx context.Context, tx pgx.Tx, prepareErr error) error {
	if err := p.flush(ctx, tx); err != nil {
		return err
	}
	return prepareErr
}

func expectAffected(want int, mismatch error) func(pgconn.CommandTag) error {
	return func(tag pgconn.CommandTag) error { return expectRows(tag, want, mismatch) }
}
