package database

import (
	"context"

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
			_ = tx.Rollback(context.Background())
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
