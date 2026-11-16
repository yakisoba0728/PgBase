//go:build integration

package pgbase

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/recordops"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

// Drop real PostgreSQL replies after the transaction has written data. Reads
// still use the socket's normal deadlines, so this models a lost response while
// exercising pgx's real cancellation and connection-discard behavior.
type extensionLostReplyConn struct {
	net.Conn
	drop    *atomic.Bool
	dropped *atomic.Int64
}

func (c extensionLostReplyConn) Read(buffer []byte) (int, error) {
	for {
		n, err := c.Conn.Read(buffer)
		if n > 0 && c.drop.Load() {
			c.dropped.Add(int64(n))
			if err != nil {
				return 0, err
			}
			continue
		}
		return n, err
	}
}

func TestExtensionHostCloseBoundsRollbackWhenDatabaseRepliesStop(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	var drop atomic.Bool
	var dropped atomic.Int64
	var wire net.Conn
	config.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		connection, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		wire = connection
		return extensionLostReplyConn{Conn: connection, drop: &drop, dropped: &dropped}, nil
	}
	connection, err := pgx.ConnectConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wire.Close() })
	if _, err := connection.Exec(t.Context(), `CREATE TABLE public.extension_cleanup_probe(id integer PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	tx, err := connection.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `INSERT INTO public.extension_cleanup_probe VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	outcome := recordops.NewTransactionOutcome()
	var calls int
	var outcomeErr error
	if !outcome.Register(func(_ context.Context, err error) { calls++; outcomeErr = err }) {
		t.Fatal("could not register transaction observer")
	}
	host := &extensionHost{authorityContext: ctx, databaseTx: tx, databaseToken: uuid.New(), recordOutcome: outcome}
	drop.Store(true)
	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- host.Close() }()
	select {
	case err = <-done:
	case <-time.After(3 * time.Second):
		// Bound the failing baseline too; closing only this disposable socket
		// unblocks its reader so the test never leaks a hung host goroutine.
		_ = wire.Close()
		<-done
		t.Fatal("host cleanup exceeded three seconds after database replies stopped")
	}
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(outcomeErr, context.DeadlineExceeded) {
		t.Fatalf("cleanup/observer did not report the independent deadline: %v / %v", err, outcomeErr)
	}
	if dropped.Load() == 0 {
		t.Fatal("fixture did not drop a PostgreSQL response")
	}
	if !connection.IsClosed() {
		t.Fatal("failed rollback left an ambiguous PostgreSQL connection reusable")
	}
	if host.databaseTx != nil || host.databaseToken != uuid.Nil || host.recordOutcome != nil || host.objectTx != nil {
		t.Fatal("cleanup retained invocation transaction state")
	}
	if err := host.Close(); err != nil || calls != 1 {
		t.Fatalf("repeated close outcome=%v callbacks=%d", err, calls)
	}
	check, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close(context.Background())
	var rows int
	if err := check.QueryRow(t.Context(), `SELECT count(*) FROM public.extension_cleanup_probe`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("failed cleanup retained writes: rows=%d error=%v", rows, err)
	}
	t.Logf("lost-response cleanup completed in %s; observer called once; connection discarded; rows=%d", time.Since(started), rows)
}
