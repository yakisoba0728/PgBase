//go:build integration

package userauth

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/mailer"
)

type capturedSender struct {
	mu       sync.Mutex
	messages []mailer.Message
	err      error
}

func (s *capturedSender) Send(_ context.Context, message mailer.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, message)
	return s.err
}

func (s *capturedSender) token(t *testing.T) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		t.Fatal("no email message captured")
	}
	parsed, err := url.Parse(s.messages[len(s.messages)-1].Text)
	if err != nil {
		t.Fatal(err)
	}
	fragment, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	token := fragment.Get("token")
	if token == "" {
		t.Fatalf("message contains no token: %q", s.messages[len(s.messages)-1].Text)
	}
	return token
}

func newEmailActionService(t *testing.T) (*Service, *capturedSender) {
	t.Helper()
	sender := &capturedSender{}
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	if err := service.ConfigureEmailActions(sender, "http://localhost:8090", time.Hour); err != nil {
		t.Fatal(err)
	}
	return service, sender
}

func TestVerificationTokenIsHashedPurposeBoundAndSingleUse(t *testing.T) {
	service, sender := newEmailActionService(t)
	ctx := context.Background()
	registered, err := service.Register(ctx, "verify@example.com", "correct horse battery staple", "verify-register")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RequestVerification(ctx, registered.User.ID); err != nil {
		t.Fatal(err)
	}
	token := sender.token(t)
	var stored []byte
	if err := service.pool.QueryRow(ctx, `select token_hash from pgbase_meta.user_email_actions where user_id=$1 and purpose='verify'`, registered.User.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if string(stored) == token || !hmacEqual(stored, service.actionTokenHash("verify", token)) {
		t.Fatal("verification action did not store only its purpose-bound token hash")
	}
	if err := service.ConfirmPasswordReset(ctx, token, "replacement horse battery"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("cross-purpose confirmation = %v", err)
	}
	if err := service.ConfirmVerification(ctx, token); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfirmVerification(ctx, token); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("replayed verification = %v", err)
	}
	principal, err := service.Authenticate(ctx, registered.RawToken)
	if err != nil || !principal.User.Verified {
		t.Fatalf("verified principal = %#v, %v", principal, err)
	}
}

func TestConfiguredVerificationKeyRotationExpiryAndSingleUse(t *testing.T) {
	service, sender := newEmailActionService(t)
	ctx := context.Background()
	registered, err := service.Register(ctx, "keyed-verify@example.test", "correct horse battery staple", "keyed-verify-register")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.PutTokenPolicy(ctx, DefaultCollectionID, TokenPolicy{Purpose: "verify", TTLSeconds: 60, KeyVersion: "verify-one", Key: "first-verify-key"}); err != nil {
		t.Fatal(err)
	}
	if err = service.RequestVerification(ctx, registered.User.ID); err != nil {
		t.Fatal(err)
	}
	rotated := sender.token(t)
	if _, err = service.PutTokenPolicy(ctx, DefaultCollectionID, TokenPolicy{Purpose: "verify", TTLSeconds: 60, KeyVersion: "verify-two", Key: "second-verify-key"}); err != nil {
		t.Fatal(err)
	}
	if err = service.ConfirmVerification(ctx, rotated); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("rotated verification=%v", err)
	}
	if err = service.RequestVerification(ctx, registered.User.ID); err != nil {
		t.Fatal(err)
	}
	expired := sender.token(t)
	if _, err = service.pool.Exec(ctx, `update pgbase_meta.user_email_actions set expires_at=CURRENT_TIMESTAMP-interval '1 second' where user_id=$1 and purpose='verify' and consumed_at is null`, registered.User.ID); err != nil {
		t.Fatal(err)
	}
	if err = service.ConfirmVerification(ctx, expired); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expired verification=%v", err)
	}
	if err = service.RequestVerification(ctx, registered.User.ID); err != nil {
		t.Fatal(err)
	}
	valid := sender.token(t)
	if err = service.ConfirmVerification(ctx, valid); err != nil {
		t.Fatal(err)
	}
	if err = service.ConfirmVerification(ctx, valid); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("replayed verification=%v", err)
	}
}

func TestPasswordResetIsNondisclosingAndCredentialBound(t *testing.T) {
	service, sender := newEmailActionService(t)
	ctx := context.Background()
	oldPassword := "correct horse battery staple"
	registered, err := service.Register(ctx, "reset@example.com", oldPassword, "reset-register")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RequestPasswordReset(ctx, "missing@example.com", "reset-missing"); err != nil {
		t.Fatalf("unknown reset request = %v", err)
	}
	if len(sender.messages) != 0 {
		t.Fatal("unknown reset request sent a message")
	}
	if err := service.RequestPasswordReset(ctx, registered.User.Email, "reset-known"); err != nil {
		t.Fatal(err)
	}
	staleToken := sender.token(t)
	if err := service.ChangePassword(ctx, registered.User.ID, oldPassword, "intermediate horse battery"); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfirmPasswordReset(ctx, staleToken, "stale replacement battery"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("credential-stale reset = %v", err)
	}
	if err := service.RequestPasswordReset(ctx, registered.User.Email, "reset-current"); err != nil {
		t.Fatal(err)
	}
	currentToken := sender.token(t)
	if err := service.ConfirmPasswordReset(ctx, currentToken, "final replacement battery"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, registered.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("original session after reset = %v", err)
	}
	if _, err := service.Login(ctx, registered.User.Email, "final replacement battery", "reset-final-login"); err != nil {
		t.Fatalf("new reset password login = %v", err)
	}
}

func TestEmailChangeIsUniqueVerifiedAndRevokesSessions(t *testing.T) {
	service, sender := newEmailActionService(t)
	ctx := context.Background()
	password := "correct horse battery staple"
	first, err := service.Register(ctx, "before@example.com", password, "email-first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Register(ctx, "occupied@example.com", password, "email-second"); err != nil {
		t.Fatal(err)
	}
	if err := service.RequestEmailChange(ctx, first.User.ID, password, "occupied@example.com"); !errors.Is(err, ErrConflict) {
		t.Fatalf("occupied email request = %v", err)
	}
	if err := service.RequestEmailChange(ctx, first.User.ID, password, "after@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfirmEmailChange(ctx, sender.token(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, first.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old session after email change = %v", err)
	}
	loggedIn, err := service.Login(ctx, "after@example.com", password, "email-after-login")
	if err != nil || !loggedIn.User.Verified || loggedIn.User.Email != "after@example.com" {
		t.Fatalf("changed email login = %#v, %v", loggedIn, err)
	}
}

func TestExpiredActionAndRedactedResetDeliveryFailure(t *testing.T) {
	service, sender := newEmailActionService(t)
	ctx := context.Background()
	registered, err := service.Register(ctx, "expiry@example.com", "correct horse battery staple", "expiry-register")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RequestVerification(ctx, registered.User.ID); err != nil {
		t.Fatal(err)
	}
	token := sender.token(t)
	if _, err := service.pool.Exec(ctx, `update pgbase_meta.user_email_actions set expires_at=CURRENT_TIMESTAMP-interval '1 second' where user_id=$1`, registered.User.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfirmVerification(ctx, token); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expired verification = %v", err)
	}
	sender.err = errors.New("fixture secret smtp failure")
	if err := service.RequestPasswordReset(ctx, registered.User.Email, "expiry-reset"); err != nil {
		t.Fatalf("reset delivery failure was disclosed: %v", err)
	}
}

func TestConcurrentActionConfirmationConsumesExactlyOnce(t *testing.T) {
	service, sender := newEmailActionService(t)
	ctx := context.Background()
	registered, err := service.Register(ctx, "single-use@example.com", "correct horse battery staple", "single-use-register")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RequestVerification(ctx, registered.User.ID); err != nil {
		t.Fatal(err)
	}
	token := sender.token(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			results <- service.ConfirmVerification(ctx, token)
		}()
	}
	close(start)
	succeeded, rejected := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrInvalidCredentials):
			rejected++
		default:
			t.Fatalf("ConfirmVerification race = %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("confirmation race succeeded/rejected = %d/%d, want 1/1", succeeded, rejected)
	}
}
