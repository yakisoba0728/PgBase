//go:build integration

package userauth

import (
	"context"
	"encoding/base32"
	"errors"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

func TestTOTPEnrollmentRevokesSessionsAndGatesLogin(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	registered, err := service.Register(ctx, "mfa@example.com", "correct horse battery staple", "mfa-register")
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := service.BeginTOTPEnrollment(ctx, registered.User.ID, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrollment.Secret)
	if err != nil {
		t.Fatal(err)
	}
	now, err := service.databaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := service.ConfirmTOTPEnrollment(ctx, registered.User.ID, totpCode(secret, now.Unix()/30))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, registered.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old session = %v", err)
	}
	_, err = service.Login(ctx, registered.User.Email, "correct horse battery staple", "mfa-login")
	var required *MFARequiredError
	if !errors.As(err, &required) || required.ChallengeToken == "" {
		t.Fatalf("login = %#v", err)
	}
	session, err := service.CompleteMFA(ctx, required.ChallengeToken, recovery.Codes[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, session.RawToken); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteMFA(ctx, required.ChallengeToken, recovery.Codes[0]); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("challenge replay = %v", err)
	}
}
