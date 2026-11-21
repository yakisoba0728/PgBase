//go:build integration

package userauth

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newBudgetEmailService(t *testing.T) (*Service, *capturedSender) {
	t.Helper()
	s := NewService(newUserAuthPool(t), stubPasswords{}, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	sender := &capturedSender{}
	if err := s.ConfigureEmailActions(sender, "https://accounts.example.test", time.Hour); err != nil {
		t.Fatal(err)
	}
	return s, sender
}

func expireEmailActionCooldown(t *testing.T, s *Service, userID uuid.UUID, purpose, recipient string) {
	t.Helper()
	for _, bucket := range s.emailActionDeliveryBuckets(userID, purpose, recipient) {
		if bucket.window != emailActionResendCooldown {
			continue
		}
		if _, err := s.pool.Exec(t.Context(), `update pgbase_meta.user_auth_rate_limits set window_started_at=CURRENT_TIMESTAMP-interval '61 seconds' where subject_hash=$1`, bucket.hash); err != nil {
			t.Fatal(err)
		}
	}
}

func TestVerificationDeliveryBudgetIsSharedAcrossServices(t *testing.T) {
	s, sender := newBudgetEmailService(t)
	ctx := t.Context()
	user, err := s.Register(ctx, "shared-mail@example.test", "correct horse battery staple", "shared-mail")
	if err != nil {
		t.Fatal(err)
	}
	other := NewService(s.pool, stubPasswords{}, s.secret, time.Hour)
	if err := other.ConfigureEmailActions(sender, "https://accounts.example.test", time.Hour); err != nil {
		t.Fatal(err)
	}
	start, results := make(chan struct{}), make(chan error, 2)
	for _, service := range []*Service{s, other} {
		go func() { <-start; results <- service.RequestVerification(ctx, user.User.ID) }()
	}
	close(start)
	succeeded, limited := 0, 0
	for range 2 {
		switch err := <-results; {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrRateLimited):
			limited++
		default:
			t.Fatal(err)
		}
	}
	var count int
	if err := s.pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_email_actions where user_id=$1`, user.User.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if succeeded != 1 || limited != 1 || len(sender.messages) != 1 || count != 1 {
		t.Fatalf("accepted/limited=%d/%d, messages=%d, actions=%d", succeeded, limited, len(sender.messages), count)
	}
}

func TestEmailChangeDeliveryBudgetCountsChangingRecipientsAndBoundsPendingActions(t *testing.T) {
	s, sender := newBudgetEmailService(t)
	ctx := t.Context()
	user, err := s.Register(ctx, "mail-budget@example.test", "correct horse battery staple", "mail-budget")
	if err != nil {
		t.Fatal(err)
	}
	var previous string
	for i := range emailActionDeliveryLimit {
		if i > 0 {
			expireEmailActionCooldown(t, s, user.User.ID, "email_change", previous)
		}
		previous = fmt.Sprintf("destination-%d@example.test", i)
		if err := s.RequestEmailChange(ctx, user.User.ID, "correct horse battery staple", previous); err != nil {
			t.Fatal(err)
		}
	}
	expireEmailActionCooldown(t, s, user.User.ID, "email_change", previous)
	if err := s.RequestEmailChange(ctx, user.User.ID, "correct horse battery staple", "different@example.test"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("exhausted actor budget = %v", err)
	}
	var total, pending int
	if err := s.pool.QueryRow(ctx, `select count(*),count(*) filter (where consumed_at is null) from pgbase_meta.user_email_actions where user_id=$1 and purpose='email_change'`, user.User.ID).Scan(&total, &pending); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != emailActionDeliveryLimit || total != emailActionDeliveryLimit || pending != 1 {
		t.Fatalf("messages=%d, total=%d, pending=%d", len(sender.messages), total, pending)
	}
}

func TestEmailActionRecipientCooldownAndDeliveryFailureDoNotRefundCapacity(t *testing.T) {
	s, sender := newBudgetEmailService(t)
	ctx := t.Context()
	first, err := s.Register(ctx, "first-mail@example.test", "correct horse battery staple", "first-mail")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Register(ctx, "second-mail@example.test", "correct horse battery staple", "second-mail")
	if err != nil {
		t.Fatal(err)
	}
	sender.err = errors.New("delivery result unavailable")
	if err := s.RequestEmailChange(ctx, first.User.ID, "correct horse battery staple", "recipient@example.test"); err == nil {
		t.Fatal("expected transport failure")
	}
	if err := s.RequestEmailChange(ctx, first.User.ID, "correct horse battery staple", "another@example.test"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("actor cooldown after transport failure = %v", err)
	}
	if err := s.RequestEmailChange(ctx, second.User.ID, "correct horse battery staple", "recipient@example.test"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("shared recipient cooldown = %v", err)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("mailer called %d times, want 1", len(sender.messages))
	}
	var count int
	if err := s.pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_email_actions`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("actions=%d, err=%v", count, err)
	}
}
