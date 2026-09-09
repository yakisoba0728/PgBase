// Package userauth provides authentication for public, project-level users.
package userauth

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/mailer"
)

type errorKind uint8

const (
	errorValidation errorKind = iota + 1
	errorInvalidCredentials
	errorConflict
	errorRateLimited
	errorNotFound
	errorMFARequired
)

type authError struct{ kind errorKind }

func (e *authError) Error() string {
	switch e.kind {
	case errorValidation:
		return "user authentication validation failed"
	case errorInvalidCredentials:
		return "invalid user credentials"
	case errorConflict:
		return "user already exists"
	case errorRateLimited:
		return "user authentication rate limit exceeded"
	case errorNotFound:
		return "user not found"
	case errorMFARequired:
		return "multi-factor authentication required"
	default:
		return "user authentication failed"
	}
}

func (e *authError) Is(target error) bool {
	var other *authError
	return errors.As(target, &other) && e.kind == other.kind
}

var (
	ErrValidation         = &authError{kind: errorValidation}
	ErrInvalidCredentials = &authError{kind: errorInvalidCredentials}
	ErrConflict           = &authError{kind: errorConflict}
	ErrRateLimited        = &authError{kind: errorRateLimited}
	ErrNotFound           = &authError{kind: errorNotFound}
	ErrMFARequired        = &authError{kind: errorMFARequired}
)

type MFARequiredError struct {
	ChallengeToken string
	ExpiresAt      time.Time
}

func (e *MFARequiredError) Error() string        { return ErrMFARequired.Error() }
func (e *MFARequiredError) Is(target error) bool { return errors.Is(ErrMFARequired, target) }

type TOTPEnrollment struct {
	Secret     string
	OTPAuthURL string
}

type RecoveryCodes struct{ Codes []string }

type Passwords interface {
	Hash(password string) (string, error)
	Verify(encodedHash, password string) error
}

type User struct {
	ID               uuid.UUID
	AuthCollectionID uuid.UUID
	Email            string
	Verified         bool
	CreatedAt        time.Time
}

type Session struct {
	User                         User
	RawToken                     string
	ExpiresAt                    time.Time
	Impersonated                 bool
	ImpersonatingAdministratorID uuid.UUID
}

type Principal struct {
	User                         User
	ExpiresAt                    time.Time
	Impersonated                 bool
	ImpersonatingAdministratorID uuid.UUID
}

type ManagedUser struct {
	User
	Active    bool
	UpdatedAt time.Time
}

type UserPage struct {
	Items     []ManagedUser
	NextAfter uuid.UUID
}

type Service struct {
	pool                *database.Pool
	passwords           Passwords
	secret              []byte
	ttl                 time.Duration
	credentialSlots     chan struct{}
	passwordSlots       chan struct{}
	sessionSlots        chan struct{}
	cleanupCounter      atomic.Uint64
	mailer              mailer.Sender
	decorateMailer      func(mailer.Sender) mailer.Sender
	emailBaseURL        string
	emailActionTTL      time.Duration
	oauthProviders      map[string]OAuthProvider
	oauthRedirects      map[string]struct{}
	oauthStateTTL       time.Duration
	oauthRegistration   bool
	defaultRegistration bool
}

type OAuthIdentity struct {
	Subject                     string
	Email                       string
	EmailVerified               bool
	AvatarURL                   string
	AvatarAllowInsecureLoopback bool
}

type OAuthProvider interface {
	AuthorizationURL(state, challenge, redirectURI string) (string, error)
	Exchange(context.Context, string, string, string) (OAuthIdentity, error)
}

type OAuthAuthorization struct {
	AuthorizationURL string
	ExpiresAt        time.Time
}

func NewService(pool *database.Pool, passwords Passwords, secret []byte, ttl time.Duration) *Service {
	return &Service{pool: pool, passwords: passwords, secret: append([]byte(nil), secret...), ttl: ttl,
		credentialSlots: make(chan struct{}, 16), passwordSlots: make(chan struct{}, 2), sessionSlots: make(chan struct{}, 32), defaultRegistration: true}
}
