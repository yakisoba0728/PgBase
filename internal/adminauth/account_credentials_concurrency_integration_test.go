//go:build integration

package adminauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/database"
)

func TestAccountPasswordChangeSerializesPendingCredentialRedemption(t *testing.T) {
	for _, kind := range []string{"password-reset", "email-otp"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			pool := newAdminAuthPool(t)
			service := NewService(pool, NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), "account-race-setup", time.Hour, nil)
			capture := &resetCapture{}
			if err := service.ConfigurePasswordReset(capture, "https://pgbase.example", time.Hour); err != nil {
				t.Fatal(err)
			}
			if err := service.ConfigureEmailOTP(capture, time.Minute); err != nil {
				t.Fatal(err)
			}
			owner, err := service.Setup(ctx, "account-race-setup", "owner@example.test", "owner account password", "192.0.2.1")
			if err != nil {
				t.Fatal(err)
			}
			target, err := service.CreateAdministratorAccount(ctx, owner.Administrator.ID, "target@example.test", "target account password")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Login(ctx, target.Email, "target account password", "192.0.2.2"); err != nil {
				t.Fatal(err)
			}
			var redeem func() error
			if kind == "password-reset" {
				if err := service.RequestPasswordReset(ctx, target.Email, "192.0.2.3"); err != nil {
					t.Fatal(err)
				}
				token := resetToken(t, capture.messages[len(capture.messages)-1])
				redeem = func() error { return service.ConfirmPasswordReset(ctx, token, "recovery account password") }
			} else {
				otp, err := service.IssueEmailOTP(ctx, target.Email)
				if err != nil {
					t.Fatal(err)
				}
				redeem = func() error {
					_, err := service.CompleteEmailOTP(ctx, otp.ID, otp.Code, "192.0.2.4")
					return err
				}
			}

			// Pause management after it locks the target account, before it can
			// delete pending credentials. The real public methods run unchanged.
			const gate = 87009123
			if _, err := pool.Exec(ctx, `create function pgbase_meta.pause_account_session_delete() returns trigger language plpgsql as $$ begin perform pg_advisory_xact_lock(87009123); return old; end $$`); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `create trigger pause_account_session_delete before delete on pgbase_meta.admin_sessions for each row execute function pgbase_meta.pause_account_session_delete()`); err != nil {
				t.Fatal(err)
			}
			barrier, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rollbackCredentialBarrier(barrier)
			if _, err := barrier.Exec(ctx, `select pg_advisory_xact_lock($1)`, gate); err != nil {
				t.Fatal(err)
			}
			changed := make(chan error, 1)
			go func() {
				changed <- service.SetAdministratorPassword(ctx, owner.Administrator.ID, target.ID, "managed account password")
			}()
			manager := waitCredentialLock(t, ctx, pool, 0)
			completed := make(chan error, 1)
			go func() { completed <- redeem() }()
			waitCredentialLock(t, ctx, pool, manager)
			if err := barrier.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-changed; err != nil {
				t.Fatalf("managed password change failed: %v", err)
			}
			if err := <-completed; !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("pending credential must be invalidated without a lock failure: %v", err)
			}
			if _, err := service.Login(ctx, target.Email, "managed account password", "192.0.2.5"); err != nil {
				t.Fatalf("management password was not retained: %v", err)
			}
		})
	}
}

func TestAccountDeactivationPreventsLateCredentialIssuance(t *testing.T) {
	for _, kind := range []string{"password-reset", "email-otp"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			pool := newAdminAuthPool(t)
			service := NewService(pool, NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), "account-race-setup", time.Hour, nil)
			capture := &resetCapture{}
			if err := service.ConfigurePasswordReset(capture, "https://pgbase.example", time.Hour); err != nil {
				t.Fatal(err)
			}
			if err := service.ConfigureEmailOTP(capture, time.Minute); err != nil {
				t.Fatal(err)
			}
			owner, err := service.Setup(ctx, "account-race-setup", "owner@example.test", "owner account password", "192.0.2.1")
			if err != nil {
				t.Fatal(err)
			}
			target, err := service.CreateAdministratorAccount(ctx, owner.Administrator.ID, "target@example.test", "target account password")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Login(ctx, target.Email, "target account password", "192.0.2.2"); err != nil {
				t.Fatal(err)
			}
			// A real deactivation holds the account lock while the issuance call
			// starts. The trigger only supplies a deterministic scheduling gate.
			if _, err := pool.Exec(ctx, `create function pgbase_meta.pause_account_session_delete() returns trigger language plpgsql as $$ begin perform pg_advisory_xact_lock(87009123); return old; end $$`); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `create trigger pause_account_session_delete before delete on pgbase_meta.admin_sessions for each row execute function pgbase_meta.pause_account_session_delete()`); err != nil {
				t.Fatal(err)
			}
			barrier, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rollbackCredentialBarrier(barrier)
			if _, err := barrier.Exec(ctx, `select pg_advisory_xact_lock(87009123)`); err != nil {
				t.Fatal(err)
			}
			changed := make(chan error, 1)
			go func() {
				_, err := service.SetAdministratorActive(ctx, owner.Administrator.ID, target.ID, false)
				changed <- err
			}()
			manager := waitCredentialLock(t, ctx, pool, 0)
			issued := make(chan error, 1)
			go func() {
				if kind == "password-reset" {
					issued <- service.RequestPasswordReset(ctx, target.Email, "192.0.2.3")
				} else {
					_, err := service.IssueEmailOTP(ctx, target.Email)
					issued <- err
				}
			}()
			waitCredentialLock(t, ctx, pool, manager)
			if err := barrier.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-changed; err != nil {
				t.Fatalf("deactivation failed: %v", err)
			}
			issueErr := <-issued
			if kind == "password-reset" && issueErr != nil {
				t.Fatalf("reset response must remain enumeration-safe: %v", issueErr)
			}
			if kind == "email-otp" && !errors.Is(issueErr, ErrInvalidCredentials) {
				t.Errorf("inactive administrator OTP issuance must be rejected: %v", issueErr)
			}
			if len(capture.messages) != 0 {
				t.Errorf("credential was delivered after deactivation: %d messages", len(capture.messages))
			}
			var pending int
			if err := pool.QueryRow(ctx, `select (select count(*) from pgbase_meta.admin_password_resets where admin_id=$1)+(select count(*) from pgbase_meta.admin_email_otps where admin_id=$1)`, target.ID).Scan(&pending); err != nil {
				t.Fatal(err)
			}
			if pending != 0 {
				t.Errorf("deactivated administrator retained %d pending credentials", pending)
			}
		})
	}
}

func TestEmailOTPPropagatesCredentialQueryCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	pool := newAdminAuthPool(t)
	service := NewService(pool, NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), "account-race-setup", time.Hour, nil)
	if err := service.ConfigureEmailOTP(&resetCapture{}, time.Minute); err != nil {
		t.Fatal(err)
	}
	owner, err := service.Setup(ctx, "account-race-setup", "owner@example.test", "owner account password", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	otp, err := service.IssueEmailOTP(ctx, owner.Administrator.Email)
	if err != nil {
		t.Fatal(err)
	}
	barrier, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackCredentialBarrier(barrier)
	if _, err := barrier.Exec(ctx, `select id from pgbase_meta.admins where id=$1 for update`, owner.Administrator.ID); err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 1)
	go func() {
		_, err := service.CompleteEmailOTP(ctx, otp.ID, otp.Code, "192.0.2.2")
		completed <- err
	}()
	pid := waitCredentialLock(t, ctx, pool, 0)
	var canceled bool
	if err := pool.QueryRow(ctx, `select pg_cancel_backend($1)`, pid).Scan(&canceled); err != nil || !canceled {
		t.Fatalf("cancel blocked OTP query: canceled=%v err=%v", canceled, err)
	}
	err = <-completed
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "57014" || database.Classify(err) != database.KindUnavailable {
		t.Fatalf("OTP completion lost original database cancellation: %v", err)
	}
	if err := barrier.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteEmailOTP(ctx, otp.ID, otp.Code, "192.0.2.3"); err != nil {
		t.Fatalf("failed query consumed the pending OTP: %v", err)
	}
}

func waitCredentialLock(t *testing.T, ctx context.Context, pool *database.Pool, exclude int) int {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var pid int
		var event, query string
		err := pool.QueryRow(ctx, `select a.pid,a.wait_event,a.query from pg_stat_activity a where a.datname=current_database() and a.pid<>pg_backend_pid() and a.pid<>$1 and a.wait_event_type='Lock' and exists(select 1 from pg_locks l where l.pid=a.pid and not l.granted) order by a.pid limit 1`, exclude).Scan(&pid, &event, &query)
		if err == nil {
			t.Logf("blocked backend %d: %s; %s", pid, event, query)
			return pid
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("credential operation did not reach a database lock")
		case <-ticker.C:
		}
	}
}

func rollbackCredentialBarrier(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
