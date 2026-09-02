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
const dummyPassword = "pgbase dummy credential 2026"

const (
	accountFailureLimit = 5
	networkFailureLimit = 20
)

type rateLimitBucket struct {
	hash           []byte
	limit          int
	clearOnSuccess bool
}

type loginCandidate struct {
	administrator Administrator
	passwordHash  string
}

// loginTraceStage and loginTraceContextKey form an unexported characterization seam.
// Only code in this package can attach a callback, and ordinary request
// contexts therefore take the nil/no-op path.
type loginTraceStage string

const (
	loginStageNetworkAdmission   loginTraceStage = "network admission complete; legacy global bucket ignored"
	loginStageFailureSignalRead  loginTraceStage = "email failure signal short read complete"
	loginStageDelayNone          loginTraceStage = "delay class: 0s"
	loginStageDelay250MS         loginTraceStage = "delay class: 250ms"
	loginStageDelay500MS         loginTraceStage = "delay class: 500ms"
	loginStageDelay1S            loginTraceStage = "delay class: 1s"
	loginStageKDFSlotAcquired    loginTraceStage = "KDF slot acquired"
	loginStageCandidateHashRead  loginTraceStage = "candidate hash short read complete"
	loginStageRealKDF            loginTraceStage = "real KDF complete"
	loginStageDummyKDF           loginTraceStage = "dummy KDF complete"
	loginStageKDFSlotReleased    loginTraceStage = "KDF slot released"
	loginStageAdministratorCheck loginTraceStage = "administrator hash and active recheck complete"
	loginStageSessionInsert      loginTraceStage = "session insert complete"
	loginStageLimiterCleanup     loginTraceStage = "successful limiter cleanup complete"
	loginStageFailureRecorded    loginTraceStage = "failure limiter signal recorded"
)

type loginTraceContextKey struct{}

func traceLogin(ctx context.Context, stage loginTraceStage) {
	if ctx == nil {
		return
	}
	trace, _ := ctx.Value(loginTraceContextKey{}).(func(loginTraceStage))
	if trace != nil {
		trace(stage)
	}
}

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
		issued = Session{Administrator: administrator, RawToken: rawToken, ExpiresAt: expiresAt,
			CSRFToken: DeriveCSRFToken(s.tokenSecret, rawToken)}
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
		store := NewStore(tx)
		administrator, resetErr = store.ResetAdministratorPassword(ctx, normalizedEmail, passwordHash, now)
		if resetErr != nil {
			return resetErr
		}
		emailHash := rateLimitSubjectHash(s.tokenSecret, "login", "account", normalizedEmail)
		if err := store.LockRateLimits(ctx, []int64{advisoryKey(emailHash)}); err != nil {
			return err
		}
		return store.ClearRateLimit(ctx, emailHash)
	})
	if err != nil {
		return Administrator{}, err
	}
	return administrator, nil
}

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

func (s *Service) Login(ctx context.Context, email, password, subject string) (Session, error) {
	if err := s.ready(); err != nil {
		return Session{}, err
	}
	normalizedEmail, normalizationErr := NormalizeEmail(email)
	if normalizationErr != nil {
		normalizedEmail = strings.ToLower(strings.TrimSpace(email))
	}
	now := s.clock.Now()
	emailBucket := rateLimitBucket{
		hash:           rateLimitSubjectHash(s.tokenSecret, "login", "account", normalizedEmail),
		clearOnSuccess: true,
	}
	networkBucket := rateLimitBucket{
		hash:  rateLimitSubjectHash(s.tokenSecret, "login", "network", subject),
		limit: networkFailureLimit,
	}
	if err := s.admitRateLimit(ctx, []rateLimitBucket{networkBucket}, now); err != nil {
		return Session{}, err
	}
	traceLogin(ctx, loginStageNetworkAdmission)
	failures, err := s.loginFailureCount(ctx, emailBucket.hash, now)
	if err != nil {
		return Session{}, err
	}
	traceLogin(ctx, loginStageFailureSignalRead)
	if err := waitLoginFailureDelay(ctx, failures); err != nil {
		return Session{}, err
	}
	candidate, valid, err := s.verifyLoginCandidate(ctx, normalizedEmail, normalizationErr, password)
	if err != nil {
		return Session{}, err
	}
	if !valid {
		if err := s.recordLoginFailure(ctx, emailBucket.hash, now); err != nil {
			return Session{}, err
		}
		traceLogin(ctx, loginStageFailureRecorded)
		return Session{}, ErrInvalidCredentials
	}

	buckets := []rateLimitBucket{emailBucket, networkBucket}
	var issued Session
	var operationErr error
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		store := NewStore(tx)
		administrator, passwordHash, lookupErr := store.AdministratorByEmailForUpdate(ctx, normalizedEmail)
		if lookupErr != nil && !errors.Is(lookupErr, ErrInvalidCredentials) {
			return lookupErr
		}
		candidateStillValid := lookupErr == nil && administrator.ID == candidate.administrator.ID &&
			constantTimeStringEqual(passwordHash, candidate.passwordHash)
		traceLogin(ctx, loginStageAdministratorCheck)
		if !candidateStillValid {
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
		traceLogin(ctx, loginStageSessionInsert)
		if err := clearSuccessfulRateLimits(ctx, store, buckets); err != nil {
			return err
		}
		traceLogin(ctx, loginStageLimiterCleanup)
		issued = Session{Administrator: administrator, RawToken: rawToken, ExpiresAt: expiresAt,
			CSRFToken: DeriveCSRFToken(s.tokenSecret, rawToken)}
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	if operationErr != nil {
		if errors.Is(operationErr, ErrInvalidCredentials) {
			if err := s.recordLoginFailure(ctx, emailBucket.hash, now); err != nil {
				return Session{}, err
			}
			traceLogin(ctx, loginStageFailureRecorded)
		}
		return Session{}, operationErr
	}
	return issued, nil
}

// Authenticate resolves a raw session token without persisting the raw material.
func (s *Service) Authenticate(ctx context.Context, rawToken string) (Principal, error) {
	if err := s.ready(); err != nil {
		return Principal{}, err
	}
	administrator, expiresAt, err := NewStore(s.pool).AuthenticateSession(
		ctx, HashSessionToken(s.tokenSecret, rawToken), s.clock.Now())
	if err != nil {
		return Principal{}, err
	}
	return Principal{
		Administrator: administrator,
		ExpiresAt:     expiresAt,
		CSRFToken:     DeriveCSRFToken(s.tokenSecret, rawToken),
	}, nil
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

func (s *Service) acquirePasswordVerification(ctx context.Context) (func(), error) {
	select {
	case s.verifySlots <- struct{}{}:
		return func() { <-s.verifySlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Service) verifyLoginCandidate(
	ctx context.Context,
	normalizedEmail string,
	normalizationErr error,
	password string,
) (loginCandidate, bool, error) {
	release, err := s.acquirePasswordVerification(ctx)
	if err != nil {
		return loginCandidate{}, false, err
	}
	traceLogin(ctx, loginStageKDFSlotAcquired)
	defer func() {
		release()
		traceLogin(ctx, loginStageKDFSlotReleased)
	}()

	administrator, passwordHash, lookupErr := NewStore(s.pool).AdministratorByEmail(ctx, normalizedEmail)
	if lookupErr != nil && !errors.Is(lookupErr, ErrInvalidCredentials) {
		return loginCandidate{}, false, lookupErr
	}
	traceLogin(ctx, loginStageCandidateHashRead)
	candidate := loginCandidate{administrator: administrator, passwordHash: passwordHash}
	credentialShapeValid := normalizationErr == nil && lookupErr == nil && validatePassword(password) == nil
	dummy := false
	if _, _, ok := parsePasswordHash(passwordHash); !credentialShapeValid || !ok {
		passwordHash = dummyPasswordHash
		password = dummyPassword
		credentialShapeValid = false
		dummy = true
	}
	verificationErr := s.passwords.Verify(passwordHash, password)
	if dummy {
		traceLogin(ctx, loginStageDummyKDF)
	} else {
		traceLogin(ctx, loginStageRealKDF)
	}
	if verificationErr != nil && !errors.Is(verificationErr, ErrInvalidCredentials) {
		return loginCandidate{}, false, verificationErr
	}
	return candidate, credentialShapeValid && verificationErr == nil, nil
}

func (s *Service) loginFailureCount(ctx context.Context, emailHash []byte, now time.Time) (int, error) {
	failures := 0
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		store := NewStore(tx)
		if err := store.LockRateLimits(ctx, []int64{advisoryKey(emailHash)}); err != nil {
			return err
		}
		var err error
		failures, err = store.RateLimitFailures(ctx, emailHash, now)
		return err
	})
	return failures, err
}

func (s *Service) recordLoginFailure(ctx context.Context, emailHash []byte, now time.Time) error {
	return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		store := NewStore(tx)
		if err := store.LockRateLimits(ctx, []int64{advisoryKey(emailHash)}); err != nil {
			return err
		}
		return store.RecordRateLimitSignal(ctx, emailHash, now)
	})
}

func waitLoginFailureDelay(ctx context.Context, failures int) error {
	var delay time.Duration
	switch {
	case failures >= 5:
		delay = time.Second
		traceLogin(ctx, loginStageDelay1S)
	case failures == 4:
		delay = 500 * time.Millisecond
		traceLogin(ctx, loginStageDelay500MS)
	case failures == 3:
		delay = 250 * time.Millisecond
		traceLogin(ctx, loginStageDelay250MS)
	default:
		traceLogin(ctx, loginStageDelayNone)
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) rateLimitBuckets(operation, normalizedEmail, subject string) []rateLimitBucket {
	buckets := []rateLimitBucket{
		{hash: rateLimitSubjectHash(s.tokenSecret, operation, "account", normalizedEmail), limit: accountFailureLimit, clearOnSuccess: true},
		{hash: rateLimitSubjectHash(s.tokenSecret, operation, "network", subject), limit: networkFailureLimit},
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
