//go:build integration

package userauth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingPasswords counts how many guesses are checked against a real
// account hash and holds each check open, so concurrent attempts overlap.
type countingPasswords struct {
	checked atomic.Int32
	hold    time.Duration
}

func (*countingPasswords) Hash(password string) (string, error) { return "counted:" + password, nil }

func (p *countingPasswords) Verify(hash, password string) error {
	if hash != dummyPasswordHash {
		p.checked.Add(1)
		time.Sleep(p.hold)
	}
	if hash == "counted:"+password {
		return nil
	}
	return ErrInvalidCredentials
}

const concurrentGuesses = 24

func runConcurrently(n int, attempt func(int) error) []error {
	start := make(chan struct{})
	results := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i] = attempt(i)
		}()
	}
	close(start)
	wg.Wait()
	return results
}

func TestConcurrentWrongPasswordsNeverExceedAccountBudget(t *testing.T) {
	passwords := &countingPasswords{hold: 50 * time.Millisecond}
	service := NewService(newUserAuthPool(t), passwords, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	// Admission limits would otherwise refuse most of the burst before it
	// reaches the budget; widen them so the budget alone bounds the guesses.
	service.credentialSlots = make(chan struct{}, concurrentGuesses)
	service.passwordSlots = make(chan struct{}, concurrentGuesses)
	const limit = 3
	if err := service.ConfigureRatePolicy(RatePolicy{AccountMaxFailures: limit, NetworkMaxFailures: 1000, RegistrationNetworkMaxFailures: 1000, Window: 15 * time.Minute, Block: 15 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const email, password = "budget-race@example.test", "correct horse battery staple"
	if _, err := service.Register(ctx, email, password, "budget-race-register"); err != nil {
		t.Fatal(err)
	}
	results := runConcurrently(concurrentGuesses, func(int) error {
		_, err := service.Login(ctx, email, "wrong horse battery staple", "198.51.100.40")
		return err
	})
	for _, err := range results {
		if !errors.Is(err, ErrInvalidCredentials) && !errors.Is(err, ErrRateLimited) {
			t.Fatalf("concurrent wrong password = %v", err)
		}
	}
	if checked := passwords.checked.Load(); checked > limit {
		t.Fatalf("%d concurrent wrong passwords were checked, budget is %d", checked, limit)
	}
	if _, err := service.Login(ctx, email, password, "198.51.100.41"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("correct password after the budget = %v, want rate limited", err)
	}
}

func TestSuccessfulLoginRefundsItsReservation(t *testing.T) {
	passwords := &countingPasswords{}
	service := NewService(newUserAuthPool(t), passwords, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	const limit = 3
	if err := service.ConfigureRatePolicy(RatePolicy{AccountMaxFailures: limit, NetworkMaxFailures: 1000, RegistrationNetworkMaxFailures: 1000, Window: 15 * time.Minute, Block: 15 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const email, password = "budget-refund@example.test", "correct horse battery staple"
	if _, err := service.Register(ctx, email, password, "budget-refund-register"); err != nil {
		t.Fatal(err)
	}
	for range limit - 1 {
		if _, err := service.Login(ctx, email, "wrong horse battery staple", "198.51.100.42"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("wrong password = %v", err)
		}
	}
	// The attempt that would reach the limit blocks while it is in flight;
	// succeeding lifts that block and leaves the earlier failures counted.
	for range 3 {
		if _, err := service.Login(ctx, email, password, "198.51.100.42"); err != nil {
			t.Fatalf("correct password one failure below the limit = %v", err)
		}
	}
	if _, err := service.Login(ctx, email, "wrong horse battery staple", "198.51.100.42"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("failure reaching the limit = %v, want rate limited", err)
	}
}

func TestConcurrentWrongOTPCodesNeverExceedAccountBudget(t *testing.T) {
	service, sender := newEmailActionService(t)
	const limit = 3
	if err := service.ConfigureRatePolicy(RatePolicy{AccountMaxFailures: limit, NetworkMaxFailures: 1000, RegistrationNetworkMaxFailures: 1000, Window: 15 * time.Minute, Block: 15 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	registered, err := service.Register(ctx, "otp-race-budget@example.test", "correct horse battery staple", "otp-race-budget-register")
	if err != nil {
		t.Fatal(err)
	}
	// The code itself allows more guesses than the budget, so only the
	// account budget can stop the burst at three.
	if _, err := service.pool.Exec(ctx, `update pgbase_meta.auth_collection_policies set otp_enabled=true,otp_duration_seconds=300,otp_max_attempts=10 where auth_collection_id=$1`, DefaultCollectionID); err != nil {
		t.Fatal(err)
	}
	issued, err := service.RequestEmailOTP(ctx, registered.User.Email, "otp-race-budget")
	if err != nil {
		t.Fatal(err)
	}
	code := otpFromMessage(t, sender)
	wrong := "000000"
	if code == wrong {
		wrong = "999999"
	}
	results := runConcurrently(concurrentGuesses, func(int) error {
		_, err := service.CompleteEmailOTP(ctx, issued.OTPID, wrong)
		return err
	})
	for _, err := range results {
		if !errors.Is(err, ErrInvalidCredentials) && !errors.Is(err, ErrRateLimited) {
			t.Fatalf("concurrent wrong code = %v", err)
		}
	}
	var attempts int
	if err := service.pool.QueryRow(ctx, `select attempts from pgbase_meta.user_email_otps where id=$1`, issued.OTPID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts > limit {
		t.Fatalf("%d concurrent wrong codes were checked, budget is %d", attempts, limit)
	}
	if _, err := service.CompleteEmailOTP(ctx, issued.OTPID, code); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("correct code after the budget = %v, want rate limited", err)
	}
}
