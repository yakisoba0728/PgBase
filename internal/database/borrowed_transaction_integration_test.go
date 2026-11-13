//go:build integration

package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestBorrowedTransactionChecksDatabaseStateAndCallerIsolation(t *testing.T) {
	ctx := context.Background()
	pool, err := Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	other, err := Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(other.Close)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	scope := WithTransaction(ctx, tx)
	if borrowed, present, err := pool.BorrowedTransaction(scope); err != nil || !present || borrowed != tx {
		t.Fatalf("same database transaction = %v/%t, %v", borrowed, present, err)
	}
	if _, present, err := other.BorrowedTransaction(scope); !present || !errors.Is(err, ErrTransactionDatabaseMismatch) {
		t.Fatalf("other database transaction accepted: %t, %v", present, err)
	}
	if _, present, err := pool.BorrowedTransaction(WithTransaction(ctx, nil)); !present || !errors.Is(err, ErrTransactionUnavailable) {
		t.Fatalf("nil transaction fell back to independent operation: %t, %v", present, err)
	}
	canceled, cancel := context.WithCancel(scope)
	cancel()
	if _, _, err := pool.BorrowedTransaction(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("borrowed cancellation = %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, present, err := pool.BorrowedTransaction(scope); !present || !errors.Is(err, ErrTransactionUnavailable) {
		t.Fatalf("closed transaction accepted: %t, %v", present, err)
	}
	if err := pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		if _, _, err := pool.BorrowedTransaction(WithTransaction(ctx, tx)); err != nil {
			return err
		}
		if err := RequireReadCommitted(ctx, tx); !errors.Is(err, ErrTransactionIsolation) {
			t.Fatalf("repeatable read write admission = %v", err)
		}
		var isolation string
		if err := tx.QueryRow(ctx, "SHOW transaction_isolation").Scan(&isolation); err != nil {
			return err
		}
		if isolation != string(pgx.RepeatableRead) {
			t.Fatalf("caller isolation changed to %q", isolation)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSavepointFailurePreservesOwnerTransaction(t *testing.T) {
	ctx := context.Background()
	pool, err := Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "create table public.savepoint_probe (id integer primary key)"); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("reject compound operation")
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "insert into public.savepoint_probe values (1)"); err != nil {
			return err
		}
		if err := InSavepoint(ctx, tx, func(child pgx.Tx) error {
			if _, err := child.Exec(ctx, "insert into public.savepoint_probe values (2)"); err != nil {
				return err
			}
			return failure
		}); !errors.Is(err, failure) {
			t.Fatalf("savepoint failure = %v", err)
		}
		var count int
		if err := tx.QueryRow(ctx, "select count(*) from public.savepoint_probe").Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Fatalf("savepoint retained partial work: %d", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var id int
	if err := pool.QueryRow(ctx, "select id from public.savepoint_probe").Scan(&id); err != nil || id != 1 {
		t.Fatalf("owner commit = %d, %v", id, err)
	}
}

type failingSavepointOwner struct{ pgx.Tx }
type failingSavepointRollback struct{ pgx.Tx }

func (owner failingSavepointOwner) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := owner.Tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return failingSavepointRollback{tx}, nil
}

func (failingSavepointRollback) Rollback(context.Context) error {
	return errors.New("savepoint rollback unavailable")
}

func TestFailedSavepointCleanupCannotLeaveWritesForOuterCommit(t *testing.T) {
	ctx := context.Background()
	pool, err := Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "create table public.failed_cleanup_probe (id integer primary key)"); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	failure := errors.New("reject mutation")
	err = InSavepoint(ctx, failingSavepointOwner{tx}, func(child pgx.Tx) error {
		if _, err := child.Exec(ctx, "insert into public.failed_cleanup_probe values (1)"); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("mutation cause lost: %v", err)
	}
	if err := tx.Commit(ctx); err == nil {
		t.Fatal("outer owner committed failed savepoint work")
	}
	var count int
	if err := pool.QueryRow(ctx, "select count(*) from public.failed_cleanup_probe").Scan(&count); err != nil || count != 0 {
		t.Fatalf("unrecoverable cleanup published writes: %d, %v", count, err)
	}
}
