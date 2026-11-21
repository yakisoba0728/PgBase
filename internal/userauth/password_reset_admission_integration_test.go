//go:build integration

package userauth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
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
	return "reset-fixture:" + password, nil
}

func (*resetAdmissionPasswords) Verify(string, string) error { return nil }

func TestPasswordResetRejectsIneligibleActionsBeforeHashing(t *testing.T) {
	for _, state := range []string{"missing", "expired", "consumed", "wrong-purpose", "password-changed", "email-changed", "inactive", "policy-rejected", "rotated-key"} {
		t.Run(state, func(t *testing.T) {
			s, sender := newBudgetEmailService(t)
			ctx := t.Context()
			user, err := s.Register(ctx, "reset-preflight@example.test", "correct horse battery staple", "reset-preflight")
			if err != nil {
				t.Fatal(err)
			}
			if state == "rotated-key" {
				if _, err := s.PutTokenPolicy(ctx, DefaultCollectionID, TokenPolicy{Purpose: "password_reset", TTLSeconds: 60, KeyVersion: "before", Key: "password-reset-preflight-key-000001"}); err != nil {
					t.Fatal(err)
				}
			}
			if state == "wrong-purpose" {
				err = s.RequestVerification(ctx, user.User.ID)
			} else {
				err = s.RequestPasswordReset(ctx, user.User.Email, "reset-preflight")
			}
			if err != nil {
				t.Fatal(err)
			}
			token := sender.token(t)
			want := ErrInvalidCredentials
			switch state {
			case "missing":
				token, err = adminauth.GenerateToken()
			case "expired":
				_, err = s.pool.Exec(ctx, `update pgbase_meta.user_email_actions set expires_at=CURRENT_TIMESTAMP-interval '1 second' where user_id=$1`, user.User.ID)
			case "consumed":
				_, err = s.pool.Exec(ctx, `update pgbase_meta.user_email_actions set consumed_at=CURRENT_TIMESTAMP where user_id=$1`, user.User.ID)
			case "password-changed":
				_, err = s.pool.Exec(ctx, `update pgbase_meta.users set password_hash='replacement-fixture' where id=$1`, user.User.ID)
			case "email-changed":
				_, err = s.pool.Exec(ctx, `update pgbase_meta.users set email='replacement@example.test' where id=$1`, user.User.ID)
			case "inactive":
				_, err = s.pool.Exec(ctx, `update pgbase_meta.users set active=false where id=$1`, user.User.ID)
			case "policy-rejected":
				_, err = s.pool.Exec(ctx, `update pgbase_meta.auth_collection_policies set password_enabled=false where auth_collection_id=$1`, DefaultCollectionID)
				want = ErrValidation
			case "rotated-key":
				_, err = s.PutTokenPolicy(ctx, DefaultCollectionID, TokenPolicy{Purpose: "password_reset", TTLSeconds: 60, KeyVersion: "after", Key: "password-reset-preflight-key-000002"})
			}
			if err != nil {
				t.Fatal(err)
			}
			passwords := &resetAdmissionPasswords{}
			s.passwords = passwords
			if err := s.ConfirmPasswordReset(ctx, token, "replacement horse battery staple"); !errors.Is(err, want) {
				t.Fatalf("confirmation = %v, want %v", err, want)
			}
			if got := passwords.hashes.Load(); got != 0 {
				t.Fatalf("ineligible action performed %d password hashes", got)
			}
		})
	}
}

func TestPasswordResetRechecksCredentialAfterHashing(t *testing.T) {
	s, sender := newBudgetEmailService(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	user, err := s.Register(ctx, "reset-race@example.test", "correct horse battery staple", "reset-race")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RequestPasswordReset(ctx, user.User.Email, "reset-race"); err != nil {
		t.Fatal(err)
	}
	token := sender.token(t)
	passwords := &resetAdmissionPasswords{onHash: func() {
		// A credential change must complete without waiting for KDF-held locks.
		if _, err := s.pool.Exec(ctx, `update pgbase_meta.users set password_hash='changed-during-hash' where id=$1`, user.User.ID); err != nil {
			t.Fatal(err)
		}
	}}
	s.passwords = passwords
	if err := s.ConfirmPasswordReset(ctx, token, "replacement horse battery staple"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("confirmation after credential change = %v", err)
	}
	var hash string
	if err := s.pool.QueryRow(ctx, `select password_hash from pgbase_meta.users where id=$1`, user.User.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash != "changed-during-hash" || passwords.hashes.Load() != 1 {
		t.Fatalf("hash=%q, KDF calls=%d", hash, passwords.hashes.Load())
	}
}

func TestPasswordResetConcurrentConfirmationCommitsOnce(t *testing.T) {
	s, sender := newBudgetEmailService(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	user, err := s.Register(ctx, "reset-concurrent@example.test", "correct horse battery staple", "reset-concurrent")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RequestPasswordReset(ctx, user.User.Email, "reset-concurrent"); err != nil {
		t.Fatal(err)
	}
	token := sender.token(t)
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
