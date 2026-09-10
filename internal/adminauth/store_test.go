//go:build integration

package adminauth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func newAdminAuthPool(t *testing.T) *database.Pool {
	t.Helper()
	databaseURL := testutil.NewDatabase(t)
	pool, err := database.Open(context.Background(), databaseURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestStoreCreatesAdministratorAndPersistsSessionLifecycle(t *testing.T) {
	pool := newAdminAuthPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)
	admin := Administrator{ID: uuid.New(), Email: "admin@example.com", Active: true, CreatedAt: now, UpdatedAt: now}
	created, err := store.CreateAdministrator(ctx, admin, "encoded-password-hash")
	if err != nil {
		t.Fatal(err)
	}
	if created != admin {
		t.Fatalf("created administrator = %#v, want %#v", created, admin)
	}

	tokenHash := HashSessionToken([]byte("0123456789abcdef0123456789abcdef"), "raw-token")
	expires := now.Add(12 * time.Hour)
	if err := store.CreateSession(ctx, uuid.New(), admin.ID, tokenHash, expires, now); err != nil {
		t.Fatal(err)
	}
	got, gotExpires, err := store.AuthenticateSession(ctx, tokenHash, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got != admin {
		t.Fatalf("authenticated administrator = %#v, want %#v", got, admin)
	}
	if !gotExpires.Equal(expires) {
		t.Fatalf("authenticated session expiration = %s, want %s", gotExpires, expires)
	}
	if _, _, err := store.AuthenticateSession(ctx, tokenHash, expires); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("authenticate at expiration error = %v, want ErrInvalidCredentials", err)
	}
	if err := store.RevokeSession(ctx, tokenHash, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AuthenticateSession(ctx, tokenHash, now.Add(3*time.Minute)); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("authenticate revoked session error = %v, want ErrInvalidCredentials", err)
	}
}

func TestStoreAuthenticateSessionCoalescesLastSeenWrites(t *testing.T) {
	pool := newAdminAuthPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	createdAt := time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)
	administrator := Administrator{ID: uuid.New(), Email: "admin@example.com", Active: true, CreatedAt: createdAt, UpdatedAt: createdAt}
	if _, err := store.CreateAdministrator(ctx, administrator, "encoded-password-hash"); err != nil {
		t.Fatal(err)
	}
	tokenHash := HashSessionToken([]byte("0123456789abcdef0123456789abcdef"), "coalesced-token")
	if err := store.CreateSession(ctx, uuid.New(), administrator.ID, tokenHash, createdAt.Add(time.Hour), createdAt); err != nil {
		t.Fatal(err)
	}
	readLastSeen := func() time.Time {
		t.Helper()
		var lastSeen time.Time
		if err := pool.QueryRow(ctx, "select last_seen_at from pgbase_meta.admin_sessions where token_hash = $1", tokenHash).Scan(&lastSeen); err != nil {
			t.Fatal(err)
		}
		return lastSeen.UTC()
	}

	for _, authenticatedAt := range []time.Time{createdAt.Add(time.Minute), createdAt.Add(4 * time.Minute)} {
		if _, _, err := store.AuthenticateSession(ctx, tokenHash, authenticatedAt); err != nil {
			t.Fatalf("AuthenticateSession at %s: %v", authenticatedAt, err)
		}
		if lastSeen := readLastSeen(); !lastSeen.Equal(createdAt) {
			t.Fatalf("last_seen_at after fresh authentication = %s, want %s", lastSeen, createdAt)
		}
	}
	staleAuthentication := createdAt.Add(6 * time.Minute)
	if _, _, err := store.AuthenticateSession(ctx, tokenHash, staleAuthentication); err != nil {
		t.Fatalf("AuthenticateSession after refresh interval: %v", err)
	}
	if lastSeen := readLastSeen(); !lastSeen.Equal(staleAuthentication) {
		t.Fatalf("last_seen_at after stale authentication = %s, want %s", lastSeen, staleAuthentication)
	}
}

func TestStoreEnforcesNormalizedEmailUniqueness(t *testing.T) {
	pool := newAdminAuthPool(t)
	store := NewStore(pool)
	now := time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)
	first := Administrator{ID: uuid.New(), Email: " \u3000ADMIN@Example.COM\u3000 ", Active: true, CreatedAt: now, UpdatedAt: now}
	created, err := store.CreateAdministrator(context.Background(), first, "hash")
	if err != nil {
		t.Fatal(err)
	}
	if created.Email != "admin@example.com" {
		t.Fatalf("created email = %q, want normalized email", created.Email)
	}
	second := first
	second.ID = uuid.New()
	second.Email = "admin@example.com"
	if _, err := store.CreateAdministrator(context.Background(), second, "other-hash"); err == nil {
		t.Fatal("duplicate normalized email was accepted")
	}
}

func TestStoreSharesRateLimitsAcrossInstances(t *testing.T) {
	pool := newAdminAuthPool(t)
	stores := []*Store{NewStore(pool), NewStore(pool)}
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)
	subjectHash := HashSessionToken([]byte("0123456789abcdef0123456789abcdef"), "rate-subject")
	for attempt := 1; attempt <= 5; attempt++ {
		blocked, err := stores[attempt%2].RecordRateLimitFailure(ctx, subjectHash, now, 5)
		if err != nil {
			t.Fatal(err)
		}
		if blocked != (attempt == 5) {
			t.Fatalf("attempt %d blocked = %t, want %t", attempt, blocked, attempt == 5)
		}
	}
	blocked, err := stores[0].RateLimitBlocked(ctx, subjectHash, now.Add(time.Minute))
	if err != nil || !blocked {
		t.Fatalf("shared rate limit blocked = %t, err = %v", blocked, err)
	}
	if err := stores[1].ClearRateLimit(ctx, subjectHash); err != nil {
		t.Fatal(err)
	}
	blocked, err = stores[0].RateLimitBlocked(ctx, subjectHash, now.Add(time.Minute))
	if err != nil || blocked {
		t.Fatalf("cleared shared rate limit blocked = %t, err = %v", blocked, err)
	}
}

func TestStoreRateLimitContentionRollsBackEarlierAdvisoryLocks(t *testing.T) {
	pool := newAdminAuthPool(t)
	ctx := context.Background()
	blocker, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Release()
	defer func() { _, _ = blocker.Exec(context.Background(), "select pg_advisory_unlock_all()") }()

	keys := []int64{0x7067626173651001, 0x7067626173651002}
	if _, err := blocker.Exec(ctx, "select pg_advisory_lock($1)", keys[1]); err != nil {
		t.Fatal(err)
	}
	err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return NewStore(tx).LockRateLimits(ctx, keys)
	})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("LockRateLimits under contention error = %v, want ErrRateLimited", err)
	}
	var reacquired bool
	if err := blocker.QueryRow(ctx, "select pg_try_advisory_lock($1)", keys[0]).Scan(&reacquired); err != nil {
		t.Fatal(err)
	}
	if !reacquired {
		t.Fatal("first advisory lock remained held after transaction rollback")
	}
}
