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

func TestInTxRollsBackOnError(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	pool, err := Open(context.Background(), databaseURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	_, err = pool.Exec(context.Background(), "create table public.rollback_probe (id integer primary key)")
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("stop")
	err = pool.InTx(context.Background(), pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, execErr := tx.Exec(context.Background(), "insert into public.rollback_probe values (1)"); execErr != nil {
			return execErr
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v", err)
	}
	var count int
	if err := pool.QueryRow(context.Background(), "select count(*) from public.rollback_probe").Scan(&count); err != nil {
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

	_, err = pool.Exec(context.Background(), "create table public.panic_probe (id integer primary key)")
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
			if _, execErr := tx.Exec(context.Background(), "insert into public.panic_probe values (1)"); execErr != nil {
				return execErr
			}
			panic(panicValue)
		})
	}()

	var count int
	if err := pool.QueryRow(context.Background(), "select count(*) from public.panic_probe").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("count = %d", count)
	}
}

func TestInTxExpiredDeadlineRollsBackAndReleasesConnection(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	pool, err := Open(context.Background(), databaseURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	baseline := pool.Stats().AcquiredConns
	deadline, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err = pool.InTx(deadline, pgx.TxOptions{}, func(pgx.Tx) error {
		<-deadline.Done()
		return deadline.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("InTx error = %v", err)
	}
	if got := pool.Stats().AcquiredConns; got != baseline {
		t.Fatalf("acquired connections after expired rollback = %d, want %d", got, baseline)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("pool after expired rollback: %v", err)
	}
}
