//go:build integration

package userauth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEmailOTPFailuresShareOneAccountBudgetAcrossCodes(t *testing.T) {
	service, sender := newEmailActionService(t)
	ctx := context.Background()
	if err := service.ConfigureRatePolicy(RatePolicy{AccountMaxFailures: 5, NetworkMaxFailures: 100, RegistrationNetworkMaxFailures: 100, Window: 15 * time.Minute, Block: 15 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	target, err := service.Register(ctx, "otp-budget@example.test", "correct horse battery staple", "otp-budget-register")
	if err != nil {
		t.Fatal(err)
	}
	bystander, err := service.Register(ctx, "otp-bystander@example.test", "correct horse battery staple", "otp-bystander-register")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `update pgbase_meta.auth_collection_policies set otp_enabled=true,otp_duration_seconds=300,otp_max_attempts=3 where auth_collection_id=$1`, DefaultCollectionID); err != nil {
		t.Fatal(err)
	}
	request := func(email, network string) (string, string) {
		t.Helper()
		issued, err := service.RequestEmailOTP(ctx, email, network)
		if err != nil {
			t.Fatal(err)
		}
		return issued.OTPID, otpFromMessage(t, sender)
	}
	wrong := func(code string) string {
		if code == "000000" {
			return "999999"
		}
		return "000000"
	}

	// Three failures exhaust the first code's own attempts.
	first, firstCode := request(target.User.Email, "otp-budget-1")
	for i := range 3 {
		if _, err := service.CompleteEmailOTP(ctx, first, wrong(firstCode)); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("first code attempt %d = %v", i, err)
		}
	}
	// A success in between does not refund the account budget, matching the
	// password login limiter.
	second, secondCode := request(target.User.Email, "otp-budget-2")
	if _, err := service.CompleteEmailOTP(ctx, second, secondCode); err != nil {
		t.Fatalf("second code = %v", err)
	}
	// A fresh code restores the per-code attempts but not the account budget:
	// the fifth wrong code across codes blocks the account.
	third, thirdCode := request(target.User.Email, "otp-budget-3")
	if _, err := service.CompleteEmailOTP(ctx, third, wrong(thirdCode)); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("fourth failure = %v", err)
	}
	if _, err := service.CompleteEmailOTP(ctx, third, wrong(thirdCode)); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("fifth failure = %v, want rate limited", err)
	}
	if _, err := service.CompleteEmailOTP(ctx, third, thirdCode); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("correct code while blocked = %v, want rate limited", err)
	}
	fourth, fourthCode := request(target.User.Email, "otp-budget-4")
	if _, err := service.CompleteEmailOTP(ctx, fourth, fourthCode); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("fresh code while blocked = %v, want rate limited", err)
	}

	// The budget is per account: another account of the collection still signs in.
	other, otherCode := request(bystander.User.Email, "otp-budget-other")
	if _, err := service.CompleteEmailOTP(ctx, other, otherCode); err != nil {
		t.Fatalf("other account = %v", err)
	}

	// Once the block ends a correct code is accepted again.
	if _, err := service.pool.Exec(ctx, `update pgbase_meta.user_auth_rate_limits set blocked_until=CURRENT_TIMESTAMP-interval '1 second',window_started_at=CURRENT_TIMESTAMP-interval '1 hour' where subject_hash=$1`, service.emailOTPBudget(DefaultCollectionID, service.emailOTPAccountTag(DefaultCollectionID, target.User.Email))[0].hash); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteEmailOTP(ctx, fourth, fourthCode); err != nil {
		t.Fatalf("correct code after the block = %v", err)
	}
}
