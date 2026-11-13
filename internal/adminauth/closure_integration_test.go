//go:build integration

package adminauth

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"testing"
	"time"
)

func TestAdminTOTPChangeWaitsForExistingLogin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := newAdminAuthPool(t)
	s := NewService(pool, NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), "closure", time.Hour, nil)
	session, err := s.Setup(ctx, "closure", "rotation@example.test", "correct horse battery staple", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	const oldSecret = "JBSWY3DPEHPK3PXP"
	if err = s.EnableTOTP(ctx, session.Administrator.Email, oldSecret); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `create function pgbase_meta.pause_rotation_login() returns trigger language plpgsql as $$ begin perform pg_advisory_xact_lock(91827001); return new; end $$`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `create trigger pause_rotation_login before insert on pgbase_meta.admin_sessions for each row execute function pgbase_meta.pause_rotation_login()`); err != nil {
		t.Fatal(err)
	}
	barrier, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackCredentialBarrier(barrier)
	if _, err = barrier.Exec(ctx, `select pg_advisory_xact_lock(91827001)`); err != nil {
		t.Fatal(err)
	}
	secret, _ := decodeTOTPSecret(oldSecret)
	type loginResult struct {
		session Session
		err     error
	}
	logged := make(chan loginResult, 1)
	go func() {
		v, e := s.LoginWithOTP(ctx, session.Administrator.Email, "correct horse battery staple", "192.0.2.1", testTOTP(secret, time.Now()))
		logged <- loginResult{v, e}
	}()
	loginPID := waitCredentialLock(t, ctx, pool, 0)
	changed := make(chan error, 1)
	go func() { changed <- s.EnableTOTP(ctx, session.Administrator.Email, "KRUGS4ZANFZSAYJA") }()
	// The old implementation completes rotation while the old-factor login is paused.
	var early bool
	for {
		select {
		case err = <-changed:
			if err != nil {
				t.Fatal(err)
			}
			early = true
		default:
		}
		if early {
			break
		}
		var waiting bool
		if err = pool.QueryRow(ctx, `select exists(select 1 from pg_stat_activity where datname=current_database() and pid<>$1 and wait_event_type='Lock')`, loginPID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err = barrier.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	login := <-logged
	if login.err != nil {
		t.Fatal(login.err)
	}
	if !early {
		if err = <-changed; err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.Authenticate(ctx, login.session.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old factor minted session after rotation: %v", err)
	}
}

func TestAdminSelfEmailChangeSerializesPendingIssuance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := newAdminAuthPool(t)
	s := NewService(pool, NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), "closure", time.Hour, nil)
	capture := &resetCapture{}
	if err := s.ConfigureEmailOTP(capture, time.Minute); err != nil {
		t.Fatal(err)
	}
	session, err := s.Setup(ctx, "closure", "self-old@example.test", "correct horse battery staple", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `create function pgbase_meta.pause_self_email() returns trigger language plpgsql as $$ begin perform pg_advisory_xact_lock(91827002); return new; end $$`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `create trigger pause_self_email before update of email on pgbase_meta.admins for each row execute function pgbase_meta.pause_self_email()`); err != nil {
		t.Fatal(err)
	}
	barrier, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackCredentialBarrier(barrier)
	if _, err = barrier.Exec(ctx, `select pg_advisory_xact_lock(91827002)`); err != nil {
		t.Fatal(err)
	}
	changed := make(chan error, 1)
	go func() {
		_, e := s.UpdateAdministratorCredentials(ctx, session.Administrator.ID, "192.0.2.1", "correct horse battery staple", "self-new@example.test", "")
		changed <- e
	}()
	changePID := waitCredentialLock(t, ctx, pool, 0)
	issued := make(chan error, 1)
	go func() { _, e := s.IssueEmailOTP(ctx, session.Administrator.Email); issued <- e }()
	// Issuance must wait on the same lifecycle lock as the credential mutation.
	early := false
	for {
		select {
		case err = <-issued:
			if err != nil {
				t.Fatal(err)
			}
			early = true
		default:
		}
		if early {
			break
		}
		var waiting bool
		if err = pool.QueryRow(ctx, `select exists(select 1 from pg_stat_activity where datname=current_database() and pid<>$1 and wait_event_type='Lock')`, changePID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err = barrier.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-changed; err != nil {
		t.Fatal(err)
	}
	if !early {
		err = <-issued
		if err != nil && !errors.Is(err, pgx.ErrNoRows) && !errors.Is(err, ErrInvalidCredentials) {
			t.Fatal(err)
		}
	}
	var count int
	if err = pool.QueryRow(ctx, `select count(*) from pgbase_meta.admin_email_otps where admin_id=$1`, session.Administrator.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("old-address OTP survived self email change: %d", count)
	}
}
