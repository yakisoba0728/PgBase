//go:build integration

package adminauth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func enableAccountTOTP(t *testing.T, fixture accountManagementFixture) []byte {
	t.Helper()
	if err := fixture.service.EnableTOTP(t.Context(), fixture.first.Email, accountTestTOTPSecret); err != nil {
		t.Fatal(err)
	}
	secret, err := decodeTOTPSecret(accountTestTOTPSecret)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func assertAdministratorTOTPCounter(t *testing.T, fixture accountManagementFixture, want *int64) {
	t.Helper()
	var counter *int64
	if err := fixture.service.pool.QueryRow(t.Context(), `select last_counter from pgbase_meta.admin_totp where administrator_id=$1`, fixture.first.ID).Scan(&counter); err != nil {
		t.Fatal(err)
	}
	if (counter == nil) != (want == nil) || (counter != nil && *counter != *want) {
		t.Fatalf("stored TOTP counter=%v, want=%v", counter, want)
	}
}

func TestAdministratorTOTPCountersPersistAcrossServiceInstances(t *testing.T) {
	fixture := newAccountManagementFixture(t)
	secret := enableAccountTOTP(t, fixture)
	s, ctx := fixture.service, t.Context()
	code := testTOTP(secret, fixture.clock.Now())
	if _, err := s.LoginWithOTP(ctx, fixture.first.Email, accountTestPassword, "192.0.2.1", code); err != nil {
		t.Fatal(err)
	}
	counter := fixture.clock.Now().Unix() / 30
	assertAdministratorTOTPCounter(t, fixture, &counter)
	other := NewService(s.pool, NewArgon2idPasswords(), s.tokenSecret, "", time.Hour, fixture.clock)
	if _, err := other.LoginWithOTP(ctx, fixture.first.Email, accountTestPassword, "192.0.2.2", code); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("consumed code error=%v", err)
	}
	fixture.clock.Add(30 * time.Second)
	if _, err := other.LoginWithOTP(ctx, fixture.first.Email, accountTestPassword, "192.0.2.2", testTOTP(secret, fixture.clock.Now())); err != nil {
		t.Fatalf("next unused step rejected: %v", err)
	}
	counter++
	assertAdministratorTOTPCounter(t, fixture, &counter)
}

func TestAdministratorTOTPNetworkRejectionDoesNotConsumeCounter(t *testing.T) {
	fixture := newAccountManagementFixture(t)
	secret := enableAccountTOTP(t, fixture)
	s, ctx := fixture.service, t.Context()
	if err := s.AllowIP(ctx, fixture.first.Email, "192.0.2.0/24"); err != nil {
		t.Fatal(err)
	}
	code := testTOTP(secret, fixture.clock.Now())
	if _, err := s.LoginWithOTP(ctx, fixture.first.Email, accountTestPassword, "198.51.100.1", code); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disallowed network error=%v", err)
	}
	assertAdministratorTOTPCounter(t, fixture, nil)
	if _, err := s.LoginWithOTP(ctx, fixture.first.Email, accountTestPassword, "192.0.2.1", code); err != nil {
		t.Fatalf("admitted network rejected unused code: %v", err)
	}
}

func TestAdministratorTOTPConsumptionRollsBackWithSessionFailure(t *testing.T) {
	fixture := newAccountManagementFixture(t)
	secret := enableAccountTOTP(t, fixture)
	s, ctx := fixture.service, t.Context()
	if _, err := s.pool.Exec(ctx, `create function pgbase_meta.reject_totp_test_session() returns trigger language plpgsql as $$
		begin raise exception 'test session write failure'; end $$;
		create trigger reject_totp_test_session before insert on pgbase_meta.admin_sessions
		for each row execute function pgbase_meta.reject_totp_test_session()`); err != nil {
		t.Fatal(err)
	}
	code := testTOTP(secret, fixture.clock.Now())
	if _, err := s.LoginWithOTP(ctx, fixture.first.Email, accountTestPassword, "192.0.2.1", code); err == nil {
		t.Fatal("session fault did not reject login")
	}
	assertAdministratorTOTPCounter(t, fixture, nil)
	var sessions int
	if err := s.pool.QueryRow(ctx, `select count(*) from pgbase_meta.admin_sessions where admin_id=$1`, fixture.first.ID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("sessions after rollback=%d err=%v", sessions, err)
	}
	if _, err := s.pool.Exec(ctx, `drop trigger reject_totp_test_session on pgbase_meta.admin_sessions;
		drop function pgbase_meta.reject_totp_test_session()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoginWithOTP(ctx, fixture.first.Email, accountTestPassword, "192.0.2.1", code); err != nil {
		t.Fatalf("unused counter after rollback rejected: %v", err)
	}
}

func TestAdministratorTOTPConcurrentLoginsConsumeOneCounter(t *testing.T) {
	fixture := newAccountManagementFixture(t)
	secret := enableAccountTOTP(t, fixture)
	s := fixture.service
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	barrier, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackCredentialBarrier(barrier)
	if _, err := barrier.Exec(ctx, `select id from pgbase_meta.admins where id=$1 for update`, fixture.first.ID); err != nil {
		t.Fatal(err)
	}
	other := NewService(s.pool, NewArgon2idPasswords(), s.tokenSecret, "", time.Hour, fixture.clock)
	code := testTOTP(secret, fixture.clock.Now())
	results := make(chan error, 2)
	blockedPID := 0
	for _, call := range []struct {
		service *Service
		address string
	}{{s, "192.0.2.1"}, {other, "192.0.2.2"}} {
		go func() {
			_, err := call.service.LoginWithOTP(ctx, fixture.first.Email, accountTestPassword, call.address, code)
			results <- err
		}()
		// Finish each request's fail-fast account-limiter read before launching
		// the next. Both logins must contend at the held account row, rather
		// than one being rejected by unrelated credential admission.
		blockedPID = waitCredentialLock(t, ctx, s.pool, blockedPID)
	}
	if err := barrier.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	succeeded, rejected := 0, 0
	for range 2 {
		select {
		case err := <-results:
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrInvalidCredentials):
				rejected++
			default:
				t.Fatalf("concurrent login error=%v", err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("succeeded=%d rejected=%d", succeeded, rejected)
	}
	var sessions int
	if err := s.pool.QueryRow(ctx, `select count(*) from pgbase_meta.admin_sessions where admin_id=$1`, fixture.first.ID).Scan(&sessions); err != nil || sessions != 1 {
		t.Fatalf("sessions=%d err=%v", sessions, err)
	}
}

func TestAdministratorTOTPReenrollingTheSameSecretKeepsConsumedCodes(t *testing.T) {
	fixture := newAccountManagementFixture(t)
	secret := enableAccountTOTP(t, fixture)
	s, ctx := fixture.service, t.Context()
	code := testTOTP(secret, fixture.clock.Now())
	if _, err := s.LoginWithOTP(ctx, fixture.first.Email, accountTestPassword, "192.0.2.1", code); err != nil {
		t.Fatal(err)
	}
	counter := fixture.clock.Now().Unix() / 30
	if err := s.EnableTOTP(ctx, fixture.first.Email, accountTestTOTPSecret); err != nil {
		t.Fatal(err)
	}
	assertAdministratorTOTPCounter(t, fixture, &counter)
	if _, err := s.LoginWithOTP(ctx, fixture.first.Email, accountTestPassword, "192.0.2.1", code); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("consumed code accepted after same-secret re-enrollment: %v", err)
	}
	fixture.clock.Add(30 * time.Second)
	if _, err := s.LoginWithOTP(ctx, fixture.first.Email, accountTestPassword, "192.0.2.1", testTOTP(secret, fixture.clock.Now())); err != nil {
		t.Fatalf("next unused step rejected: %v", err)
	}
}

func TestAdministratorTOTPReplacementStartsANewCounterSequence(t *testing.T) {
	fixture := newAccountManagementFixture(t)
	secret := enableAccountTOTP(t, fixture)
	s, ctx := fixture.service, t.Context()
	issued, err := s.LoginWithOTP(ctx, fixture.first.Email, accountTestPassword, "192.0.2.1", testTOTP(secret, fixture.clock.Now()))
	if err != nil {
		t.Fatal(err)
	}
	const replacement = "KRUGS4ZANFZSAYJA"
	if err := s.EnableTOTP(ctx, fixture.first.Email, replacement); err != nil {
		t.Fatal(err)
	}
	assertAdministratorTOTPCounter(t, fixture, nil)
	if _, err := s.Authenticate(ctx, issued.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("session survived factor replacement: %v", err)
	}
	secret, err = decodeTOTPSecret(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoginWithOTP(ctx, fixture.first.Email, accountTestPassword, "192.0.2.1", testTOTP(secret, fixture.clock.Now())); err != nil {
		t.Fatalf("replacement factor rejected: %v", err)
	}
	if err := s.DisableTOTP(ctx, fixture.first.Email); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login(ctx, fixture.first.Email, accountTestPassword, "192.0.2.1"); err != nil {
		t.Fatalf("disabled factor retained a login requirement: %v", err)
	}
}
