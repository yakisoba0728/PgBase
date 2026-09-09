package pgbase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/userauth"
)

func TestEveryAuthLifecycleVetoPreventsDelegationAndPairsAfter(t *testing.T) {
	veto := errors.New("veto")
	realm, user := uuid.New(), uuid.New()
	tests := []struct {
		kind string
		call func(h hookedUserAuth) error
	}{
		{HookAuthPassword, func(h hookedUserAuth) error {
			_, err := h.RegisterInCollection(context.Background(), realm, "secret@example.test", "plaintext", "network")
			return err
		}},
		{HookAuthOAuthBegin, func(h hookedUserAuth) error {
			_, err := h.BeginOAuthInCollection(context.Background(), realm, "provider", "https://return.test", "network")
			return err
		}},
		{HookAuthOAuthComplete, func(h hookedUserAuth) error {
			_, err := h.CompleteOAuth(context.Background(), "provider", "state-secret", "code-secret")
			return err
		}},
		{HookAuthOTPRequest, func(h hookedUserAuth) error {
			_, err := h.RequestEmailOTPInCollection(context.Background(), realm, "secret@example.test", "network")
			return err
		}},
		{HookAuthOTPComplete, func(h hookedUserAuth) error {
			_, err := h.CompleteEmailOTPInCollection(context.Background(), realm, "otp-id", "123456")
			return err
		}},
		{HookAuthVerificationRequest, func(h hookedUserAuth) error { return h.RequestVerification(context.Background(), user) }},
		{HookAuthVerificationConfirm, func(h hookedUserAuth) error { return h.ConfirmVerification(context.Background(), "token-secret") }},
		{HookAuthPasswordResetRequest, func(h hookedUserAuth) error {
			return h.RequestPasswordResetInCollection(context.Background(), realm, "secret@example.test", "network")
		}},
		{HookAuthPasswordResetConfirm, func(h hookedUserAuth) error {
			return h.ConfirmPasswordReset(context.Background(), "token-secret", "plaintext")
		}},
		{HookAuthEmailChangeRequest, func(h hookedUserAuth) error {
			return h.RequestEmailChange(context.Background(), user, "plaintext", "secret@example.test")
		}},
		{HookAuthEmailChangeConfirm, func(h hookedUserAuth) error { return h.ConfirmEmailChange(context.Background(), "token-secret") }},
		{HookAuthImpersonate, func(h hookedUserAuth) error {
			_, err := h.Impersonate(context.Background(), user, "admin-token-secret", time.Minute)
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			registry := &HookRegistry{}
			var beforeData any
			var afterErr error
			_, err := registry.On(test.kind, Hook{
				Before: func(_ context.Context, event *HookEvent) error { beforeData = event.Data; return veto },
				After:  func(_ context.Context, event *HookEvent) { afterErr = event.Err },
			})
			if err != nil {
				t.Fatal(err)
			}
			h := hookedUserAuth{Service: (*userauth.Service)(nil), registry: registry}
			if err := test.call(h); !errors.Is(err, veto) || !errors.Is(afterErr, veto) {
				t.Fatalf("call=%v after=%v", err, afterErr)
			}
			if _, ok := beforeData.(*AuthEvent); !ok {
				t.Fatalf("payload=%T", beforeData)
			}
		})
	}
}
