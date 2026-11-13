//go:build integration

package adminauth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAdministratorPasswordReplacementInvalidatesPendingEmailCredentials(t *testing.T) {
	for _, operation := range []string{"reset", "confirm-reset", "upsert"} {
		t.Run(operation, func(t *testing.T) {
			ctx := context.Background()
			clock := &fakeClock{now: time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC)}
			service := NewService(newAdminAuthPool(t), NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), "setup-token-012345678901234567890", time.Hour, clock)
			capture := &resetCapture{}
			if err := service.ConfigurePasswordReset(capture, "https://pgbase.example", time.Hour); err != nil {
				t.Fatal(err)
			}
			if err := service.ConfigureEmailOTP(capture, time.Minute); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Setup(ctx, "setup-token-012345678901234567890", "admin@example.test", "correct horse battery staple", "192.0.2.1"); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := service.RequestPasswordReset(ctx, "admin@example.test", "192.0.2.1"); err != nil {
					t.Fatal(err)
				}
			}
			oldReset := resetToken(t, capture.messages[0])
			newReset := resetToken(t, capture.messages[1])
			otp, err := service.IssueEmailOTP(ctx, "admin@example.test")
			if err != nil {
				t.Fatal(err)
			}
			switch operation {
			case "reset":
				_, err = service.ResetAdministratorPassword(ctx, "admin@example.test", "replacement correct horse battery")
			case "confirm-reset":
				err = service.ConfirmPasswordReset(ctx, newReset, "replacement correct horse battery")
			case "upsert":
				_, _, err = service.UpsertAdministrator(ctx, "admin@example.test", "replacement correct horse battery")
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.CompleteEmailOTP(ctx, otp.ID, otp.Code, "192.0.2.1"); !errors.Is(err, ErrInvalidCredentials) {
				t.Errorf("pre-change OTP still authenticates: %v", err)
			}
			if err := service.ConfirmPasswordReset(ctx, oldReset, "older reset overwrites new password"); !errors.Is(err, ErrInvalidCredentials) {
				t.Errorf("pre-change reset token still changes password: %v", err)
			}
		})
	}
}
