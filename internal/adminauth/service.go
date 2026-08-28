package adminauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
)

const setupAdvisoryLockKey int64 = 0x7067626173650003

const dummyPasswordHash = "$argon2id$v=19$m=65536,t=3,p=4$cGdiYXNlLWR1bW15LXYxIQ$AnRqzUvZayQh6Siwo8bh8R9TAt8al8dpPzH25DzI5Tk"

const (
	accountFailureLimit = 5
	networkFailureLimit = 20
	globalFailureLimit  = 1000
)

type rateLimitBucket struct {
	hash           []byte
	limit          int
	clearOnSuccess bool
}

// Setup creates the only first administrator and its initial session.
func (s *Service) Setup(ctx context.Context, presentedSetupToken, email, password, subject string) (Session, error) {
	if err := s.ready(); err != nil {
		return Session{}, err
	}
	if s.setupToken == "" {
		return Session{}, ErrSetupUnavailable
	}
	normalizedEmail, err := NormalizeEmail(email)
	if err != nil {
		return Session{}, err
	}
	now := s.clock.Now()
	buckets := s.rateLimitBuckets("setup", normalizedEmail, subject)
	required, err := s.SetupRequired(ctx)
	if err != nil {
		return Session{}, err
	}
	if !required {
		return Session{}, ErrSetupUnavailable
	}
	if err := s.admitRateLimit(ctx, buckets, now); err != nil {
		return Session{}, err
	}
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
		if !constantTimeStringEqual(s.setupToken, presentedSetupToken) {
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
		administratorID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate administrator ID: %w", err)
		}
		administrator := Administrator{
			ID: administratorID, Email: normalizedEmail, Active: true, CreatedAt: now, UpdatedAt: now,
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
		if err := clearSuccessfulRateLimits(ctx, store, buckets); err != nil {
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

// CreateAdministrator creates an active administrator without issuing a session.
// It is intended for local administrator recovery through the CLI.
func (s *Service) CreateAdministrator(ctx context.Context, email, password string) (Administrator, error) {
	if err := s.ready(); err != nil {
		return Administrator{}, err
	}
	normalizedEmail, err := NormalizeEmail(email)
	if err != nil {
		return Administrator{}, err
	}
	passwordHash, err := s.passwords.Hash(password)
	if err != nil {
		return Administrator{}, err
	}
	now := s.clock.Now().UTC()
	administratorID, err := uuid.NewV7()
	if err != nil {
		return Administrator{}, fmt.Errorf("generate administrator ID: %w", err)
	}
	administrator, err := NewStore(s.pool).CreateAdministrator(ctx, Administrator{
		ID: administratorID, Email: normalizedEmail, Active: true, CreatedAt: now, UpdatedAt: now,
	}, passwordHash)
	if database.Classify(err) == database.KindConflict {
		return Administrator{}, ErrConflict
	}
	if err != nil {
		return Administrator{}, err
	}
	return administrator, nil
}

// ResetAdministratorPassword replaces an active administrator password and
// revokes all of that administrator's sessions without issuing a replacement.
func (s *Service) ResetAdministratorPassword(ctx context.Context, email, password string) (Administrator, error) {
	if err := s.ready(); err != nil {
		return Administrator{}, err
	}
	normalizedEmail, err := NormalizeEmail(email)
	if err != nil {
		return Administrator{}, err
	}
	passwordHash, err := s.passwords.Hash(password)
	if err != nil {
		return Administrator{}, err
	}
	now := s.clock.Now().UTC()
	var administrator Administrator
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var resetErr error
		administrator, resetErr = NewStore(tx).ResetAdministratorPassword(ctx, normalizedEmail, passwordHash, now)
		return resetErr
	})
	if err != nil {
		return Administrator{}, err
	}
	return administrator, nil
}

// SetupRequired reports whether the database has no configured administrator.
func (s *Service) SetupRequired(ctx context.Context) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	count, err := NewStore(s.pool).AdministratorCount(ctx)
	if err != nil {
		return false, err
	}
	return count == 0, nil
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
	buckets := s.rateLimitBuckets("login", normalizedEmail, subject)
	if err := s.admitRateLimit(ctx, buckets, now); err != nil {
		return Session{}, err
	}
	var issued Session
	var operationErr error
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		store := NewStore(tx)
		administrator, passwordHash, lookupErr := store.AdministratorByEmailForUpdate(ctx, normalizedEmail)
		if lookupErr != nil && !errors.Is(lookupErr, ErrInvalidCredentials) {
			return lookupErr
		}
		if normalizationErr != nil || lookupErr != nil {
			passwordHash = dummyPasswordHash
		}
		verificationErr := s.verifyPassword(ctx, passwordHash, password)
		if verificationErr != nil && !errors.Is(verificationErr, ErrInvalidCredentials) {
			return verificationErr
		}
		credentialsValid := normalizationErr == nil && lookupErr == nil && verificationErr == nil
		if !credentialsValid {
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
		if err := clearSuccessfulRateLimits(ctx, store, buckets); err != nil {
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
func (s *Service) Authenticate(ctx context.Context, rawToken string) (Administrator, time.Time, error) {
	if err := s.ready(); err != nil {
		return Administrator{}, time.Time{}, err
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
	if len(s.tokenSecret) == 0 || s.sessionTTL <= 0 || s.verifySlots == nil {
		return ErrValidation
	}
	return nil
}

func (s *Service) verifyPassword(ctx context.Context, encodedHash, password string) error {
	select {
	case s.verifySlots <- struct{}{}:
		defer func() { <-s.verifySlots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.passwords.Verify(encodedHash, password)
}

func (s *Service) rateLimitBuckets(operation, normalizedEmail, subject string) []rateLimitBucket {
	buckets := []rateLimitBucket{
		{hash: rateLimitSubjectHash(s.tokenSecret, operation, "account", normalizedEmail), limit: accountFailureLimit, clearOnSuccess: true},
		{hash: rateLimitSubjectHash(s.tokenSecret, operation, "network", subject), limit: networkFailureLimit},
		{hash: rateLimitSubjectHash(s.tokenSecret, operation, "global", "v1"), limit: globalFailureLimit},
	}
	if operation == "setup" {
		for index := range buckets {
			buckets[index].clearOnSuccess = true
		}
	}
	return buckets
}

func (s *Service) admitRateLimit(ctx context.Context, buckets []rateLimitBucket, now time.Time) error {
	var operationErr error
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		store := NewStore(tx)
		if err := store.LockRateLimits(ctx, rateLimitLockKeys(buckets)); err != nil {
			return err
		}
		blocked, err := rateLimitBlocked(ctx, store, buckets, now)
		if err != nil {
			return err
		}
		if blocked {
			operationErr = ErrRateLimited
			return nil
		}
		return recordRateLimitAttempt(ctx, store, buckets, now)
	})
	if err != nil {
		return err
	}
	return operationErr
}

func rateLimitLockKeys(buckets []rateLimitBucket) []int64 {
	keys := make([]int64, 0, len(buckets))
	seen := make(map[int64]struct{}, len(buckets))
	for _, bucket := range buckets {
		key := advisoryKey(bucket.hash)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func rateLimitBlocked(ctx context.Context, store *Store, buckets []rateLimitBucket, now time.Time) (bool, error) {
	for _, bucket := range buckets {
		blocked, err := store.RateLimitBlocked(ctx, bucket.hash, now)
		if err != nil {
			return false, err
		}
		if blocked {
			return true, nil
		}
	}
	return false, nil
}

func recordRateLimitAttempt(ctx context.Context, store *Store, buckets []rateLimitBucket, now time.Time) error {
	for _, bucket := range buckets {
		if _, err := store.RecordRateLimitFailure(ctx, bucket.hash, now, bucket.limit); err != nil {
			return err
		}
	}
	return nil
}

func clearSuccessfulRateLimits(ctx context.Context, store *Store, buckets []rateLimitBucket) error {
	if err := store.LockRateLimits(ctx, rateLimitLockKeys(buckets)); err != nil {
		return err
	}
	for _, bucket := range buckets {
		if bucket.clearOnSuccess {
			if err := store.ClearRateLimit(ctx, bucket.hash); err != nil {
				return err
			}
		} else if err := store.ReleaseRateLimitAttempt(ctx, bucket.hash); err != nil {
			return err
		}
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
