//go:build integration

package adminauth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAdministratorEmailOTPIsOneUseExpiresAndEnforcesIPPolicy(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 9, 10, 3, 4, 5, 0, time.UTC)}
	service := NewService(pool, NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), "setup-token-012345678901234567890", time.Hour, clock)
	mail := &resetCapture{}
	if err := service.ConfigureEmailOTP(mail, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Setup(context.Background(), "setup-token-012345678901234567890", "admin@example.test", "correct horse battery staple", "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	if err := service.AllowIP(context.Background(), "admin@example.test", "192.0.2.0/24"); err != nil {
		t.Fatal(err)
	}
	issued, err := service.IssueEmailOTP(context.Background(), "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if len(mail.messages) != 1 || mail.messages[0].Purpose != "admin_email_otp" {
		t.Fatalf("mail=%#v", mail.messages)
	}
	if _, err := service.CompleteEmailOTP(context.Background(), issued.ID, issued.Code, "198.51.100.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("restricted IP error=%v", err)
	}
	session, err := service.CompleteEmailOTP(context.Background(), issued.ID, issued.Code, "192.0.2.2")
	if err != nil || session.RawToken == "" {
		t.Fatalf("complete session=%#v err=%v", session, err)
	}
	if _, err := service.CompleteEmailOTP(context.Background(), issued.ID, issued.Code, "192.0.2.2"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("reused OTP error=%v", err)
	}
	expiring, err := service.IssueEmailOTP(context.Background(), "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(2 * time.Minute)
	if _, err := service.CompleteEmailOTP(context.Background(), expiring.ID, expiring.Code, "192.0.2.2"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expired OTP error=%v", err)
	}
}

func TestEmailOTPUsesLiveRatePolicy(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 9, 10, 3, 4, 5, 0, time.UTC)}
	secret := []byte("0123456789abcdef0123456789abcdef")
	service := NewService(pool, NewArgon2idPasswords(), secret, "setup-token-012345678901234567890", time.Hour, clock)
	if err := service.ConfigureEmailOTP(&resetCapture{}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Setup(context.Background(), "setup-token-012345678901234567890", "admin@example.test", "correct horse battery staple", "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	policy := RatePolicy{AccountMaxFailures: 1, NetworkMaxFailures: 2, Window: 2 * time.Hour, Block: 3 * time.Hour}
	if err := service.ConfigureRatePolicy(policy); err != nil {
		t.Fatal(err)
	}
	issued, err := service.IssueEmailOTP(context.Background(), "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.IssueEmailOTP(context.Background(), "admin@example.test"); err != nil {
		t.Fatal(err)
	}
	var blockedUntil time.Time
	issueHash := rateLimitSubjectHash(secret, "email-otp-issue", "account", "admin@example.test")
	if err := pool.QueryRow(context.Background(), `select blocked_until from pgbase_meta.admin_rate_limits where subject_hash=$1`, issueHash).Scan(&blockedUntil); err != nil {
		t.Fatal(err)
	}
	if !blockedUntil.Equal(clock.now.Add(policy.Block)) {
		t.Fatalf("issue block=%s want %s", blockedUntil, clock.now.Add(policy.Block))
	}
	if _, err := service.IssueEmailOTP(context.Background(), "admin@example.test"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("issue policy did not block subsequent request: %v", err)
	}
	const network = "192.0.2.88"
	if _, err := service.CompleteEmailOTP(context.Background(), issued.ID, "000000", network); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("incorrect OTP error=%v", err)
	}
	if _, err := service.CompleteEmailOTP(context.Background(), issued.ID, "000000", network); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("second incorrect OTP error=%v", err)
	}
	networkHash := rateLimitSubjectHash(secret, "email-otp-complete", "network", network)
	if err := pool.QueryRow(context.Background(), `select blocked_until from pgbase_meta.admin_rate_limits where subject_hash=$1`, networkHash).Scan(&blockedUntil); err != nil {
		t.Fatal(err)
	}
	if !blockedUntil.Equal(clock.now.Add(policy.Block)) {
		t.Fatalf("complete block=%s want %s", blockedUntil, clock.now.Add(policy.Block))
	}
	if _, err := service.CompleteEmailOTP(context.Background(), issued.ID, "000000", network); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("complete policy did not block subsequent request: %v", err)
	}
}
