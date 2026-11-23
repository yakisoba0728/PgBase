//go:build integration

package database

import (
	"context"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/testutil"
)

const coordinationFixtureLock int64 = 0x7067626173650fff

func openCoordinationFixture(t *testing.T) *Pool {
	t.Helper()
	pool, err := Open(t.Context(), testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// advisoryLocks counts the granted advisory locks a backend holds on key.
func advisoryLocks(t *testing.T, pool *Pool, pid uint32, key int64) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(t.Context(), `select count(*) from pg_locks where locktype='advisory' and pid=$1 and granted
		and classid=($2::bigint >> 32)::oid and objid=($2::bigint & 4294967295)::oid and objsubid=1`, int32(pid), key).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func backendAlive(t *testing.T, pool *Pool, pid uint32) bool {
	t.Helper()
	var alive bool
	if err := pool.QueryRow(t.Context(), `select exists(select 1 from pg_stat_activity where pid=$1)`, int32(pid)).Scan(&alive); err != nil {
		t.Fatal(err)
	}
	return alive
}

func TestReusedCoordinationSessionDropsBorrowerLocksAndKeepsWriterGate(t *testing.T) {
	pool := openCoordinationFixture(t)
	ctx := t.Context()
	first, err := pool.BorrowCoordinationSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pid := first.Conn().PgConn().PID()
	if _, err := first.Conn().Exec(ctx, `select pg_advisory_lock_shared($1), pg_advisory_lock_shared($1), pg_advisory_lock($1)`, coordinationFixtureLock); err != nil {
		t.Fatal(err)
	}
	first.Release(true)
	first.Release(true) // idempotent
	if held := advisoryLocks(t, pool, pid, coordinationFixtureLock); held != 0 {
		t.Fatalf("released session still holds %d borrower locks", held)
	}
	if held := advisoryLocks(t, pool, pid, backupGateKey); held != 1 {
		t.Fatalf("released session holds the writer gate %d times, want 1", held)
	}
	second, err := pool.BorrowCoordinationSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := second.Conn().PgConn().PID(); got != pid {
		t.Fatalf("second borrow used backend %d, want reused backend %d", got, pid)
	}
	// Without reuse the session is closed, so its locks cannot outlive it.
	second.Release(false)
	waitBackendGone(t, pool, pid, "unreusable session stayed open")
	third, err := pool.BorrowCoordinationSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := third.Conn().PgConn().PID(); got == pid {
		t.Fatal("closed session was handed out again")
	}
	third.Release(true)
}

func TestCoordinationBorrowBeyondReusableSessionsDoesNotWait(t *testing.T) {
	pool := openCoordinationFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	sessions := make([]*CoordinationSession, 0, maximumReusableCoordinationSessions+1)
	for range maximumReusableCoordinationSessions + 1 {
		session, err := pool.BorrowCoordinationSession(ctx)
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, session)
	}
	extra := sessions[len(sessions)-1]
	if extra.pooled != nil {
		t.Fatal("borrow beyond the reusable bound took a reusable session")
	}
	pid := extra.Conn().PgConn().PID()
	for _, session := range sessions {
		session.Release(true)
	}
	waitBackendGone(t, pool, pid, "dedicated session stayed open after release")
}

func waitBackendGone(t *testing.T, pool *Pool, pid uint32, message string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for backendAlive(t, pool, pid) {
		if time.Now().After(deadline) {
			t.Fatal(message)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPoolCloseClosesReusableCoordinationSessions(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	observer, err := Open(t.Context(), databaseURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(observer.Close)
	pool, err := Open(t.Context(), databaseURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	session, err := pool.BorrowCoordinationSession(t.Context())
	if err != nil {
		pool.Close()
		t.Fatal(err)
	}
	pid := session.Conn().PgConn().PID()
	session.Release(true)
	if !backendAlive(t, observer, pid) {
		pool.Close()
		t.Fatal("reusable session was not kept for reuse")
	}
	pool.Close()
	waitBackendGone(t, observer, pid, "pool close left a reusable coordination session holding the writer gate")
	late, err := pool.BorrowCoordinationSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if late.pooled != nil {
		t.Fatal("closed pool handed out a reusable session")
	}
	late.Release(true)
}
