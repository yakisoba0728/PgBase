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
	"github.com/yakisoba0728/PgBase/internal/validation"
)

type errorKind uint8

const (
	errorValidation errorKind = iota + 1
	errorInvalidCredentials
	errorConflict
	errorRateLimited
	errorNotFound
	errorMFARequired
	errorPreconditionFailed
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
	case errorPreconditionFailed:
		return "stored state changed since it was read"
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
	ErrPreconditionFailed = &authError{kind: errorPreconditionFailed}
)

// fieldError keeps errors.Is(err, kind) working and names the offending
// request field for administrator responses (errors.As *validation.Error).
func fieldError(kind error, field, code string) error {
	return errors.Join(kind, &validation.Error{Details: []validation.Detail{{Field: field, Code: code}}})
}

// firstInvalid takes (field string, invalid bool) pairs and names the first
// invalid field, or "".
func firstInvalid(checks ...any) string {
	for i := 0; i+1 < len(checks); i += 2 {
		if checks[i+1].(bool) {
			return checks[i].(string)
		}
	}
	return ""
}

// RateLimitedError is ErrRateLimited with a known time until the exhausted
// budget resets, so HTTP callers can send an accurate Retry-After.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string        { return ErrRateLimited.Error() }
func (e *RateLimitedError) Is(target error) bool { return errors.Is(ErrRateLimited, target) }

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
	// Total is filled only when requested and stops counting at
	// maximumUserTotal, setting TotalCapped.
	Total       int
	TotalCapped bool
}

// UserQuery selects one page of users. Search is an exact user id or an exact
// (case-insensitive) email address.
type UserQuery struct {
	After            uuid.UUID
	Limit            int
	AuthCollectionID uuid.UUID
	Search           string
	Total            bool
}

type Service struct {
	pool                *database.Pool
	passwords           Passwords
	secret              []byte
	ttl                 time.Duration
	credentialSlots     chan struct{}
	passwordSlots       chan struct{}
	sessionSlots        chan struct{}
	mailer              mailer.Sender
	mailQueue           *mailer.Queue
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
		recordIdentities: recordidentity.New(nil), mailQueue: newMailQueue()}
}

// Mail that public, unauthenticated requests may cause is issued and sent by
// this queue. Its bounds keep an unreachable mail server from holding more
// than mailQueueWorkers connections, and requests beyond mailQueueCapacity
// unfinished jobs send nothing, exactly as an unknown address does.
const (
	mailQueueWorkers  = 4
	mailQueueCapacity = 256
	mailQueueTimeout  = time.Minute
)

func newMailQueue() *mailer.Queue {
	return mailer.NewQueue(mailQueueWorkers, mailQueueCapacity, mailQueueTimeout)
}

// WaitForQueuedMail blocks until the mail that requests have queued so far is
// issued and handed to the mail server, or ctx ends. Hosts call it before
// closing the database pool; tests call it before reading delivered mail.
func (s *Service) WaitForQueuedMail(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return s.mailQueue.Wait(ctx)
}
