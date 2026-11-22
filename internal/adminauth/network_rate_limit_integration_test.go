//go:build integration

package adminauth

import (
	"context"
	"errors"
	"testing"
	"time"
)

const ipv6RotationPassword = "correct horse battery staple"

func newIPv6RotationService(t *testing.T) *Service {
	t.Helper()
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 9, 10, 5, 6, 7, 0, time.UTC)}
	service := NewService(pool, NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), "setup-token-012345678901234567890", time.Hour, clock)
	if _, err := service.Setup(context.Background(), "setup-token-012345678901234567890", "admin@example.test", ipv6RotationPassword, "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureRatePolicy(RatePolicy{AccountMaxFailures: 50, NetworkMaxFailures: 2, Window: 2 * time.Hour, Block: 3 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	return service
}

func TestLoginNetworkLimitCannotBeEvadedByRotatingIPv6WithinOne64(t *testing.T) {
	service := newIPv6RotationService(t)
	ctx := context.Background()
	for _, address := range []string{"2001:db8:1:1::1", "2001:db8:1:1::2"} {
		if _, err := service.Login(ctx, "admin@example.test", "incorrect horse battery staple", address); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("login from %s error=%v, want invalid credentials", address, err)
		}
	}
	if _, err := service.Login(ctx, "admin@example.test", "incorrect horse battery staple", "2001:db8:1:1:dead:beef:0:3"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("rotated address in the same /64 error=%v, want rate limited", err)
	}
	if _, err := service.Login(ctx, "admin@example.test", ipv6RotationPassword, "2001:db8:1:1::4"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("correct password from the blocked /64 error=%v, want rate limited", err)
	}
	if _, err := service.Login(ctx, "admin@example.test", ipv6RotationPassword, "2001:db8:1:2::1"); err != nil {
		t.Fatalf("login from an independent /64: %v", err)
	}
}

func TestPasswordResetRequestNetworkLimitAggregatesIPv6Within64(t *testing.T) {
	service := newIPv6RotationService(t)
	if err := service.ConfigurePasswordReset(&resetCapture{}, "https://pgbase.example", time.Hour); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, address := range []string{"2001:db8:2:1::1", "2001:db8:2:1::2"} {
		if err := service.RequestPasswordReset(ctx, "unknown@example.test", address); err != nil {
			t.Fatalf("reset request from %s: %v", address, err)
		}
	}
	if err := service.RequestPasswordReset(ctx, "unknown@example.test", "2001:db8:2:1::3"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("rotated reset request error=%v, want rate limited", err)
	}
	if err := service.RequestPasswordReset(ctx, "other@example.test", "2001:db8:2:2::1"); err != nil {
		t.Fatalf("reset request from an independent /64: %v", err)
	}
}

func TestEmailOTPCompletionNetworkLimitAggregatesIPv6Within64(t *testing.T) {
	service := newIPv6RotationService(t)
	if err := service.ConfigureEmailOTP(&resetCapture{}, time.Minute); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, address := range []string{"2001:db8:3:1::1", "2001:db8:3:1::2"} {
		if _, err := service.CompleteEmailOTP(ctx, "00000000-0000-0000-0000-000000000001", "000000", address); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("OTP completion from %s error=%v, want invalid credentials", address, err)
		}
	}
	if _, err := service.CompleteEmailOTP(ctx, "00000000-0000-0000-0000-000000000001", "000000", "2001:db8:3:1::3"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("rotated OTP completion error=%v, want rate limited", err)
	}
	issued, err := service.IssueEmailOTP(ctx, "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteEmailOTP(ctx, issued.ID, issued.Code, "2001:db8:3:2::1"); err != nil {
		t.Fatalf("OTP completion from an independent /64: %v", err)
	}
}

func TestIPAllowlistStillMatchesExactIPv6Address(t *testing.T) {
	service := newIPv6RotationService(t)
	ctx := context.Background()
	if err := service.AllowIP(ctx, "admin@example.test", "2001:db8:4:1::a/128"); err != nil {
		t.Fatal(err)
	}
	// Same /64 as the allowed address, but not the allowed address itself.
	if _, err := service.Login(ctx, "admin@example.test", ipv6RotationPassword, "2001:db8:4:1::b"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("login from a non-allowed address in the allowed /64 error=%v, want invalid credentials", err)
	}
	if _, err := service.Login(ctx, "admin@example.test", ipv6RotationPassword, "2001:db8:4:1::a"); err != nil {
		t.Fatalf("login from the exact allowed address: %v", err)
	}
}
