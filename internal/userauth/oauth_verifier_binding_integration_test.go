//go:build integration

package userauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

type verifierRecordingProvider struct {
	realmOAuthProvider
	exchanges *[]string
}

func (p verifierRecordingProvider) Exchange(ctx context.Context, code, verifier, redirect string) (OAuthIdentity, error) {
	*p.exchanges = append(*p.exchanges, verifier)
	return p.realmOAuthProvider.Exchange(ctx, code, verifier, redirect)
}

func TestOAuthCompletionRequiresBoundVerifier(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	var exchanges []string
	provider := verifierRecordingProvider{realmOAuthProvider{OAuthIdentity{Subject: "bound-subject", Email: "bound-oauth@example.test", EmailVerified: true}}, &exchanges}
	redirect := "https://app.example/oauth/callback"
	if err := service.ConfigureOAuth(map[string]OAuthProvider{"local": provider}, []string{redirect}, time.Minute); err != nil {
		t.Fatal(err)
	}
	service.ConfigureOAuthRegistration(true)

	bound, err := service.BeginOAuth(WithOAuthVerifierBinding(ctx), "local", redirect, "oauth-bound-network")
	if err != nil {
		t.Fatal(err)
	}
	if bound.CodeVerifier == "" || bound.CodeChallengeMethod != "S256" {
		t.Fatalf("start = %#v", bound)
	}
	for name, attempt := range map[string]context.Context{
		"missing verifier": ctx,
		"empty verifier":   WithOAuthCodeVerifier(ctx, ""),
		"wrong verifier":   WithOAuthCodeVerifier(ctx, bound.CodeVerifier+"x"),
	} {
		if _, err := service.CompleteOAuth(attempt, "local", bound.State, "code"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("%s = %v, want invalid credentials", name, err)
		}
	}
	if len(exchanges) != 0 {
		t.Fatalf("a rejected completion spent the provider code: %d exchanges", len(exchanges))
	}
	session, err := service.CompleteOAuth(WithOAuthCodeVerifier(ctx, bound.CodeVerifier), "local", bound.State, "code")
	if err != nil {
		t.Fatalf("bound completion with the start verifier = %v", err)
	}
	if session.RawToken == "" || len(exchanges) != 1 || exchanges[0] != bound.CodeVerifier {
		t.Fatalf("session=%q exchanges=%q", session.RawToken, exchanges)
	}
	if _, err := service.CompleteOAuth(WithOAuthCodeVerifier(ctx, bound.CodeVerifier), "local", bound.State, "code"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("replayed bound completion = %v", err)
	}

	// Without the opt-in the documented state+code completion still works, but
	// a presented verifier is always checked.
	unbound, err := service.BeginOAuth(ctx, "local", redirect, "oauth-unbound-network")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteOAuth(WithOAuthCodeVerifier(ctx, "wrong"), "local", unbound.State, "code"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unbound completion with a wrong verifier = %v", err)
	}
	if _, err := service.CompleteOAuth(ctx, "local", unbound.State, "code"); err != nil {
		t.Fatalf("unbound completion without a verifier = %v", err)
	}
}
