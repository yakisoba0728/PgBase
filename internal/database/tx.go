package database

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type transactionKey struct{}
type borrowedTransaction struct{ tx pgx.Tx }

var ErrTransactionDatabaseMismatch = errors.New("borrowed PostgreSQL transaction belongs to another database")
var ErrTransactionUnavailable = errors.New("borrowed PostgreSQL transaction is unavailable")
var ErrTransactionIsolation = errors.New("operation requires a read committed PostgreSQL transaction")

// WithTransaction lends a caller-owned transaction to record reads and writes.
// The owner alone may commit, roll back, or retry the whole operation.
func WithTransaction(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, transactionKey{}, borrowedTransaction{tx: tx})
}

func TransactionFromContext(ctx context.Context) (pgx.Tx, bool) {
	scope, ok := ctx.Value(transactionKey{}).(borrowedTransaction)
	return scope.tx, ok
}

// BorrowedTransaction validates the target before lending the transaction to a
// service. Its owner controls isolation, commit, rollback, and whole-unit retry.
func (p *Pool) BorrowedTransaction(ctx context.Context) (pgx.Tx, bool, error) {
	tx, ok := TransactionFromContext(ctx)
	if !ok {
		return nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	if p == nil || p.inner == nil || tx == nil || tx.Conn() == nil || tx.Conn().IsClosed() || tx.Conn().PgConn().TxStatus() != 'T' {
		return nil, true, ErrTransactionUnavailable
	}
	actual, expected := tx.Conn().Config(), p.inner.Config().ConnConfig
	if actual.Host != expected.Host || actual.Port != expected.Port || actual.Database != expected.Database {
		return nil, true, ErrTransactionDatabaseMismatch
	}
	return tx, true, nil
}

// RequireReadCommitted protects operations whose lock ordering requires a fresh
// snapshot after waiting. A borrowed transaction is never silently reconfigured.
func RequireReadCommitted(ctx context.Context, tx pgx.Tx) error {
	var isolation string
	if err := tx.QueryRow(ctx, "SHOW transaction_isolation").Scan(&isolation); err != nil {
		return err
	}
	if isolation != string(pgx.ReadCommitted) {
		return ErrTransactionIsolation
	}
	return nil
}

// InSavepoint keeps a compound operation atomic without completing its owner's
// transaction. Errors and panics roll back only the compound operation.
func InSavepoint(ctx context.Context, parent pgx.Tx, fn func(pgx.Tx) error) error {
	tx, err := parent.Begin(ctx)
	if err != nil {
		return err
	}
	return finishTransaction(ctx, tx, fn)
}

// InTx runs fn in a transaction and commits only when fn returns nil.
func (p *Pool) InTx(ctx context.Context, options pgx.TxOptions, fn func(pgx.Tx) error) error {
	tx, err := p.inner.BeginTx(ctx, options)
	if err != nil {
		return err
	}

	return finishTransaction(ctx, tx, fn)
}

func finishTransaction(ctx context.Context, tx pgx.Tx, fn func(pgx.Tx) error) (err error) {
	committed := false
	defer func() {
		if !committed {
			cleanupCtx, cancel := BoundedCleanupContext(ctx)
			defer cancel()
			if rollbackErr := tx.Rollback(cleanupCtx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				// A failed savepoint rollback must never leave partial writes
				// available for a later outer commit.
				closeCtx, closeCancel := BoundedCleanupContext(ctx)
				defer closeCancel()
				_ = tx.Conn().Close(closeCtx)
				err = errors.Join(err, rollbackErr)
			}
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
