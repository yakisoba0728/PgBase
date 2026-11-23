//go:build integration

package userauth

import (
	"context"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/mailer"
)

// stalledSender stands for a mail server that does not answer until released.
type stalledSender struct {
	entered chan string
	release chan struct{}
}

func (s *stalledSender) Send(ctx context.Context, message mailer.Message) error {
	s.entered <- message.Purpose
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// A slow mail server must not delay the answer for a registered address, or
// the response time tells registered and unknown addresses apart.
func TestPublicMailRequestsAnswerBeforeDelivery(t *testing.T) {
	sender := &stalledSender{entered: make(chan string, 4), release: make(chan struct{})}
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	if err := service.ConfigureEmailActions(sender, "http://localhost:8090", time.Hour); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	registered, err := service.Register(ctx, "queued-mail@example.test", "correct horse battery staple", "queued-mail-register")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `update pgbase_meta.auth_collection_policies set otp_enabled=true where auth_collection_id=$1`, DefaultCollectionID); err != nil {
		t.Fatal(err)
	}
	requests := []struct {
		purpose string
		request func(context.Context) error
	}{
		{"otp", func(ctx context.Context) error {
			_, err := service.RequestEmailOTP(ctx, registered.User.Email, "queued-mail-otp")
			return err
		}},
		{"password_reset", func(ctx context.Context) error {
			return service.RequestPasswordReset(ctx, registered.User.Email, "queued-mail-reset")
		}},
	}
	for _, request := range requests {
		// Cancelling the request once it has answered must not cancel the
		// delivery it queued.
		requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := request.request(requestCtx)
		cancel()
		if err != nil {
			t.Fatalf("%s request = %v", request.purpose, err)
		}
		select {
		case purpose := <-sender.entered:
			if purpose != request.purpose {
				t.Fatalf("queued delivery purpose = %q, want %q", purpose, request.purpose)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s mail was never handed to the mail server", request.purpose)
		}
	}
	waitCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := service.WaitForQueuedMail(waitCtx); err == nil {
		t.Fatal("queued mail finished while the mail server was still stalled")
	}
	close(sender.release)
	drainCtx, drainCancel := context.WithTimeout(ctx, 30*time.Second)
	defer drainCancel()
	if err := service.WaitForQueuedMail(drainCtx); err != nil {
		t.Fatal(err)
	}
	var otps, resets int
	if err := service.pool.QueryRow(ctx, `select (select count(*) from pgbase_meta.user_email_otps where user_id=$1 and consumed_at is null),(select count(*) from pgbase_meta.user_email_actions where user_id=$1 and purpose='password_reset' and consumed_at is null)`, registered.User.ID).Scan(&otps, &resets); err != nil {
		t.Fatal(err)
	}
	if otps != 1 || resets != 1 {
		t.Fatalf("pending OTPs=%d resets=%d, want one of each", otps, resets)
	}
}
