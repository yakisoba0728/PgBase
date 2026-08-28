package adminauth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
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
