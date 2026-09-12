package database

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// InTx runs fn in a transaction and commits only when fn returns nil.
func (p *Pool) InTx(ctx context.Context, options pgx.TxOptions, fn func(pgx.Tx) error) error {
	tx, err := p.inner.BeginTx(ctx, options)
	if err != nil {
		return err
	}

	committed := false
	defer func() {
		if !committed {
			cleanupCtx, cancel := BoundedCleanupContext(ctx)
			defer cancel()
			_ = tx.Rollback(cleanupCtx)
		}
	}()

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	committed = true
	return nil
}

const cleanupTimeout = time.Second

// BoundedCleanupContext permits cleanup after cancellation without allowing a
// rollback or connection close to outlive a short, independent cleanup budget.
func BoundedCleanupContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(parent), cleanupTimeout)
}
