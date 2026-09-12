// Package userauth provides authentication for public, project-level users.
package userauth

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/mailer"
	"github.com/yakisoba0728/PgBase/internal/recordidentity"
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
	PublicID         string
	AuthCollectionID uuid.UUID
	Email            string
	EmailVisibility  bool
	Verified         bool
	CreatedAt        time.Time
	passwordEnabled  bool
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
	recordIdentities    *recordidentity.Service
	ratePolicy          atomic.Pointer[RatePolicy]
}

type RatePolicy struct {
	AccountMaxFailures, NetworkMaxFailures, RegistrationNetworkMaxFailures int
	Window, Block                                                          time.Duration
}

func (s *Service) ConfigureRatePolicy(policy RatePolicy) error {
	if policy.AccountMaxFailures < 1 || policy.NetworkMaxFailures < 1 || policy.RegistrationNetworkMaxFailures < 1 || policy.Window <= 0 || policy.Block <= 0 {
		return ErrValidation
	}
	s.ratePolicy.Store(&policy)
	return nil
}

func (s *Service) currentRatePolicy() RatePolicy {
	if policy := s.ratePolicy.Load(); policy != nil {
		return *policy
	}
	return RatePolicy{AccountMaxFailures: loginAccountLimit, NetworkMaxFailures: loginNetworkLimit, RegistrationNetworkMaxFailures: registerNetworkLimit, Window: 15 * time.Minute, Block: 15 * time.Minute}
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
	AuthorizationURL    string
	ExpiresAt           time.Time
	State               string
	CodeVerifier        string
	CodeChallenge       string
	CodeChallengeMethod string
}

func NewService(pool *database.Pool, passwords Passwords, secret []byte, ttl time.Duration) *Service {
	return &Service{pool: pool, passwords: passwords, secret: append([]byte(nil), secret...), ttl: ttl,
		credentialSlots: make(chan struct{}, 16), passwordSlots: make(chan struct{}, 2), sessionSlots: make(chan struct{}, 32), defaultRegistration: true,
		recordIdentities: recordidentity.New(nil)}
}
