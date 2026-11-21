//go:build integration

package userauth

import (
	"context"
	"encoding/base32"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

const (
	preclaimAttackerPassword = "attacker horse battery staple"
	preclaimOwnerPassword    = "owner reclaimed horse battery staple"
	preclaimRedirect         = "https://app.example/oauth/callback"
)

// preclaimAccount registers email with the attacker's password, links the
// attacker's OAuth identity and enrolls TOTP, returning the account ID.
func preclaimAccount(t *testing.T, service *Service, email string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	if err := service.ConfigureOAuth(map[string]OAuthProvider{"local": realmOAuthProvider{identity: OAuthIdentity{Subject: "attacker-subject-" + email, Email: "attacker@evil.test", EmailVerified: true}}}, []string{preclaimRedirect}, time.Minute); err != nil {
		t.Fatal(err)
	}
	registered, err := service.Register(ctx, email, preclaimAttackerPassword, "198.51.100.10")
	if err != nil {
		t.Fatal(err)
	}
	link, err := service.BeginOAuthLink(ctx, registered.User.ID, preclaimAttackerPassword, "local", preclaimRedirect)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteOAuth(ctx, "local", link.State, "code"); err != nil {
		t.Fatalf("attacker OAuth link: %v", err)
	}
	enrollment, err := service.BeginTOTPEnrollment(ctx, registered.User.ID, preclaimAttackerPassword)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrollment.Secret)
	if err != nil {
		t.Fatal(err)
	}
	now, err := service.databaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmTOTPEnrollment(ctx, registered.User.ID, totpCode(secret, now.Unix()/30)); err != nil {
		t.Fatal(err)
	}
	return registered.User.ID
}

func ownerResetsPassword(t *testing.T, service *Service, sender *capturedSender, email string) {
	t.Helper()
	ctx := context.Background()
	if err := service.RequestPasswordReset(ctx, email, "203.0.113.20"); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfirmPasswordReset(ctx, sender.token(t), preclaimOwnerPassword); err != nil {
		t.Fatalf("owner password reset: %v", err)
	}
}

func accountFactorCounts(t *testing.T, service *Service, userID uuid.UUID) (identities, factors, recoveryCodes int) {
	t.Helper()
	if err := service.pool.QueryRow(context.Background(), `select
		(select count(*) from pgbase_meta.user_oauth_identities where user_id=$1),
		(select count(*) from pgbase_meta.user_totp_factors where user_id=$1),
		(select count(*) from pgbase_meta.user_totp_recovery_codes where user_id=$1)`, userID).Scan(&identities, &factors, &recoveryCodes); err != nil {
		t.Fatal(err)
	}
	return identities, factors, recoveryCodes
}

// attackerOAuthLogin returns the account the attacker's OAuth identity signs
// into, or uuid.Nil when the login does not reach an account.
func attackerOAuthLogin(t *testing.T, service *Service) (uuid.UUID, error) {
	t.Helper()
	ctx := context.Background()
	begin, err := service.BeginOAuth(ctx, "local", preclaimRedirect, "198.51.100.11")
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.CompleteOAuth(ctx, "local", begin.State, "code")
	var required *MFARequiredError
	if errors.As(err, &required) {
		var userID uuid.UUID
		if scanErr := service.pool.QueryRow(ctx, `select user_id from pgbase_meta.user_mfa_challenges where token_hash=$1`, service.mfaChallengeHash(required.ChallengeToken)).Scan(&userID); scanErr != nil {
			t.Fatal(scanErr)
		}
		return userID, err
	}
	if err != nil {
		return uuid.Nil, err
	}
	return session.User.ID, nil
}

func TestPasswordResetOfUnverifiedAccountRemovesPreclaimedOAuthAndTOTP(t *testing.T) {
	service, sender := newEmailActionService(t)
	ctx := context.Background()
	const email = "preclaimed@example.test"
	victimAccount := preclaimAccount(t, service, email)
	if identities, factors, codes := accountFactorCounts(t, service, victimAccount); identities != 1 || factors != 1 || codes == 0 {
		t.Fatalf("preclaim setup identities=%d factors=%d codes=%d", identities, factors, codes)
	}
	if _, err := service.Register(ctx, email, preclaimOwnerPassword, "203.0.113.20"); !errors.Is(err, ErrConflict) {
		t.Fatalf("owner registration error=%v, want conflict", err)
	}

	ownerResetsPassword(t, service, sender, email)

	if identities, factors, codes := accountFactorCounts(t, service, victimAccount); identities != 0 || factors != 0 || codes != 0 {
		t.Fatalf("after reset identities=%d factors=%d codes=%d, want all removed", identities, factors, codes)
	}
	if reached, err := attackerOAuthLogin(t, service); reached == victimAccount {
		t.Fatalf("attacker OAuth login still reaches the reclaimed account (err=%v)", err)
	}
	if _, err := service.Login(ctx, email, preclaimAttackerPassword, "198.51.100.12"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("attacker password login error=%v, want invalid credentials", err)
	}
	session, err := service.Login(ctx, email, preclaimOwnerPassword, "203.0.113.21")
	if err != nil {
		t.Fatalf("owner login after reset without MFA: %v", err)
	}
	if session.User.ID != victimAccount || session.User.Verified {
		t.Fatalf("owner session user=%s verified=%t", session.User.ID, session.User.Verified)
	}
}

func TestPasswordResetOfUnverifiedOAuthOnlyAccountLeavesPasswordSignIn(t *testing.T) {
	service, sender := newEmailActionService(t)
	ctx := context.Background()
	const email = "oauth-only-unverified@example.test"
	service.ConfigureDefaultRegistration(true)
	service.ConfigureOAuthRegistration(true)
	if err := service.ConfigureOAuth(map[string]OAuthProvider{"local": realmOAuthProvider{identity: OAuthIdentity{Subject: "unverified-oidc-subject", Email: email, EmailVerified: false}}}, []string{preclaimRedirect}, time.Minute); err != nil {
		t.Fatal(err)
	}
	begin, err := service.BeginOAuth(ctx, "local", preclaimRedirect, "198.51.100.30")
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CompleteOAuth(ctx, "local", begin.State, "code")
	if err != nil {
		t.Fatalf("OAuth registration: %v", err)
	}
	if created.User.Verified {
		t.Fatal("unverified provider email created a verified account")
	}

	ownerResetsPassword(t, service, sender, email)

	if identities, _, _ := accountFactorCounts(t, service, created.User.ID); identities != 0 {
		t.Fatalf("identities after reset=%d, want 0", identities)
	}
	session, err := service.Login(ctx, email, preclaimOwnerPassword, "203.0.113.23")
	if err != nil || session.User.ID != created.User.ID {
		t.Fatalf("password login after reclaiming an OAuth-only account: user=%s err=%v", session.User.ID, err)
	}
}

func TestPasswordResetOfVerifiedAccountKeepsOAuthAndTOTP(t *testing.T) {
	service, sender := newEmailActionService(t)
	ctx := context.Background()
	const email = "verified-owner@example.test"
	account := preclaimAccount(t, service, email)
	if err := service.RequestVerification(ctx, account); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfirmVerification(ctx, sender.token(t)); err != nil {
		t.Fatal(err)
	}
	_, _, codesBefore := accountFactorCounts(t, service, account)

	ownerResetsPassword(t, service, sender, email)

	if identities, factors, codes := accountFactorCounts(t, service, account); identities != 1 || factors != 1 || codes != codesBefore {
		t.Fatalf("verified reset identities=%d factors=%d codes=%d (before %d), want preserved", identities, factors, codes, codesBefore)
	}
	reached, err := attackerOAuthLogin(t, service)
	var required *MFARequiredError
	if reached != account || !errors.As(err, &required) {
		t.Fatalf("linked OAuth login reached=%s err=%v, want MFA challenge for the same account", reached, err)
	}
	if _, err := service.Login(ctx, email, preclaimOwnerPassword, "203.0.113.22"); !errors.As(err, &required) {
		t.Fatalf("verified account password login error=%v, want MFA challenge", err)
	}
}
