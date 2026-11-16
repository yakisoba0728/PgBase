//go:build integration

package pgbase

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

type failingTrustedSavepointParent struct {
	pgx.Tx
	rollbackErr error
	cleanup     func(context.Context)
}

func (p failingTrustedSavepointParent) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := p.Tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return failingTrustedSavepoint{Tx: tx, rollbackErr: p.rollbackErr, cleanup: p.cleanup}, nil
}

type failingTrustedSavepoint struct {
	pgx.Tx
	rollbackErr error
	cleanup     func(context.Context)
}

func (s failingTrustedSavepoint) Rollback(ctx context.Context) error {
	s.cleanup(ctx)
	// Inject the failure before PostgreSQL rolls back the savepoint. The outer
	// connection is usable unless the transaction boundary invalidates it.
	return s.rollbackErr
}

func TestTrustedSavepointRollbackFailurePreventsOuterCommit(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) { options.EnableTrustedDatabaseAccess = true })
	db, err := fixture.app.TrustedDatabase()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `CREATE TABLE public.trusted_rollback_probe (id integer PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	outer, err := fixture.app.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer outer.Rollback(context.Background())
	rollbackErr := errors.New("injected savepoint rollback failure")
	operationErr := errors.New("nested operation failure")
	cleanupCalls := 0
	parent := failingTrustedSavepointParent{Tx: outer, rollbackErr: rollbackErr, cleanup: func(ctx context.Context) {
		cleanupCalls++
		if _, ok := ctx.Deadline(); !ok {
			t.Error("savepoint cleanup has no deadline")
		}
		if ctx.Err() != nil {
			t.Errorf("cleanup inherited cancellation: %v", ctx.Err())
		}
	}}
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), trustedTransactionKey{}, trustedTransaction{owner: db, tx: parent}))
	defer cancel()
	err = db.RunInTransaction(ctx, func(nested context.Context) error {
		if _, err := db.Exec(nested, `INSERT INTO public.trusted_rollback_probe VALUES (1)`); err != nil {
			return err
		}
		cancel()
		return operationErr
	})
	if !errors.Is(err, operationErr) || !errors.Is(err, rollbackErr) {
		t.Errorf("nested outcome lost operation or cleanup failure: %v", err)
	}
	if cleanupCalls != 1 {
		t.Errorf("cleanup calls = %d, want 1", cleanupCalls)
	}
	if err := outer.Commit(t.Context()); err == nil {
		t.Error("outer commit accepted writes from a failed savepoint rollback")
	}
	var count int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM public.trusted_rollback_probe`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial writes survived failed cleanup: count=%d error=%v", count, err)
	}
}
