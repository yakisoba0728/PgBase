//go:build integration

package adminauth

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCleanerDeletesStaleRateLimitsInBoundedBatches(t *testing.T) {
	pool := newAdminAuthPool(t)
	now := time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)
	if _, err := pool.Exec(context.Background(), `
		insert into pgbase_meta.admin_rate_limits
			(subject_hash, window_started_at, failures, blocked_until, updated_at)
		select digest::bytea, $1, 1, null, $1
		from (select decode(md5(i::text) || md5((i + 10000)::text), 'hex') as digest from generate_series(1, 1005) i) rows`, now.Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	freshHash := HashSessionToken([]byte("0123456789abcdef0123456789abcdef"), "fresh")
	if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.admin_rate_limits
		(subject_hash, window_started_at, failures, blocked_until, updated_at) values ($1, $2, 1, null, $2)`, freshHash, now); err != nil {
		t.Fatal(err)
	}

	cleaner := NewCleaner(pool, &fakeClock{now: now})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- cleaner.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		var count int
		if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.admin_rate_limits").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rate-limit row count = %d, want 1 after cleaning", count)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Fatalf("Cleaner.Run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Cleaner.Run did not stop after cancellation")
	}

	var remaining []byte
	if err := pool.QueryRow(context.Background(), "select subject_hash from pgbase_meta.admin_rate_limits").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if string(remaining) != string(freshHash) {
		t.Fatal("cleaner removed the fresh row instead of retaining it")
	}
}

func TestCleanerDeletesOnlyStaleExpiredAndRevokedSessions(t *testing.T) {
	pool := newAdminAuthPool(t)
	now := time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)
	administrator := Administrator{ID: uuid.New(), Email: "cleaner@example.com", Active: true, CreatedAt: now, UpdatedAt: now}
	if _, err := NewStore(pool).CreateAdministrator(context.Background(), administrator, "hash"); err != nil {
		t.Fatal(err)
	}
	store := NewStore(pool)
	staleExpired := HashSessionToken([]byte("0123456789abcdef0123456789abcdef"), "stale-expired")
	staleRevoked := HashSessionToken([]byte("0123456789abcdef0123456789abcdef"), "stale-revoked")
	fresh := HashSessionToken([]byte("0123456789abcdef0123456789abcdef"), "fresh-session")
	if err := store.CreateSession(context.Background(), uuid.New(), administrator.ID, staleExpired, now.Add(-25*time.Hour), now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(context.Background(), uuid.New(), administrator.ID, staleRevoked, now.Add(time.Hour), now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeSession(context.Background(), staleRevoked, now.Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(context.Background(), uuid.New(), administrator.ID, fresh, now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}

	cleaner := NewCleaner(pool, &fakeClock{now: now})
	if err := cleaner.cleanDatabase(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.admin_sessions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("remaining sessions = %d, want 1", count)
	}
	if _, _, err := store.AuthenticateSession(context.Background(), fresh, now); err != nil {
		t.Fatalf("fresh session was removed: %v", err)
	}
}
