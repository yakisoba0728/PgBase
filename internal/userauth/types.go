// Package userauth provides authentication for public, project-level users.
package userauth

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
)

type errorKind uint8

const (
	errorValidation errorKind = iota + 1
	errorInvalidCredentials
	errorConflict
	errorRateLimited
	errorNotFound
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
)

type Passwords interface {
	Hash(password string) (string, error)
	Verify(encodedHash, password string) error
}

type User struct {
	ID        uuid.UUID
	Email     string
	CreatedAt time.Time
}

type Session struct {
	User      User
	RawToken  string
	ExpiresAt time.Time
}

type Principal struct {
	User      User
	ExpiresAt time.Time
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
	pool            *database.Pool
	passwords       Passwords
	secret          []byte
	ttl             time.Duration
	credentialSlots chan struct{}
	passwordSlots   chan struct{}
	sessionSlots    chan struct{}
	cleanupCounter  atomic.Uint64
}

func NewService(pool *database.Pool, passwords Passwords, secret []byte, ttl time.Duration) *Service {
	return &Service{pool: pool, passwords: passwords, secret: append([]byte(nil), secret...), ttl: ttl,
		credentialSlots: make(chan struct{}, 16), passwordSlots: make(chan struct{}, 2), sessionSlots: make(chan struct{}, 32)}
}
