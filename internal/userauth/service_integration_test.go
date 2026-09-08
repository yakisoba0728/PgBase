//go:build integration

package userauth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/adminauth"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func newUserAuthPool(t *testing.T) *database.Pool {
	t.Helper()
	url := testutil.NewDatabase(t)
	pool, err := database.Open(context.Background(), url, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestPublicUserSessionLifecycleAndHashedStorage(t *testing.T) {
	pool := newUserAuthPool(t)
	service := NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	registered, err := service.Register(ctx, " User@Example.com ", "correct horse battery staple", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if registered.User.Email != "user@example.com" || registered.RawToken == "" {
		t.Fatalf("registered session = %#v", registered)
	}
	var stored []byte
	if err := pool.QueryRow(ctx, `select token_hash from pgbase_meta.user_sessions where user_id=$1`, registered.User.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if string(stored) == registered.RawToken {
		t.Fatal("raw bearer token was persisted")
	}
	principal, err := service.Authenticate(ctx, registered.RawToken)
	if err != nil {
		t.Fatal(err)
	}
	if principal.User != registered.User {
		t.Fatalf("principal user = %#v, want %#v", principal.User, registered.User)
	}
	if err := service.Logout(ctx, registered.RawToken); err != nil {
		t.Fatal(err)
	}
	if err := service.Logout(ctx, registered.RawToken); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, registered.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate after logout = %v", err)
	}
}

func TestLoginAndRegistrationConflict(t *testing.T) {
	pool := newUserAuthPool(t)
	service := NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	if _, err := service.Register(ctx, "user@example.com", "correct horse battery staple", "192.0.2.2"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Register(ctx, " USER@example.com ", "another correct horse battery", "192.0.2.3"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate Register = %v", err)
	}
	if _, err := service.Login(ctx, "user@example.com", "correct horse battery staple", "192.0.2.4"); err != nil {
		t.Fatalf("Login = %v", err)
	}
	for _, tc := range []struct{ email, password string }{{"missing@example.com", "correct horse battery staple"}, {"user@example.com", "wrong password value"}, {"bad-email", "wrong password value"}} {
		if _, err := service.Login(ctx, tc.email, tc.password, "192.0.2.5"); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("Login(%q) = %v, want uniform ErrInvalidCredentials", tc.email, err)
		}
	}
}

func TestCredentialRequestCleansBoundedStaleAuthenticationRows(t *testing.T) {
	pool := newUserAuthPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		insert into pgbase_meta.user_auth_rate_limits(subject_hash,window_started_at,failures,blocked_until,updated_at)
		values(decode(repeat('ab',32),'hex'),CURRENT_TIMESTAMP-interval '1 hour',1,null,CURRENT_TIMESTAMP-interval '1 hour')`); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	if _, err := service.Register(ctx, "cleanup@example.com", "correct horse battery staple", "192.0.2.9"); err != nil {
		t.Fatal(err)
	}
	var stale int
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_auth_rate_limits where subject_hash=decode(repeat('ab',32),'hex')`).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatalf("stale rate-limit rows = %d, want 0", stale)
	}
}

func TestLoginAccountLimitSurvivesRotatingNetworkAddresses(t *testing.T) {
	pool := newUserAuthPool(t)
	service := NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	if _, err := service.Register(ctx, "victim@example.com", "correct horse battery staple", "registration-client"); err != nil {
		t.Fatal(err)
	}

	for _, email := range []string{"victim@example.com", "unknown@example.com"} {
		for attempt := 1; attempt <= loginAccountLimit; attempt++ {
			_, err := service.Login(ctx, email, "wrong password value", "rotating-client-"+email+string(rune('a'+attempt)))
			if attempt < loginAccountLimit {
				if !errors.Is(err, ErrInvalidCredentials) {
					t.Fatalf("%s attempt %d = %v, want uniform ErrInvalidCredentials", email, attempt, err)
				}
				continue
			}
			if !errors.Is(err, ErrRateLimited) {
				t.Fatalf("%s threshold attempt = %v, want ErrRateLimited", email, err)
			}
		}
	}

	if _, err := service.Login(ctx, "victim@example.com", "correct horse battery staple", "fresh-network-client"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("correct login during account block = %v, want ErrRateLimited", err)
	}
}
