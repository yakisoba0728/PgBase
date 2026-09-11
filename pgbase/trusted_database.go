package pgbase

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/database"
)

// TrustedDatabase is the explicit SQL seam for in-process Go extensions. SQL
// text is never accepted from HTTP or JavaScript by this API.
type TrustedDatabase struct{ pool *database.Pool }

type trustedTransactionKey struct{}
type trustedTransaction struct {
	owner *TrustedDatabase
	tx    pgx.Tx
}

type trustedQuerier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// TrustedDatabase returns the SQL extension seam only after explicit startup
// opt-in. The returned handle shares the application's connection lifecycle.
func (a *App) TrustedDatabase() (*TrustedDatabase, error) {
	if a == nil || a.pool == nil {
		return nil, errors.New("trusted database is unavailable")
	}
	if !a.options.EnableTrustedDatabaseAccess {
		return nil, errors.New("trusted database access is not enabled")
	}
	return &TrustedDatabase{pool: a.pool}, nil
}

func (d *TrustedDatabase) querier(ctx context.Context) (trustedQuerier, error) {
	if d == nil || d.pool == nil {
		return nil, errors.New("trusted database is unavailable")
	}
	if bound, ok := ctx.Value(trustedTransactionKey{}).(trustedTransaction); ok && bound.owner == d {
		return bound.tx, nil
	}
	return d.pool, nil
}

func (d *TrustedDatabase) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	q, err := d.querier(ctx)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	return q.Exec(ctx, sql, arguments...)
}
func (d *TrustedDatabase) Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error) {
	q, err := d.querier(ctx)
	if err != nil {
		return nil, err
	}
	return q.Query(ctx, sql, arguments...)
}
func (d *TrustedDatabase) QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row {
	q, err := d.querier(ctx)
	if err != nil {
		return errorRow{err}
	}
	return q.QueryRow(ctx, sql, arguments...)
}

// RunInTransaction starts a transaction, or a PostgreSQL savepoint when ctx
// already carries one created by this method. Database calls using callbackCtx
// are pinned to that transaction, including calls made by helper functions.
func (d *TrustedDatabase) RunInTransaction(ctx context.Context, fn func(callbackCtx context.Context) error) error {
	if fn == nil {
		return errors.New("transaction callback is required")
	}
	if bound, ok := ctx.Value(trustedTransactionKey{}).(trustedTransaction); ok && bound.owner == d {
		nested, err := bound.tx.Begin(ctx)
		if err != nil {
			return err
		}
		return runTrustedTransaction(ctx, d, nested, fn)
	}
	if d == nil || d.pool == nil {
		return errors.New("trusted database is unavailable")
	}
	return d.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return fn(context.WithValue(ctx, trustedTransactionKey{}, trustedTransaction{owner: d, tx: tx}))
	})
}

func runTrustedTransaction(ctx context.Context, owner *TrustedDatabase, tx pgx.Tx, fn func(context.Context) error) (err error) {
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if err = fn(context.WithValue(ctx, trustedTransactionKey{}, trustedTransaction{owner: owner, tx: tx})); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	committed = true
	return nil
}

type errorRow struct{ err error }

func (r errorRow) Scan(...any) error { return r.err }

// TrustedQuery is a small immutable builder that keeps SQL and arguments
// together for review and can execute through a transaction-aware handle.
type TrustedQuery struct {
	SQL       string
	Arguments []any
}

func NewTrustedQuery(sql string, arguments ...any) TrustedQuery {
	return TrustedQuery{SQL: sql, Arguments: append([]any(nil), arguments...)}
}
func (q TrustedQuery) Exec(ctx context.Context, database *TrustedDatabase) (pgconn.CommandTag, error) {
	return database.Exec(ctx, q.SQL, q.Arguments...)
}
func (q TrustedQuery) Query(ctx context.Context, database *TrustedDatabase) (pgx.Rows, error) {
	return database.Query(ctx, q.SQL, q.Arguments...)
}
func (q TrustedQuery) QueryRow(ctx context.Context, database *TrustedDatabase) pgx.Row {
	return database.QueryRow(ctx, q.SQL, q.Arguments...)
}
