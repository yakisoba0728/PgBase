//go:build integration

package userauth

import (
	"context"
	"encoding/base32"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"testing"
	"time"
)

func TestEmailOTPIsHashedResentAttemptBoundAndSingleUse(t *testing.T) {
	service, sender := newEmailActionService(t)
	ctx := context.Background()
	registered, err := service.Register(ctx, "otp@example.test", "correct horse battery staple", "otp-register")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `update pgbase_meta.auth_collection_policies set otp_enabled=true,otp_duration_seconds=60,otp_max_attempts=3 where auth_collection_id=$1`, DefaultCollectionID); err != nil {
		t.Fatal(err)
	}
	unknown, err := service.RequestEmailOTP(ctx, "missing@example.test", "otp-missing")
	if err != nil || unknown.OTPID == "" {
		t.Fatalf("unknown request=%#v %v", unknown, err)
	}
	if len(sender.messages) != 0 {
		t.Fatal("unknown address sent email")
	}
	first, err := service.RequestEmailOTP(ctx, registered.User.Email, "otp-first")
	if err != nil {
		t.Fatal(err)
	}
	firstCode := otpFromMessage(t, sender)
	var stored []byte
	if err := service.pool.QueryRow(ctx, `select code_hash from pgbase_meta.user_email_otps where id=$1`, first.OTPID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if string(stored) == firstCode {
		t.Fatal("OTP stored in plaintext")
	}
	second, err := service.RequestEmailOTP(ctx, registered.User.Email, "otp-second")
	if err != nil {
		t.Fatal(err)
	}
	secondCode := otpFromMessage(t, sender)
	wrongCode := "000000"
	if secondCode == wrongCode {
		wrongCode = "999999"
	}
	if _, err := service.CompleteEmailOTP(ctx, first.OTPID, firstCode); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("superseded OTP=%v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := service.CompleteEmailOTP(ctx, second.OTPID, wrongCode); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d=%v", i, err)
		}
	}
	if _, err := service.CompleteEmailOTP(ctx, second.OTPID, secondCode); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("attempt-exhausted OTP=%v", err)
	}
	valid, err := service.RequestEmailOTP(ctx, registered.User.Email, "otp-valid")
	if err != nil {
		t.Fatal(err)
	}
	code := otpFromMessage(t, sender)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := service.CompleteEmailOTP(ctx, valid.OTPID, code); results <- err }()
	}
	wg.Wait()
	close(results)
	success, invalid := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrInvalidCredentials) {
			invalid++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || invalid != 1 {
		t.Fatalf("concurrent redemption success=%d invalid=%d", success, invalid)
	}
	if session, err := service.Login(ctx, registered.User.Email, "correct horse battery staple", "otp-post-login"); err != nil || session.User.Verified {
		t.Fatalf("OTP changed email verification: %#v %v", session.User, err)
	}
}

func TestConfiguredOTPKeyRotationExpiryAndSingleUse(t *testing.T) {
	service, sender := newEmailActionService(t)
	ctx := context.Background()
	registered, err := service.Register(ctx, "keyed-otp@example.test", "correct horse battery staple", "keyed-otp-register")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.pool.Exec(ctx, `update pgbase_meta.auth_collection_policies set otp_enabled=true,otp_duration_seconds=300,otp_max_attempts=3 where auth_collection_id=$1`, DefaultCollectionID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.PutTokenPolicy(ctx, DefaultCollectionID, TokenPolicy{Purpose: "otp", TTLSeconds: 60, KeyVersion: "otp-one", Key: "first-email-otp-policy-key-00000001"}); err != nil {
		t.Fatal(err)
	}
	rotated, err := service.RequestEmailOTP(ctx, registered.User.Email, "keyed-otp-rotate")
	if err != nil {
		t.Fatal(err)
	}
	rotatedCode := otpFromMessage(t, sender)
	if _, err = service.PutTokenPolicy(ctx, DefaultCollectionID, TokenPolicy{Purpose: "otp", TTLSeconds: 60, KeyVersion: "otp-two", Key: "second-email-otp-policy-key-0000001"}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.CompleteEmailOTP(ctx, rotated.OTPID, rotatedCode); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("rotated OTP=%v", err)
	}
	expired, err := service.RequestEmailOTP(ctx, registered.User.Email, "keyed-otp-expire")
	if err != nil {
		t.Fatal(err)
	}
	expiredCode := otpFromMessage(t, sender)
	if _, err = service.pool.Exec(ctx, `update pgbase_meta.user_email_otps set expires_at=CURRENT_TIMESTAMP-interval '1 second' where id=$1`, expired.OTPID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.CompleteEmailOTP(ctx, expired.OTPID, expiredCode); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expired OTP=%v", err)
	}
	valid, err := service.RequestEmailOTP(ctx, registered.User.Email, "keyed-otp-valid")
	if err != nil {
		t.Fatal(err)
	}
	validCode := otpFromMessage(t, sender)
	if remaining := time.Until(valid.ExpiresAt); remaining > 61*time.Second || remaining < 55*time.Second {
		t.Fatalf("configured OTP expiry=%s", remaining)
	}
	if _, err = service.CompleteEmailOTP(ctx, valid.OTPID, validCode); err != nil {
		t.Fatal(err)
	}
	if _, err = service.CompleteEmailOTP(ctx, valid.OTPID, validCode); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("replayed OTP=%v", err)
	}
}

func TestEmailOTPStillRequiresEnrolledMFA(t *testing.T) {
	service, sender := newEmailActionService(t)
	ctx := context.Background()
	registered, err := service.Register(ctx, "otp-mfa@example.test", "correct horse battery staple", "otp-mfa-register")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `update pgbase_meta.auth_collection_policies set otp_enabled=true where auth_collection_id=$1`, DefaultCollectionID); err != nil {
		t.Fatal(err)
	}
	enrollment, err := service.BeginTOTPEnrollment(ctx, registered.User.ID, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	secret := decodeTOTPSecret(t, enrollment.Secret)
	now, err := service.databaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmTOTPEnrollment(ctx, registered.User.ID, totpCode(secret, now.Unix()/30)); err != nil {
		t.Fatal(err)
	}
	request, err := service.RequestEmailOTP(ctx, registered.User.Email, "otp-mfa-request")
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CompleteEmailOTP(ctx, request.OTPID, otpFromMessage(t, sender))
	var required *MFARequiredError
	if !errors.As(err, &required) || required.ChallengeToken == "" {
		t.Fatalf("OTP bypassed MFA: %v", err)
	}
}

func TestEmailOTPConcurrentResendAndDisableRedemption(t *testing.T) {
	service, sender := newEmailActionService(t)
	ctx := context.Background()
	registered, err := service.Register(ctx, "otp-race@example.test", "correct horse battery staple", "otp-race-register")
	if err != nil {
		t.Fatal(err)
	}
	policy := AuthPolicy{PasswordEnabled: true, MinimumPasswordBytes: 12, MFAEnabled: true, OTPEnabled: true, OTPDurationSeconds: 300, OTPMaxAttempts: 5}
	if _, err := service.SetAuthPolicy(ctx, DefaultCollectionID, policy); err != nil {
		t.Fatal(err)
	}
	requests := make(chan EmailOTPRequest, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			request, err := service.RequestEmailOTP(ctx, registered.User.Email, fmt.Sprintf("otp-race-%d", i))
			requests <- request
			errs <- err
		}(i)
	}
	wg.Wait()
	close(requests)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var active int
	if err := service.pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_email_otps where user_id=$1 and consumed_at is null`, registered.User.ID).Scan(&active); err != nil || active != 1 {
		t.Fatalf("active concurrent OTPs=%d %v", active, err)
	}
	latestCode := otpFromMessage(t, sender)
	var activeID string
	if err := service.pool.QueryRow(ctx, `select id::text from pgbase_meta.user_email_otps where user_id=$1 and consumed_at is null`, registered.User.ID).Scan(&activeID); err != nil {
		t.Fatal(err)
	}
	var session Session
	completeDone := make(chan error, 1)
	disableDone := make(chan error, 1)
	go func() {
		var err error
		session, err = service.CompleteEmailOTP(ctx, activeID, latestCode)
		completeDone <- err
	}()
	go func() {
		policy.OTPEnabled = false
		_, err := service.SetAuthPolicy(ctx, DefaultCollectionID, policy)
		disableDone <- err
	}()
	completeErr, disableErr := <-completeDone, <-disableDone
	if disableErr != nil {
		t.Fatal(disableErr)
	}
	if completeErr != nil && !errors.Is(completeErr, ErrInvalidCredentials) {
		t.Fatal(completeErr)
	}
	if session.RawToken != "" {
		if _, err := service.Authenticate(ctx, session.RawToken); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("OTP session survived policy disable: %v", err)
		}
	}
}

var otpPattern = regexp.MustCompile(`\b[0-9]{6}\b`)

func otpFromMessage(t *testing.T, s *capturedSender) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		t.Fatal("no OTP email")
	}
	code := otpPattern.FindString(s.messages[len(s.messages)-1].Text)
	if code == "" {
		t.Fatalf("message=%q", s.messages[len(s.messages)-1].Text)
	}
	return code
}
func decodeTOTPSecret(t *testing.T, value string) []byte {
	t.Helper()
	secret, err := base32NoPadding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

var base32NoPadding = base32.StdEncoding.WithPadding(base32.NoPadding)
