package pgbase

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

// AuthEvent contains operation identity and a secret-free result projection.
// Passwords, bearer tokens, OAuth state/code, OTP values and email addresses
// are deliberately absent.
type AuthEvent struct {
	Flow               string
	RealmID            uuid.UUID
	UserID             uuid.UUID
	Provider           string
	Session            *AuthSessionEvent
	ChallengeExpiresAt time.Time
}

type AuthSessionEvent struct {
	UserID          uuid.UUID
	RealmID         uuid.UUID
	ExpiresAt       time.Time
	Impersonated    bool
	AdministratorID uuid.UUID
}

type hookedUserAuth struct {
	*userauth.Service
	registry *HookRegistry
}

func (h hookedUserAuth) begin(ctx context.Context, kind, flow string, realm, user uuid.UUID, provider string) (*AuthEvent, func(error), error) {
	event := &AuthEvent{Flow: flow, RealmID: realm, UserID: user, Provider: provider}
	finish, err := h.registry.Begin(ctx, &HookEvent{Kind: kind, Data: event})
	return event, finish, err
}

func authSessionResult(event *AuthEvent, session userauth.Session) {
	event.UserID, event.RealmID = session.User.ID, session.User.AuthCollectionID
	event.Session = &AuthSessionEvent{UserID: session.User.ID, RealmID: session.User.AuthCollectionID, ExpiresAt: session.ExpiresAt, Impersonated: session.Impersonated, AdministratorID: session.ImpersonatingAdministratorID}
}

func (h hookedUserAuth) session(ctx context.Context, kind, flow string, realm, user uuid.UUID, provider string, operation func() (userauth.Session, error)) (result userauth.Session, err error) {
	event, finish, err := h.begin(ctx, kind, flow, realm, user, provider)
	defer func() { finish(err) }()
	if err != nil {
		return result, err
	}
	result, err = operation()
	if err == nil {
		authSessionResult(event, result)
	}
	return result, err
}

func (h hookedUserAuth) action(ctx context.Context, kind, flow string, realm, user uuid.UUID, operation func() error) (err error) {
	_, finish, err := h.begin(ctx, kind, flow, realm, user, "")
	defer func() { finish(err) }()
	if err != nil {
		return err
	}
	return operation()
}

func (h hookedUserAuth) Register(ctx context.Context, email, password, address string) (userauth.Session, error) {
	return h.session(ctx, HookAuthPassword, "register", userauth.DefaultCollectionID, uuid.Nil, "", func() (userauth.Session, error) { return h.Service.Register(ctx, email, password, address) })
}
func (h hookedUserAuth) RegisterInCollection(ctx context.Context, realm uuid.UUID, email, password, address string) (userauth.Session, error) {
	return h.session(ctx, HookAuthPassword, "register", realm, uuid.Nil, "", func() (userauth.Session, error) {
		return h.Service.RegisterInCollection(ctx, realm, email, password, address)
	})
}
func (h hookedUserAuth) Login(ctx context.Context, email, password, address string) (userauth.Session, error) {
	return h.session(ctx, HookAuthPassword, "login", userauth.DefaultCollectionID, uuid.Nil, "", func() (userauth.Session, error) { return h.Service.Login(ctx, email, password, address) })
}
func (h hookedUserAuth) LoginInCollection(ctx context.Context, realm uuid.UUID, email, password, address string) (userauth.Session, error) {
	return h.session(ctx, HookAuthPassword, "login", realm, uuid.Nil, "", func() (userauth.Session, error) {
		return h.Service.LoginInCollection(ctx, realm, email, password, address)
	})
}

func (h hookedUserAuth) RequestEmailOTP(ctx context.Context, email, address string) (result userauth.EmailOTPRequest, err error) {
	return h.requestOTP(ctx, userauth.DefaultCollectionID, func() (userauth.EmailOTPRequest, error) { return h.Service.RequestEmailOTP(ctx, email, address) })
}
func (h hookedUserAuth) RequestEmailOTPInCollection(ctx context.Context, realm uuid.UUID, email, address string) (result userauth.EmailOTPRequest, err error) {
	return h.requestOTP(ctx, realm, func() (userauth.EmailOTPRequest, error) {
		return h.Service.RequestEmailOTPInCollection(ctx, realm, email, address)
	})
}
func (h hookedUserAuth) requestOTP(ctx context.Context, realm uuid.UUID, operation func() (userauth.EmailOTPRequest, error)) (result userauth.EmailOTPRequest, err error) {
	event, finish, err := h.begin(ctx, HookAuthOTPRequest, "otp.request", realm, uuid.Nil, "")
	defer func() { finish(err) }()
	if err != nil {
		return result, err
	}
	result, err = operation()
	if err == nil && !result.ExpiresAt.IsZero() {
		event.ChallengeExpiresAt = result.ExpiresAt
	}
	return result, err
}
func (h hookedUserAuth) CompleteEmailOTP(ctx context.Context, id, code string) (userauth.Session, error) {
	return h.session(ctx, HookAuthOTPComplete, "otp.complete", userauth.DefaultCollectionID, uuid.Nil, "", func() (userauth.Session, error) { return h.Service.CompleteEmailOTP(ctx, id, code) })
}
func (h hookedUserAuth) CompleteEmailOTPInCollection(ctx context.Context, realm uuid.UUID, id, code string) (userauth.Session, error) {
	return h.session(ctx, HookAuthOTPComplete, "otp.complete", realm, uuid.Nil, "", func() (userauth.Session, error) { return h.Service.CompleteEmailOTPInCollection(ctx, realm, id, code) })
}

func (h hookedUserAuth) RequestVerification(ctx context.Context, user uuid.UUID) error {
	return h.action(ctx, HookAuthVerificationRequest, "verification.request", uuid.Nil, user, func() error { return h.Service.RequestVerification(ctx, user) })
}
func (h hookedUserAuth) ConfirmVerification(ctx context.Context, token string) error {
	return h.action(ctx, HookAuthVerificationConfirm, "verification.confirm", uuid.Nil, uuid.Nil, func() error { return h.Service.ConfirmVerification(ctx, token) })
}
func (h hookedUserAuth) RequestPasswordReset(ctx context.Context, email, address string) error {
	return h.action(ctx, HookAuthPasswordResetRequest, "password_reset.request", userauth.DefaultCollectionID, uuid.Nil, func() error { return h.Service.RequestPasswordReset(ctx, email, address) })
}
func (h hookedUserAuth) RequestPasswordResetInCollection(ctx context.Context, realm uuid.UUID, email, address string) error {
	return h.action(ctx, HookAuthPasswordResetRequest, "password_reset.request", realm, uuid.Nil, func() error { return h.Service.RequestPasswordResetInCollection(ctx, realm, email, address) })
}
func (h hookedUserAuth) ConfirmPasswordReset(ctx context.Context, token, password string) error {
	return h.action(ctx, HookAuthPasswordResetConfirm, "password_reset.confirm", uuid.Nil, uuid.Nil, func() error { return h.Service.ConfirmPasswordReset(ctx, token, password) })
}
func (h hookedUserAuth) RequestEmailChange(ctx context.Context, user uuid.UUID, password, email string) error {
	return h.action(ctx, HookAuthEmailChangeRequest, "email_change.request", uuid.Nil, user, func() error { return h.Service.RequestEmailChange(ctx, user, password, email) })
}
func (h hookedUserAuth) ConfirmEmailChange(ctx context.Context, token string) error {
	return h.action(ctx, HookAuthEmailChangeConfirm, "email_change.confirm", uuid.Nil, uuid.Nil, func() error { return h.Service.ConfirmEmailChange(ctx, token) })
}
func (h hookedUserAuth) Impersonate(ctx context.Context, user uuid.UUID, admin string, duration time.Duration) (userauth.Session, error) {
	return h.session(ctx, HookAuthImpersonate, "impersonate", uuid.Nil, user, "", func() (userauth.Session, error) { return h.Service.Impersonate(ctx, user, admin, duration) })
}
