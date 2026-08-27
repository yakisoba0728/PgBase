package adminauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const setupAdvisoryLockKey int64 = 0x7067626173650003

// Setup creates the only first administrator and its initial session.
func (s *Service) Setup(ctx context.Context, presentedSetupToken, email, password, subject string) (Session, error) {
	if err := s.ready(); err != nil {
		return Session{}, err
	}
	normalizedEmail, err := NormalizeEmail(email)
	if err != nil {
		return Session{}, err
	}
	now := s.clock.Now()
	subjectHash := rateLimitSubjectHash(s.tokenSecret, normalizedEmail, subject)
	var issued Session
	var operationErr error
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		store := NewStore(tx)
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", setupAdvisoryLockKey); err != nil {
			return fmt.Errorf("lock administrator setup: %w", err)
		}
		count, err := store.AdministratorCount(ctx)
		if err != nil {
			return err
		}
		if count != 0 {
			operationErr = ErrSetupUnavailable
			return nil
		}
		if err := store.LockRateLimit(ctx, advisoryKey(subjectHash)); err != nil {
			return err
		}
		blocked, err := store.RateLimitBlocked(ctx, subjectHash, now)
		if err != nil {
			return err
		}
		if blocked {
			operationErr = ErrRateLimited
			return nil
		}
		if !constantTimeStringEqual(s.setupToken, presentedSetupToken) {
			if _, err := store.RecordRateLimitFailure(ctx, subjectHash, now); err != nil {
				return err
			}
			operationErr = ErrInvalidCredentials
			return nil
		}
		passwordHash, err := s.passwords.Hash(password)
		if err != nil {
			if errors.Is(err, ErrValidation) {
				operationErr = ErrValidation
				return nil
			}
			return fmt.Errorf("hash administrator password: %w", err)
		}
		administrator := Administrator{
			ID: uuid.New(), Email: normalizedEmail, Active: true, CreatedAt: now, UpdatedAt: now,
		}
		administrator, err = store.CreateAdministrator(ctx, administrator, passwordHash)
		if err != nil {
			return err
		}
		rawToken, err := GenerateToken()
		if err != nil {
			return err
		}
		expiresAt := now.Add(s.sessionTTL)
		if err := store.CreateSession(ctx, uuid.New(), administrator.ID, HashSessionToken(s.tokenSecret, rawToken), expiresAt, now); err != nil {
			return err
		}
		if err := store.ClearRateLimit(ctx, subjectHash); err != nil {
			return err
		}
		issued = Session{Administrator: administrator, RawToken: rawToken, ExpiresAt: expiresAt}
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	if operationErr != nil {
		return Session{}, operationErr
	}
	return issued, nil
}

// Login verifies an administrator password and issues a new session.
func (s *Service) Login(ctx context.Context, email, password, subject string) (Session, error) {
	if err := s.ready(); err != nil {
		return Session{}, err
	}
	normalizedEmail, normalizationErr := NormalizeEmail(email)
	if normalizationErr != nil {
		normalizedEmail = strings.ToLower(strings.TrimSpace(email))
	}
	now := s.clock.Now()
	subjectHash := rateLimitSubjectHash(s.tokenSecret, normalizedEmail, subject)
	var issued Session
	var operationErr error
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		store := NewStore(tx)
		if err := store.LockRateLimit(ctx, advisoryKey(subjectHash)); err != nil {
			return err
		}
		blocked, err := store.RateLimitBlocked(ctx, subjectHash, now)
		if err != nil {
			return err
		}
		if blocked {
			operationErr = ErrRateLimited
			return nil
		}
		administrator, passwordHash, lookupErr := store.AdministratorByEmail(ctx, normalizedEmail)
		credentialsValid := normalizationErr == nil && lookupErr == nil
		if credentialsValid {
			credentialsValid = s.passwords.Verify(passwordHash, password) == nil
		}
		if !credentialsValid {
			if lookupErr != nil && !errors.Is(lookupErr, ErrInvalidCredentials) {
				return lookupErr
			}
			if _, err := store.RecordRateLimitFailure(ctx, subjectHash, now); err != nil {
				return err
			}
			operationErr = ErrInvalidCredentials
			return nil
		}
		rawToken, err := GenerateToken()
		if err != nil {
			return err
		}
		expiresAt := now.Add(s.sessionTTL)
		if err := store.CreateSession(ctx, uuid.New(), administrator.ID, HashSessionToken(s.tokenSecret, rawToken), expiresAt, now); err != nil {
			return err
		}
		if err := store.ClearRateLimit(ctx, subjectHash); err != nil {
			return err
		}
		issued = Session{Administrator: administrator, RawToken: rawToken, ExpiresAt: expiresAt}
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	if operationErr != nil {
		return Session{}, operationErr
	}
	return issued, nil
}

// Authenticate resolves a raw session token without persisting the raw material.
func (s *Service) Authenticate(ctx context.Context, rawToken string) (Administrator, error) {
	if err := s.ready(); err != nil {
		return Administrator{}, err
	}
	return NewStore(s.pool).AuthenticateSession(ctx, HashSessionToken(s.tokenSecret, rawToken), s.clock.Now())
}

// Logout revokes a raw session token. It is idempotent.
func (s *Service) Logout(ctx context.Context, rawToken string) error {
	if err := s.ready(); err != nil {
		return err
	}
	return NewStore(s.pool).RevokeSession(ctx, HashSessionToken(s.tokenSecret, rawToken), s.clock.Now())
}

func (s *Service) ready() error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("administrator authentication pool is nil")
	}
	if s.passwords == nil {
		return fmt.Errorf("administrator password service is nil")
	}
	if len(s.tokenSecret) == 0 || s.sessionTTL <= 0 {
		return ErrValidation
	}
	return nil
}

func constantTimeStringEqual(expected, actual string) bool {
	expectedHash := sha256.Sum256([]byte(expected))
	actualHash := sha256.Sum256([]byte(actual))
	return subtle.ConstantTimeCompare(expectedHash[:], actualHash[:]) == 1
}

func advisoryKey(hash []byte) int64 {
	return int64(binary.BigEndian.Uint64(hash[:8]))
}
