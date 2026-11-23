//go:build integration

package userauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

// OAuth sign-in for an identity without a link: what a provider says about the
// email decides only the new account's verified flag, never which existing
// account the identity reaches.
func TestOAuthProviderEmailNeverLinksAndOnlyVouchedEmailIsVerified(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	service.ConfigureDefaultRegistration(true)
	service.ConfigureOAuthRegistration(true)
	ctx := context.Background()
	const taken = "oauth-taken@example.test"
	existing, err := service.Register(ctx, taken, "correct horse battery staple", "oauth-trust-register")
	if err != nil {
		t.Fatal(err)
	}
	signIn := func(identity OAuthIdentity) (Session, error) {
		t.Helper()
		if err := service.ConfigureOAuth(map[string]OAuthProvider{"custom": realmOAuthProvider{identity: identity}}, []string{preclaimRedirect}, time.Minute); err != nil {
			t.Fatal(err)
		}
		begin, err := service.BeginOAuth(ctx, "custom", preclaimRedirect, "198.51.100.50")
		if err != nil {
			t.Fatal(err)
		}
		return service.CompleteOAuth(ctx, "custom", begin.State, "code")
	}
	linked := func(subject string) int {
		t.Helper()
		var count int
		if err := service.pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_oauth_identities where provider='custom' and subject=$1`, subject).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	for _, verified := range []bool{false, true} {
		subject := "taken-unverified"
		if verified {
			subject = "taken-verified"
		}
		if _, err := signIn(OAuthIdentity{Subject: subject, Email: "OAUTH-TAKEN@example.test", EmailVerified: verified}); !errors.Is(err, ErrConflict) {
			t.Fatalf("email of an existing account (verified claim %t) = %v, want conflict", verified, err)
		}
		if linked(subject) != 0 {
			t.Fatalf("identity with verified claim %t was attached to the existing account", verified)
		}
	}
	var stillUnlinked int
	if err := service.pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_oauth_identities where user_id=$1`, existing.User.ID).Scan(&stillUnlinked); err != nil || stillUnlinked != 0 {
		t.Fatalf("existing account identities=%d err=%v", stillUnlinked, err)
	}

	for _, test := range []struct {
		name         string
		identity     OAuthIdentity
		wantEmail    string
		wantVerified bool
	}{
		{"unverified email", OAuthIdentity{Subject: "new-unverified", Email: "oauth-unverified@example.test"}, "oauth-unverified@example.test", false},
		{"vouched email", OAuthIdentity{Subject: "new-verified", Email: "oauth-verified@example.test", EmailVerified: true}, "oauth-verified@example.test", true},
		{"claim without email", OAuthIdentity{Subject: "new-no-email", EmailVerified: true}, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			session, err := signIn(test.identity)
			if err != nil {
				t.Fatal(err)
			}
			if session.User.ID == existing.User.ID || session.User.Email != test.wantEmail || session.User.Verified != test.wantVerified {
				t.Fatalf("new account email=%q verified=%t, want %q %t", session.User.Email, session.User.Verified, test.wantEmail, test.wantVerified)
			}
			var stored bool
			if err := service.pool.QueryRow(ctx, `select verified from pgbase_meta.users where id=$1`, session.User.ID).Scan(&stored); err != nil || stored != test.wantVerified {
				t.Fatalf("stored verified=%t err=%v", stored, err)
			}
		})
	}
}
