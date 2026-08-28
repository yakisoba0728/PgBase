package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestInTxRollsBackOnError(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	pool, err := Open(context.Background(), databaseURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	_, err = pool.Exec(context.Background(), "create table rollback_probe (id integer primary key)")
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("stop")
	err = pool.InTx(context.Background(), pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, execErr := tx.Exec(context.Background(), "insert into rollback_probe values (1)"); execErr != nil {
			return execErr
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v", err)
	}
	var count int
	if err := pool.QueryRow(context.Background(), "select count(*) from rollback_probe").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("count = %d", count)
	}
}

func TestInTxRollsBackOnPanic(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	pool, err := Open(context.Background(), databaseURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	_, err = pool.Exec(context.Background(), "create table panic_probe (id integer primary key)")
	if err != nil {
		t.Fatal(err)
	}
	panicValue := errors.New("stop")
	func() {
		defer func() {
			if got := recover(); got != panicValue {
				t.Fatalf("panic = %v, want %v", got, panicValue)
			}
		}()
		_ = pool.InTx(context.Background(), pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, execErr := tx.Exec(context.Background(), "insert into panic_probe values (1)"); execErr != nil {
				return execErr
			}
			panic(panicValue)
		})
	}()

	var count int
	if err := pool.QueryRow(context.Background(), "select count(*) from panic_probe").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("count = %d", count)
	}
}

func TestBoundedCleanupContextAllowsCleanupAfterCancellationAndRetainsEarlierDeadline(t *testing.T) {
	canceledParent, cancelParent := context.WithCancel(context.Background())
	cancelParent()
	cleanupCtx, cancelCleanup := BoundedCleanupContext(canceledParent)
	defer cancelCleanup()
	select {
	case <-cleanupCtx.Done():
		t.Fatalf("cleanup context inherited cancellation without a deadline: %v", cleanupCtx.Err())
	default:
	}
	deadline, ok := cleanupCtx.Deadline()
	if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 2*time.Second {
		t.Fatalf("cleanup deadline = %v, present=%t", deadline, ok)
	}

	parentDeadline := time.Now().Add(25 * time.Millisecond)
	deadlineParent, cancelDeadlineParent := context.WithDeadline(context.Background(), parentDeadline)
	defer cancelDeadlineParent()
	earlierCleanup, cancelEarlierCleanup := BoundedCleanupContext(deadlineParent)
	defer cancelEarlierCleanup()
	gotDeadline, ok := earlierCleanup.Deadline()
	if !ok || gotDeadline.After(parentDeadline) {
		t.Fatalf("cleanup deadline = %v, want no later than %v", gotDeadline, parentDeadline)
	}
}
