//go:build integration

package userauth

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
)

func TestVerificationRejectsTokenSentBeforeEmailChange(t *testing.T) {
	service, sender := newEmailActionService(t)
	ctx := context.Background()
	registered, err := service.Register(ctx, "original@example.test", "correct horse battery staple", "verify-binding-register")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RequestVerification(ctx, registered.User.ID); err != nil {
		t.Fatal(err)
	}
	oldToken := sender.token(t)
	newEmail := "replacement@example.test"
	if _, err := service.UpdateAuthRecord(ctx, DefaultCollectionID, registered.User.ID, UpdateAuthRecordInput{Email: &newEmail}); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfirmVerification(ctx, oldToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("token sent to previous email must not verify replacement email: %v", err)
	}
	record, err := service.GetAuthRecord(ctx, DefaultCollectionID, registered.User.ID)
	if err != nil || record.Email != newEmail || record.Verified {
		t.Fatalf("rejected verification changed replacement record: %#v %v", record, err)
	}
	if err := service.RequestVerification(ctx, registered.User.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfirmVerification(ctx, sender.token(t)); err != nil {
		t.Fatalf("token sent to replacement address must verify: %v", err)
	}
	record, err = service.GetAuthRecord(ctx, DefaultCollectionID, registered.User.ID)
	if err != nil || !record.Verified {
		t.Fatalf("replacement verification not reflected: %#v %v", record, err)
	}
}

func TestEmailOTPRejectsCodeSentBeforeEmailChange(t *testing.T) {
	for _, keyed := range []bool{false, true} {
		name := "service-key"
		if keyed {
			name = "collection-key"
		}
		t.Run(name, func(t *testing.T) {
			service, sender := newEmailActionService(t)
			ctx := context.Background()
			registered, err := service.Register(ctx, "otp-original@example.test", "correct horse battery staple", "otp-binding-register")
			if err != nil {
				t.Fatal(err)
			}
			settings, err := service.GetAuthAdministration(ctx, DefaultCollectionID)
			if err != nil {
				t.Fatal(err)
			}
			settings.Policy.OTPEnabled = true
			if _, err := service.SetAuthPolicy(ctx, DefaultCollectionID, settings.Policy); err != nil {
				t.Fatal(err)
			}
			if keyed {
				if _, err := service.PutTokenPolicy(ctx, DefaultCollectionID, TokenPolicy{Purpose: "otp", TTLSeconds: 60, KeyVersion: "email-binding", Key: "email-binding-policy-key-000000001"}); err != nil {
					t.Fatal(err)
				}
			}
			old, err := service.RequestEmailOTP(ctx, registered.User.Email, "otp-binding-old")
			if err != nil {
				t.Fatal(err)
			}
			oldCode := otpFromMessage(t, sender)
			newEmail := "otp-replacement@example.test"
			if _, err := service.UpdateAuthRecord(ctx, DefaultCollectionID, registered.User.ID, UpdateAuthRecordInput{Email: &newEmail}); err != nil {
				t.Fatal(err)
			}
			if _, err := service.CompleteEmailOTP(ctx, old.OTPID, oldCode); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("code sent to previous email must not authenticate replacement account: %v", err)
			}
			fresh, err := service.RequestEmailOTP(ctx, newEmail, "otp-binding-new")
			if err != nil {
				t.Fatal(err)
			}
			session, err := service.CompleteEmailOTP(ctx, fresh.OTPID, otpFromMessage(t, sender))
			if err != nil || session.User.ID != registered.User.ID || session.User.Email != newEmail || session.User.Verified {
				t.Fatalf("fresh code should authenticate without changing verification policy: %#v %v", session.User, err)
			}
		})
	}
}

func TestEmailActionsRejectTokensAfterManagedEmailChange(t *testing.T) {
	for _, purpose := range []string{"verify", "password_reset", "email_change"} {
		t.Run(purpose, func(t *testing.T) {
			service, sender := newEmailActionService(t)
			ctx := context.Background()
			collectionID := uuid.New()
			rules := AuthCollectionRules{Create: "true", Update: "true", Manage: "@request.auth.email = 'manager@example.test'"}
			if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: collectionID, Name: "mail_binding_" + collectionID.String()[:8], RegistrationEnabled: true, Rules: rules}); err != nil {
				t.Fatal(err)
			}
			manager, err := service.RegisterInCollection(ctx, collectionID, "manager@example.test", "correct horse battery staple", "mail-binding-manager")
			if err != nil {
				t.Fatal(err)
			}
			target, err := service.RegisterInCollection(ctx, collectionID, "target@example.test", "correct horse battery staple", "mail-binding-target")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.PutTokenPolicy(ctx, collectionID, TokenPolicy{Purpose: purpose, TTLSeconds: 60, KeyVersion: "email-actions", Key: "email-action-binding-key-000000001"}); err != nil {
				t.Fatal(err)
			}
			var publicType string
			var confirm func(string) error
			switch purpose {
			case "verify":
				err = service.RequestVerification(ctx, target.User.ID)
				publicType = "verifyEmail"
				confirm = func(token string) error { return service.ConfirmVerification(ctx, token) }
			case "password_reset":
				err = service.RequestPasswordResetInCollection(ctx, collectionID, target.User.Email, "mail-binding-reset")
				publicType = "passwordReset"
				confirm = func(token string) error {
					return service.ConfirmPasswordReset(ctx, token, "replacement horse battery staple")
				}
			case "email_change":
				err = service.RequestEmailChange(ctx, target.User.ID, "correct horse battery staple", "pending@example.test")
				publicType = "changeEmail"
				confirm = func(token string) error { return service.ConfirmEmailChange(ctx, token) }
			}
			if err != nil {
				t.Fatal(err)
			}
			token := sender.token(t)
			administrator := identity.Subject{Kind: identity.Administrator, ID: uuid.New()}
			if record, err := service.AdminFindAuthRecordByToken(ctx, administrator, token, []string{publicType}); err != nil || record.ID != target.User.ID {
				t.Fatalf("valid email action was not discoverable: %#v %v", record, err)
			}
			newEmail := "reassigned@example.test"
			if _, err := service.UpdateAuthorizedAuthRecord(ctx, collectionID, manager.User.ID, target.User.ID, UpdateAuthRecordInput{Email: &newEmail}); err != nil {
				t.Fatal(err)
			}
			if _, err := service.AdminFindAuthRecordByToken(ctx, administrator, token, []string{publicType}); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("lookup accepted email action from previous address: %v", err)
			}
			if err := confirm(token); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("confirmation accepted email action from previous address: %v", err)
			}
			record, err := service.GetAuthRecord(ctx, collectionID, target.User.ID)
			if err != nil || record.Email != newEmail || record.Verified {
				t.Fatalf("rejected action changed account: %#v %v", record, err)
			}
			if _, err := service.LoginInCollection(ctx, collectionID, newEmail, "correct horse battery staple", "mail-binding-login"); err != nil {
				t.Fatalf("rejected action must preserve current password: %v", err)
			}
		})
	}
}
