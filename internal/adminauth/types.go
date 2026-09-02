// Package adminauth provides administrator credentials, sessions, and shared rate limiting.
package adminauth

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
)

type ErrorKind uint8

const (
	ErrorKindValidation ErrorKind = iota + 1
	ErrorKindInvalidCredentials
	ErrorKindSetupUnavailable
	ErrorKindRateLimited
	ErrorKindConflict
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
	pool        *database.Pool
	passwords   Passwords
	tokenSecret []byte
	setupToken  string
	sessionTTL  time.Duration
	clock       Clock
	verifySlots chan struct{}
}

const passwordVerificationConcurrency = 2

func NewService(pool *database.Pool, passwords Passwords, tokenSecret []byte, setupToken string, sessionTTL time.Duration, clock Clock) *Service {
	return &Service{
		pool:        pool,
		passwords:   passwords,
		tokenSecret: append([]byte(nil), tokenSecret...),
		setupToken:  setupToken,
		sessionTTL:  sessionTTL,
		clock:       normalizedClock(clock),
		verifySlots: make(chan struct{}, passwordVerificationConcurrency),
	}
}
