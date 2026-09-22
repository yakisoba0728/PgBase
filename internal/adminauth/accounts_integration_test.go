//go:build integration

package adminauth

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

const accountTestPassword = "administrator account original password"
const accountTestReplacementPassword = "administrator account replacement password"
const accountTestTOTPSecret = "JBSWY3DPEHPK3PXP"

type accountManagementFixture struct {
	service       *Service
	clock         *fakeClock
	first, second Administrator
}

func newAccountManagementFixture(t *testing.T) accountManagementFixture {
	t.Helper()
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 9, 22, 1, 2, 3, 0, time.UTC)}
	passwords := NewArgon2idPasswords()
	service := NewService(pool, passwords, []byte("account-management-test-secret-32-bytes"), "", time.Hour, clock)
	hash, err := passwords.Hash(accountTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	create := func(email string) Administrator {
		account, err := NewStore(pool).CreateAdministrator(t.Context(), Administrator{ID: uuid.New(), Email: email, Active: true, CreatedAt: clock.Now(), UpdatedAt: clock.Now()}, hash)
		if err != nil {
			t.Fatal(err)
		}
		return account
	}
	return accountManagementFixture{service: service, clock: clock, first: create("first-account@example.test"), second: create("second-account@example.test")}
}

func TestAdministratorAccountsConcurrentMutualDeactivationRetainsOneActiveActor(t *testing.T) {
	fixture := newAccountManagementFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	blocker, err := fixture.service.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Release()
	if _, err = blocker.Exec(ctx, "select pg_advisory_lock($1)", administratorLifecycleLock); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = blocker.Exec(context.Background(), "select pg_advisory_unlock_all()") }()
	other := NewService(fixture.service.pool, NewArgon2idPasswords(), fixture.service.tokenSecret, "", time.Hour, fixture.clock)
	type result struct {
		actor, target uuid.UUID
		err           error
	}
	results := make(chan result, 2)
	for _, call := range []struct {
		service       *Service
		actor, target uuid.UUID
	}{
		{fixture.service, fixture.first.ID, fixture.second.ID},
		{other, fixture.second.ID, fixture.first.ID},
	} {
		go func() {
			_, err := call.service.SetAdministratorActive(ctx, call.actor, call.target, false)
			results <- result{call.actor, call.target, err}
		}()
	}
	// Force both independent service instances to wait before releasing either;
	// a scheduler that happened to serialize the goroutines cannot mask this race.
	for {
		var waiting int
		if err := fixture.service.pool.QueryRow(ctx, `select count(*) from pg_stat_activity where datname=current_database() and wait_event_type='Lock' and wait_event='advisory'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 2 {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("both lifecycle operations did not reach the lock")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err = blocker.Exec(ctx, "select pg_advisory_unlock_all()"); err != nil {
		t.Fatal(err)
	}
	succeeded, rejected := 0, 0
	var winner uuid.UUID
	for range 2 {
		select {
		case outcome := <-results:
			if outcome.err == nil {
				succeeded++
				winner = outcome.actor
			} else if errors.Is(outcome.err, ErrInvalidCredentials) {
				rejected++
			} else {
				t.Fatalf("mutual deactivate failed unexpectedly: %v", outcome.err)
			}
		case <-ctx.Done():
			t.Fatal("mutual deactivation did not complete")
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("success=%d inactive-actor rejection=%d", succeeded, rejected)
	}
	var activeCount int
	var remaining uuid.UUID
	if err := fixture.service.pool.QueryRow(t.Context(), `select count(*) from pgbase_meta.admins where active`).Scan(&activeCount); err != nil {
		t.Fatal(err)
	}
	if activeCount != 1 {
		t.Fatalf("active administrators=%d, want exactly one", activeCount)
	}
	if err := fixture.service.pool.QueryRow(t.Context(), `select id from pgbase_meta.admins where active`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != winner {
		t.Fatal("successful actor did not remain active")
	}
}

func TestAdministratorAccountsInactiveActorCannotManageAccounts(t *testing.T) {
	fixture := newAccountManagementFixture(t)
	s := fixture.service
	ctx := t.Context()
	session, err := s.Login(ctx, fixture.first.Email, accountTestPassword, "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAdministratorActive(ctx, fixture.first.ID, fixture.second.ID, false); err != nil {
		t.Fatal(err)
	}
	var beforeHash string
	if err := s.pool.QueryRow(ctx, `select password_hash from pgbase_meta.admins where id=$1`, fixture.first.ID).Scan(&beforeHash); err != nil {
		t.Fatal(err)
	}
	operations := []struct {
		name string
		run  func() error
	}{
		{"list", func() error {
			items, _, err := s.ListAdministratorAccounts(ctx, fixture.second.ID, uuid.Nil, 10)
			if len(items) != 0 {
				t.Error("inactive actor obtained account data")
			}
			return err
		}},
		{"create", func() error {
			_, err := s.CreateAdministratorAccount(ctx, fixture.second.ID, "unauthorized@example.test", accountTestReplacementPassword)
			return err
		}},
		{"deactivate", func() error {
			_, err := s.SetAdministratorActive(ctx, fixture.second.ID, fixture.first.ID, false)
			return err
		}},
		{"activate", func() error {
			_, err := s.SetAdministratorActive(ctx, fixture.second.ID, fixture.second.ID, true)
			return err
		}},
		{"password", func() error {
			return s.SetAdministratorPassword(ctx, fixture.second.ID, fixture.first.ID, accountTestReplacementPassword)
		}},
		{"revoke", func() error { return s.RevokeAdministratorSessions(ctx, fixture.second.ID, fixture.first.ID) }},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("inactive actor error=%v", err)
			}
		})
	}
	var count, active int
	if err := s.pool.QueryRow(ctx, `select count(*),count(*) filter(where active) from pgbase_meta.admins`).Scan(&count, &active); err != nil {
		t.Fatal(err)
	}
	var afterHash string
	if err := s.pool.QueryRow(ctx, `select password_hash from pgbase_meta.admins where id=$1`, fixture.first.ID).Scan(&afterHash); err != nil {
		t.Fatal(err)
	}
	if count != 2 || active != 1 || beforeHash != afterHash {
		t.Fatal("rejected management operation changed administrator state")
	}
	if _, err := s.Authenticate(ctx, session.RawToken); err != nil {
		t.Fatalf("rejected revocation changed target session: %v", err)
	}
}

type pendingAccountCredentials struct {
	session Session
	reset   string
	otp     EmailOTP
}

func seedPendingAccountCredentials(t *testing.T, fixture accountManagementFixture, account Administrator, totp bool) pendingAccountCredentials {
	t.Helper()
	s, ctx := fixture.service, t.Context()
	var session Session
	var err error
	if totp {
		secret, err := decodeTOTPSecret(accountTestTOTPSecret)
		if err != nil {
			t.Fatal(err)
		}
		session, err = s.LoginWithOTP(ctx, account.Email, accountTestPassword, "192.0.2.1", testTOTP(secret, fixture.clock.Now()))
		if err != nil {
			t.Fatal(err)
		}
	} else {
		session, err = s.Login(ctx, account.Email, accountTestPassword, "192.0.2.1")
		if err != nil {
			t.Fatal(err)
		}
	}
	mail := &resetCapture{}
	if err := s.ConfigurePasswordReset(mail, "https://accounts.example.test", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureEmailOTP(mail, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.RequestPasswordReset(ctx, account.Email, "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	if len(mail.messages) != 1 || mail.messages[0].Purpose != "admin_password_reset" {
		t.Fatal("reset fixture did not issue one pending credential")
	}
	reset := resetToken(t, mail.messages[0])
	otp, err := s.IssueEmailOTP(ctx, account.Email)
	if err != nil {
		t.Fatal(err)
	}
	return pendingAccountCredentials{session: session, reset: reset, otp: otp}
}

func assertAccountCredentialCounts(t *testing.T, s *Service, id uuid.UUID, want int) {
	t.Helper()
	var sessions, resets, otps int
	if err := s.pool.QueryRow(t.Context(), `select (select count(*) from pgbase_meta.admin_sessions where admin_id=$1),(select count(*) from pgbase_meta.admin_password_resets where admin_id=$1),(select count(*) from pgbase_meta.admin_email_otps where admin_id=$1)`, id).Scan(&sessions, &resets, &otps); err != nil {
		t.Fatal(err)
	}
	if sessions != want || resets != want || otps != want {
		t.Fatalf("credential counts sessions/reset/OTP=%d/%d/%d, want %d each", sessions, resets, otps, want)
	}
}

func TestAdministratorAccountsPasswordAndDeactivationInvalidateCredentialsPreserveFactors(t *testing.T) {
	for _, operation := range []string{"password", "deactivate"} {
		t.Run(operation, func(t *testing.T) {
			fixture := newAccountManagementFixture(t)
			s, ctx := fixture.service, t.Context()
			if err := s.EnableTOTP(ctx, fixture.second.Email, accountTestTOTPSecret); err != nil {
				t.Fatal(err)
			}
			if err := s.AllowIP(ctx, fixture.second.Email, "192.0.2.0/24"); err != nil {
				t.Fatal(err)
			}
			totpBefore, enabled, err := NewStore(s.pool).TOTP(ctx, fixture.second.ID)
			if err != nil || !enabled {
				t.Fatal("TOTP fixture missing")
			}
			prefixesBefore, err := NewStore(s.pool).IPPrefixes(ctx, fixture.second.ID)
			if err != nil {
				t.Fatal(err)
			}
			actorCredentials := seedPendingAccountCredentials(t, fixture, fixture.first, false)
			targetCredentials := seedPendingAccountCredentials(t, fixture, fixture.second, true)
			assertAccountCredentialCounts(t, s, fixture.first.ID, 1)
			assertAccountCredentialCounts(t, s, fixture.second.ID, 1)
			password := accountTestPassword
			if operation == "password" {
				password = accountTestReplacementPassword
				if err := s.SetAdministratorPassword(ctx, fixture.first.ID, fixture.second.ID, password); err != nil {
					t.Fatal(err)
				}
			} else {
				account, err := s.SetAdministratorActive(ctx, fixture.first.ID, fixture.second.ID, false)
				if err != nil || account.Active {
					t.Fatalf("deactivation active=%t err=%v", account.Active, err)
				}
			}
			assertAccountCredentialCounts(t, s, fixture.second.ID, 0)
			assertAccountCredentialCounts(t, s, fixture.first.ID, 1)
			totpAfter, enabled, err := NewStore(s.pool).TOTP(ctx, fixture.second.ID)
			if err != nil || !enabled || !bytes.Equal(totpBefore, totpAfter) {
				t.Fatal("management change altered persisted TOTP")
			}
			prefixesAfter, err := NewStore(s.pool).IPPrefixes(ctx, fixture.second.ID)
			if err != nil || !reflect.DeepEqual(prefixesBefore, prefixesAfter) {
				t.Fatal("management change altered IP policy")
			}
			// Reactivate before trying old credentials so inactive status cannot hide
			// a missed session/reset/OTP invalidation.
			if operation == "deactivate" {
				if _, err := s.SetAdministratorActive(ctx, fixture.first.ID, fixture.second.ID, true); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Authenticate(ctx, targetCredentials.session.RawToken); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("old session error=%v", err)
			}
			if err := s.ConfirmPasswordReset(ctx, targetCredentials.reset, "rejected pending recovery password"); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("old reset error=%v", err)
			}
			if _, err := s.CompleteEmailOTP(ctx, targetCredentials.otp.ID, targetCredentials.otp.Code, "192.0.2.1"); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("old email OTP error=%v", err)
			}
			if _, err := s.Authenticate(ctx, actorCredentials.session.RawToken); err != nil {
				t.Fatalf("actor session revoked: %v", err)
			}
			if _, err := s.Login(ctx, fixture.second.Email, password, "192.0.2.1"); !errors.Is(err, ErrOTPRequired) {
				t.Fatalf("TOTP-free login error=%v", err)
			}
			secret, err := decodeTOTPSecret(accountTestTOTPSecret)
			if err != nil {
				t.Fatal(err)
			}
			code := testTOTP(secret, fixture.clock.Now())
			if err := s.AuthorizeIP(ctx, fixture.second.ID, "198.51.100.1"); !errors.Is(err, ErrIPRestricted) {
				t.Fatalf("preserved IP policy error=%v", err)
			}
			if _, err := s.LoginWithOTP(ctx, fixture.second.Email, password, "198.51.100.1", code); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("disallowed network error=%v", err)
			}
			if _, err := s.LoginWithOTP(ctx, fixture.second.Email, password, "192.0.2.1", code); err != nil {
				t.Fatalf("preserved factors rejected valid login: %v", err)
			}
		})
	}
}

func TestAdministratorAccountsChangingInactivePasswordDoesNotActivateAccount(t *testing.T) {
	fixture := newAccountManagementFixture(t)
	s, ctx := fixture.service, t.Context()
	if _, err := s.SetAdministratorActive(ctx, fixture.first.ID, fixture.second.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAdministratorPassword(ctx, fixture.first.ID, fixture.second.ID, accountTestReplacementPassword); err != nil {
		t.Fatal(err)
	}
	var active bool
	var hash string
	if err := s.pool.QueryRow(ctx, `select active,password_hash from pgbase_meta.admins where id=$1`, fixture.second.ID).Scan(&active, &hash); err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("password change activated inactive account")
	}
	if err := s.passwords.Verify(hash, accountTestReplacementPassword); err != nil {
		t.Fatal("new password was not persisted")
	}
	if _, err := s.Login(ctx, fixture.second.Email, accountTestReplacementPassword, "192.0.2.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("inactive login error=%v", err)
	}
	if _, err := s.SetAdministratorActive(ctx, fixture.first.ID, fixture.second.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login(ctx, fixture.second.Email, accountTestReplacementPassword, "192.0.2.1"); err != nil {
		t.Fatalf("new password after explicit activation: %v", err)
	}
	items, _, err := s.ListAdministratorAccounts(ctx, fixture.first.ID, uuid.Nil, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("account inventory count=%d err=%v", len(items), err)
	}
	if items[0].ID == items[1].ID {
		t.Fatal("account inventory duplicated target")
	}
}
