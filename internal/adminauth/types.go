// Package adminauth provides administrator credentials, sessions, and shared rate limiting.
package adminauth

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/mailer"
)

type ErrorKind uint8

const (
	ErrorKindValidation ErrorKind = iota + 1
	ErrorKindInvalidCredentials
	ErrorKindSetupUnavailable
	ErrorKindRateLimited
	ErrorKindConflict
	ErrorKindOTPRequired
	ErrorKindIPRestricted
)

type Error struct {
	Kind ErrorKind
}

func (e *Error) Error() string {
	switch e.Kind {
	case ErrorKindValidation:
		return "administrator authentication validation failed"
	case ErrorKindInvalidCredentials:
		return "invalid administrator credentials"
	case ErrorKindSetupUnavailable:
		return "administrator setup is unavailable"
	case ErrorKindRateLimited:
		return "administrator authentication rate limit exceeded"
	case ErrorKindConflict:
		return "administrator already exists"
	case ErrorKindOTPRequired:
		return "administrator OTP is required"
	case ErrorKindIPRestricted:
		return "administrator network is not allowed"
	default:
		return "administrator authentication failed"
	}
}

func (e *Error) Is(target error) bool {
	var other *Error
	return errors.As(target, &other) && e.Kind == other.Kind
}

var (
	ErrValidation         = &Error{Kind: ErrorKindValidation}
	ErrInvalidCredentials = &Error{Kind: ErrorKindInvalidCredentials}
	ErrSetupUnavailable   = &Error{Kind: ErrorKindSetupUnavailable}
	ErrRateLimited        = &Error{Kind: ErrorKindRateLimited}
	ErrConflict           = &Error{Kind: ErrorKindConflict}
	ErrOTPRequired        = &Error{Kind: ErrorKindOTPRequired}
	ErrIPRestricted       = &Error{Kind: ErrorKindIPRestricted}
)

// Administrator is an administrator account safe to expose to authenticated callers.
type Administrator struct {
	ID        uuid.UUID
	Email     string
	Active    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Session is a newly issued administrator session. RawToken is returned only at creation.
type Session struct {
	Administrator Administrator
	RawToken      string
	ExpiresAt     time.Time
	// CSRFToken is derived here rather than by the transport so the token
	// secret never has to leave this module. A caller that can prove a session
	// already holds everything needed to answer a CSRF challenge for it.
	CSRFToken string
}

// Principal is an authenticated session as the transport sees it. It carries
// the CSRF token for the same reason Session does.
type Principal struct {
	Administrator Administrator
	ExpiresAt     time.Time
	CSRFToken     string
}

type Passwords interface {
	Hash(password string) (string, error)
	Verify(encodedHash, password string) error
}

type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func normalizedClock(clock Clock) Clock {
	if clock == nil {
		return systemClock{}
	}
	return clock
}

type Service struct {
	pool            *database.Pool
	passwords       Passwords
	tokenSecret     []byte
	setupToken      string
	sessionTTL      time.Duration
	clock           Clock
	credentialSlots chan struct{}
	sessionSlots    chan struct{}
	probeSlots      chan struct{}
	passwordSlots   chan struct{}
	resetMailer     mailer.Sender
	resetBaseURL    string
	resetTTL        time.Duration
	emailOTPMailer  mailer.Sender
	emailOTPTTL     time.Duration
}

// EmailOTP is an administrator one-time credential. Code is only returned to
// the explicit local CLI issuance flow and is never persisted in plaintext.
type EmailOTP struct {
	ID        string
	Code      string
	ExpiresAt time.Time
}

const passwordWorkConcurrency = 2

// Credential and session work have independent budgets so a password-KDF
// backlog cannot starve established sessions. The HTTP layer coalesces probes.
const (
	credentialAdmissionConcurrency = 16
	sessionAdmissionConcurrency    = 16
	setupProbeAdmissionConcurrency = 1
)

func NewService(pool *database.Pool, passwords Passwords, tokenSecret []byte, setupToken string, sessionTTL time.Duration, clock Clock) *Service {
	return &Service{
		pool:            pool,
		passwords:       passwords,
		tokenSecret:     append([]byte(nil), tokenSecret...),
		setupToken:      setupToken,
		sessionTTL:      sessionTTL,
		clock:           normalizedClock(clock),
		credentialSlots: make(chan struct{}, credentialAdmissionConcurrency),
		sessionSlots:    make(chan struct{}, sessionAdmissionConcurrency),
		probeSlots:      make(chan struct{}, setupProbeAdmissionConcurrency),
		passwordSlots:   make(chan struct{}, passwordWorkConcurrency),
	}
}
