//go:build integration

package adminauth

import (
	"errors"
	"testing"
	"time"
)

func issueAccountEmailOTP(t *testing.T, fixture accountManagementFixture) EmailOTP {
	t.Helper()
	if err := fixture.service.ConfigureEmailOTP(&resetCapture{}, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	issued, err := fixture.service.IssueEmailOTP(t.Context(), fixture.first.Email)
	if err != nil {
		t.Fatal(err)
	}
	return issued
}

func assertEmailOTPConsumed(t *testing.T, fixture accountManagementFixture, issued EmailOTP, want bool) {
	t.Helper()
	var consumed bool
	if err := fixture.service.pool.QueryRow(t.Context(), `select consumed_at is not null from pgbase_meta.admin_email_otps where id=$1`, issued.ID).Scan(&consumed); err != nil {
		t.Fatal(err)
	}
	if consumed != want {
		t.Fatalf("email OTP consumed=%t, want %t", consumed, want)
	}
}

func assertAccountSessionCount(t *testing.T, fixture accountManagementFixture, want int) {
	t.Helper()
	var sessions int
	if err := fixture.service.pool.QueryRow(t.Context(), `select count(*) from pgbase_meta.admin_sessions where admin_id=$1`, fixture.first.ID).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != want {
		t.Fatalf("sessions=%d, want %d", sessions, want)
	}
}

// The email code alone must not bypass an enrolled second factor.
func TestAdministratorEmailOTPRequiresTOTPWhenEnabled(t *testing.T) {
	fixture := newAccountManagementFixture(t)
	secret := enableAccountTOTP(t, fixture)
	s, ctx := fixture.service, t.Context()
	issued := issueAccountEmailOTP(t, fixture)

	if _, err := s.CompleteEmailOTP(ctx, issued.ID, issued.Code, "192.0.2.1"); !errors.Is(err, ErrOTPRequired) {
		t.Fatalf("email OTP without TOTP error=%v, want ErrOTPRequired", err)
	}
	// The step change leaves the email code usable and records no failure.
	assertEmailOTPConsumed(t, fixture, issued, false)
	assertAccountSessionCount(t, fixture, 0)
	assertAdministratorTOTPCounter(t, fixture, nil)
	var failures int
	accountHash := rateLimitSubjectHash(s.tokenSecret, "email-otp-complete", "account", fixture.first.Email)
	if err := s.pool.QueryRow(ctx, `select count(*) from pgbase_meta.admin_rate_limits where subject_hash=$1`, accountHash).Scan(&failures); err != nil || failures != 0 {
		t.Fatalf("otp_required recorded a failure rows=%d err=%v", failures, err)
	}

	// A wrong email code is still rejected first; it never reveals the TOTP step.
	if _, err := s.CompleteEmailOTPWithTOTP(ctx, issued.ID, wrongCode(issued.Code), "192.0.2.1", testTOTP(secret, fixture.clock.Now())); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong email code error=%v", err)
	}
	if _, err := s.CompleteEmailOTPWithTOTP(ctx, issued.ID, issued.Code, "192.0.2.1", wrongCode(testTOTP(secret, fixture.clock.Now()))); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong TOTP error=%v", err)
	}
	assertEmailOTPConsumed(t, fixture, issued, false)
	assertAdministratorTOTPCounter(t, fixture, nil)

	code := testTOTP(secret, fixture.clock.Now())
	session, err := s.CompleteEmailOTPWithTOTP(ctx, issued.ID, issued.Code, "192.0.2.1", code)
	if err != nil || session.RawToken == "" {
		t.Fatalf("email OTP with TOTP session=%#v err=%v", session, err)
	}
	counter := fixture.clock.Now().Unix() / 30
	assertAdministratorTOTPCounter(t, fixture, &counter)
	assertEmailOTPConsumed(t, fixture, issued, true)

	// The TOTP counter is single-use across both login paths.
	if _, err := s.LoginWithOTP(ctx, fixture.first.Email, accountTestPassword, "192.0.2.1", code); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("password login replayed an email-OTP TOTP code: %v", err)
	}
	fixture.clock.Add(30 * time.Second)
	code = testTOTP(secret, fixture.clock.Now())
	if _, err := s.LoginWithOTP(ctx, fixture.first.Email, accountTestPassword, "192.0.2.1", code); err != nil {
		t.Fatal(err)
	}
	next, err := s.IssueEmailOTP(ctx, fixture.first.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteEmailOTPWithTOTP(ctx, next.ID, next.Code, "192.0.2.1", code); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("email OTP replayed a password-login TOTP code: %v", err)
	}
	assertEmailOTPConsumed(t, fixture, next, false)

	// Disabling TOTP restores email-OTP-only completion.
	if err := s.DisableTOTP(ctx, fixture.first.Email); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteEmailOTP(ctx, next.ID, next.Code, "192.0.2.1"); err != nil {
		t.Fatalf("email OTP after disabling TOTP: %v", err)
	}
}

func TestAdministratorEmailOTPWithTOTPNetworkRejectionConsumesNothing(t *testing.T) {
	fixture := newAccountManagementFixture(t)
	secret := enableAccountTOTP(t, fixture)
	s, ctx := fixture.service, t.Context()
	if err := s.AllowIP(ctx, fixture.first.Email, "192.0.2.0/24"); err != nil {
		t.Fatal(err)
	}
	issued := issueAccountEmailOTP(t, fixture)
	code := testTOTP(secret, fixture.clock.Now())
	if _, err := s.CompleteEmailOTPWithTOTP(ctx, issued.ID, issued.Code, "198.51.100.1", code); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disallowed network error=%v", err)
	}
	assertAdministratorTOTPCounter(t, fixture, nil)
	assertEmailOTPConsumed(t, fixture, issued, false)
	if _, err := s.CompleteEmailOTPWithTOTP(ctx, issued.ID, issued.Code, "192.0.2.1", code); err != nil {
		t.Fatalf("admitted network rejected unused codes: %v", err)
	}
}

func wrongCode(code string) string {
	if code == "000000" {
		return "000001"
	}
	return "000000"
}
