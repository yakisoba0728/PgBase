//go:build integration

package adminauth

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/mailer"
)

type resetCapture struct {
	messages []mailer.Message
	err      error
}

func (s *resetCapture) Send(_ context.Context, message mailer.Message) error {
	s.messages = append(s.messages, message)
	return s.err
}

func resetToken(t *testing.T, message mailer.Message) string {
	t.Helper()
	parsed, err := url.Parse(message.Text)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(parsed.Fragment, "token=")
}

func TestPasswordResetMailFlowIsOneUseAndRevokesSessions(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC)}
	service := NewService(pool, NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), "setup-token-012345678901234567890", time.Hour, clock)
	capture := &resetCapture{}
	if err := service.ConfigurePasswordReset(capture, "https://pgbase.example", time.Hour); err != nil {
		t.Fatal(err)
	}
	session, err := service.Setup(context.Background(), "setup-token-012345678901234567890", "admin@example.test", "correct horse battery staple", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RequestPasswordReset(context.Background(), "unknown@example.test", "192.0.2.2"); err != nil {
		t.Fatalf("unknown reset request: %v", err)
	}
	if len(capture.messages) != 0 {
		t.Fatal("unknown administrator sent mail")
	}
	if err := service.RequestPasswordReset(context.Background(), "ADMIN@example.test", "192.0.2.3"); err != nil {
		t.Fatal(err)
	}
	if len(capture.messages) != 1 || capture.messages[0].Purpose != "admin_password_reset" {
		t.Fatalf("messages=%#v", capture.messages)
	}
	token := resetToken(t, capture.messages[0])
	if err := service.ConfirmPasswordReset(context.Background(), token, "new correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), session.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old session error=%v, want invalid credentials", err)
	}
	if _, err := service.Login(context.Background(), "admin@example.test", "new correct horse battery staple", "192.0.2.4"); err != nil {
		t.Fatalf("new password login: %v", err)
	}
	if err := service.ConfirmPasswordReset(context.Background(), token, "another correct horse battery staple"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("reused token error=%v, want invalid credentials", err)
	}
}

func TestPasswordResetExpiryAndSMTPFailureAreNotDisclosed(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC)}
	service := NewService(pool, NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), "setup-token-012345678901234567890", time.Hour, clock)
	capture := &resetCapture{}
	if err := service.ConfigurePasswordReset(capture, "https://pgbase.example", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Setup(context.Background(), "setup-token-012345678901234567890", "admin@example.test", "correct horse battery staple", "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	if err := service.RequestPasswordReset(context.Background(), "admin@example.test", "192.0.2.2"); err != nil {
		t.Fatal(err)
	}
	token := resetToken(t, capture.messages[0])
	clock.now = clock.now.Add(2 * time.Minute)
	if err := service.ConfirmPasswordReset(context.Background(), token, "new correct horse battery staple"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expired token error=%v, want invalid credentials", err)
	}
	capture.err = errors.New("SMTP unavailable")
	if err := service.RequestPasswordReset(context.Background(), "admin@example.test", "192.0.2.3"); err != nil {
		t.Fatalf("SMTP failure leaked from reset request: %v", err)
	}
}
