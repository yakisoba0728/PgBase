//go:build integration

package adminauth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type resetAdmissionPasswords struct {
	hashes atomic.Int32
	onHash func()
}

func (p *resetAdmissionPasswords) Hash(password string) (string, error) {
	p.hashes.Add(1)
	if p.onHash != nil {
		p.onHash()
	}
	return (credentialLockPasswords{}).Hash(password)
}

func (*resetAdmissionPasswords) Verify(hash, password string) error {
	return (credentialLockPasswords{}).Verify(hash, password)
}

func newResetAdmissionService(t *testing.T) (*Service, *resetCapture) {
	t.Helper()
	s := NewService(newAdminAuthPool(t), credentialLockPasswords{}, []byte("0123456789abcdef0123456789abcdef"), "reset-preflight-setup", time.Hour, nil)
	sender := &resetCapture{}
	if err := s.ConfigurePasswordReset(sender, "https://accounts.example.test", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Setup(t.Context(), "reset-preflight-setup", "admin@example.test", "correct horse battery staple", "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	if err := s.RequestPasswordReset(t.Context(), "admin@example.test", "192.0.2.2"); err != nil {
		t.Fatal(err)
	}
	return s, sender
}

func TestPasswordResetChecksEligibilityBeforeHashing(t *testing.T) {
	for _, state := range []string{"missing", "expired", "consumed", "inactive", "valid"} {
		t.Run(state, func(t *testing.T) {
			s, sender := newResetAdmissionService(t)
			ctx := t.Context()
			token := resetToken(t, sender.messages[0])
			var err error
			switch state {
			case "missing":
				token, err = GenerateToken()
			case "expired":
				_, err = s.pool.Exec(ctx, `update pgbase_meta.admin_password_resets set expires_at=CURRENT_TIMESTAMP-interval '1 second'`)
			case "consumed":
				_, err = s.pool.Exec(ctx, `update pgbase_meta.admin_password_resets set consumed_at=CURRENT_TIMESTAMP`)
			case "inactive":
				_, err = s.pool.Exec(ctx, `update pgbase_meta.admins set active=false where email='admin@example.test'`)
			}
			if err != nil {
				t.Fatal(err)
			}
			passwords := &resetAdmissionPasswords{}
			s.passwords = passwords
			err = s.ConfirmPasswordReset(ctx, token, "replacement horse battery staple")
			if state == "valid" {
				if err != nil || passwords.hashes.Load() != 1 {
					t.Fatalf("valid reset = %v, hashes=%d", err, passwords.hashes.Load())
				}
				if err := s.ConfirmPasswordReset(ctx, token, "another horse battery staple"); !errors.Is(err, ErrInvalidCredentials) || passwords.hashes.Load() != 1 {
					t.Fatalf("used reset = %v, hashes=%d", err, passwords.hashes.Load())
				}
				return
			}
			if !errors.Is(err, ErrInvalidCredentials) || passwords.hashes.Load() != 0 {
				t.Fatalf("ineligible reset = %v, hashes=%d", err, passwords.hashes.Load())
			}
		})
	}
}

func TestPasswordResetRechecksAfterPasswordWork(t *testing.T) {
	s, sender := newResetAdmissionService(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	token := resetToken(t, sender.messages[0])
	passwords := &resetAdmissionPasswords{onHash: func() {
		// Credential invalidation must not wait on preflight-held locks.
		if _, err := s.pool.Exec(ctx, `delete from pgbase_meta.admin_password_resets`); err != nil {
			t.Fatal(err)
		}
	}}
	s.passwords = passwords
	if err := s.ConfirmPasswordReset(ctx, token, "replacement horse battery staple"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("invalidated reset = %v", err)
	}
	if passwords.hashes.Load() != 1 {
		t.Fatalf("hashes=%d, want 1", passwords.hashes.Load())
	}
	if _, err := s.Login(ctx, "admin@example.test", "correct horse battery staple", "192.0.2.3"); err != nil {
		t.Fatalf("rejected reset changed password: %v", err)
	}
}

func TestPasswordResetConcurrentConfirmationCommitsOnce(t *testing.T) {
	s, sender := newResetAdmissionService(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	token := resetToken(t, sender.messages[0])
	entered, release := make(chan struct{}, 2), make(chan struct{})
	s.passwords = &resetAdmissionPasswords{onHash: func() { entered <- struct{}{}; <-release }}
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- s.ConfirmPasswordReset(ctx, token, "replacement horse battery staple") }()
	}
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			close(release)
			t.Fatal("confirmation did not reach password work")
		}
	}
	close(release)
	succeeded, rejected := 0, 0
	for range 2 {
		switch err := <-results; {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrInvalidCredentials):
			rejected++
		default:
			t.Fatal(err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("succeeded/rejected=%d/%d", succeeded, rejected)
	}
}
