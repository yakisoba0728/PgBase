//go:build integration

package adminauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

// TestAuthenticateFromAddressMatchesAuthenticateThenAuthorizeIP pins the
// one-statement admin session check to the two calls it replaces: session
// failures first and unchanged, then the allowlist decision AuthorizeIP makes.
func TestAuthenticateFromAddressMatchesAuthenticateThenAuthorizeIP(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, NewArgon2idPasswords(), []byte("01234567890123456789012345678901"), "", time.Hour, nil)
	administrator, _, err := service.UpsertAdministrator(ctx, "session-ip@example.test", "first correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	issued, err := service.Login(ctx, administrator.Email, "first correct horse battery staple", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}

	check := func(name, address string, valid bool) {
		t.Helper()
		want, wantErr := service.Authenticate(ctx, issued.RawToken)
		if wantErr == nil && !valid {
			wantErr = ErrIPRestricted
		} else if wantErr == nil {
			wantErr = service.AuthorizeIP(ctx, want.Administrator.ID, address)
		}
		got, gotErr := service.AuthenticateFromAddress(ctx, issued.RawToken, address, valid)
		if !errors.Is(gotErr, wantErr) && !(gotErr == nil && wantErr == nil) {
			t.Fatalf("%s: AuthenticateFromAddress error = %v, want %v", name, gotErr, wantErr)
		}
		if wantErr == nil && (got.Administrator != want.Administrator || !got.ExpiresAt.Equal(want.ExpiresAt) || got.CSRFToken != want.CSRFToken) {
			t.Fatalf("%s: principal = %#v, want %#v", name, got, want)
		}
		if wantErr != nil && got != (Principal{}) {
			t.Fatalf("%s: rejected principal = %#v", name, got)
		}
	}
	check("no allowlist", "198.51.100.1", true)
	check("no allowlist, unresolved address", "", false)
	if err := service.AllowIP(ctx, administrator.Email, "192.0.2.0/24"); err != nil {
		t.Fatal(err)
	}
	if err := service.AllowIP(ctx, administrator.Email, "2001:db8::/32"); err != nil {
		t.Fatal(err)
	}
	check("allowed v4", "192.0.2.5", true)
	check("allowed mapped v4", "::ffff:192.0.2.9", true)
	check("allowed v6", "2001:db8::1", true)
	check("denied", "198.51.100.1", true)
	check("zoned", "fe80::1%eth0", true)
	check("unparseable", "unknown", true)
	check("unresolved address", "192.0.2.5", false)

	if _, err := service.AuthenticateFromAddress(ctx, "invalid-token", "198.51.100.1", false); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("invalid session error = %v, want ErrInvalidCredentials before the allowlist", err)
	}
}
