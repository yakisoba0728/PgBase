package adminauth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
)

// Store persists administrator authentication state through the shared database boundary.
type Store struct {
	db database.DBTX
}

// NewStore creates an authentication store over a pool or transaction.
func NewStore(db database.DBTX) *Store { return &Store{db: db} }

// AdministratorCount returns the number of configured administrators.
func (s *Store) AdministratorCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRow(ctx, "select count(*) from pgbase_meta.admins").Scan(&count); err != nil {
		return 0, fmt.Errorf("count administrators: %w", err)
	}
	return count, nil
}

// CreateAdministrator persists an administrator and its password hash.
func (s *Store) CreateAdministrator(ctx context.Context, administrator Administrator, passwordHash string) (Administrator, error) {
	normalizedEmail, err := NormalizeEmail(administrator.Email)
	if err != nil {
		return Administrator{}, err
	}
	administrator.Email = normalizedEmail
	var created Administrator
	err = s.db.QueryRow(ctx, `
		insert into pgbase_meta.admins (id, email, password_hash, active, created_at, updated_at)
		values ($1, $2, $3, $4, $5, $6)
		returning id, email, active, created_at, updated_at`,
		administrator.ID, administrator.Email, passwordHash, administrator.Active,
		administrator.CreatedAt, administrator.UpdatedAt).Scan(
		&created.ID, &created.Email, &created.Active, &created.CreatedAt, &created.UpdatedAt)
	if err != nil {
		return Administrator{}, fmt.Errorf("create administrator: %w", err)
	}
	return administratorUTC(created), nil
}

// ResetAdministratorPassword replaces an active administrator password and
// deletes every existing session in the caller's transaction.
func (s *Store) ResetAdministratorPassword(ctx context.Context, email, passwordHash string, now time.Time) (Administrator, error) {
	normalizedEmail, err := NormalizeEmail(email)
	if err != nil {
		return Administrator{}, err
	}
	var administrator Administrator
	err = s.db.QueryRow(ctx, `
		with updated as (
			update pgbase_meta.admins
			set password_hash = $2, updated_at = $3
			where email = $1 and active
			returning id, email, active, created_at, updated_at
		), revoked as (
			delete from pgbase_meta.admin_sessions
			where admin_id in (select id from updated)
		)
		select id, email, active, created_at, updated_at from updated`, normalizedEmail, passwordHash, now).Scan(
		&administrator.ID, &administrator.Email, &administrator.Active, &administrator.CreatedAt, &administrator.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Administrator{}, ErrInvalidCredentials
	}
	if err != nil {
		return Administrator{}, fmt.Errorf("reset administrator password: %w", err)
	}
	return administratorUTC(administrator), nil
}

// AdministratorByEmail reads an active administrator and its encoded password hash.
func (s *Store) AdministratorByEmail(ctx context.Context, email string) (Administrator, string, error) {
	var administrator Administrator
	var passwordHash string
	err := s.db.QueryRow(ctx, `
		select id, email, active, created_at, updated_at, password_hash
		from pgbase_meta.admins
		where email = $1 and active`, email).Scan(
		&administrator.ID, &administrator.Email, &administrator.Active,
		&administrator.CreatedAt, &administrator.UpdatedAt, &passwordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Administrator{}, "", ErrInvalidCredentials
	}
	if err != nil {
		return Administrator{}, "", fmt.Errorf("read administrator: %w", err)
	}
	return administratorUTC(administrator), passwordHash, nil
}

// CreateSession persists only a derived token hash for a new session.
func (s *Store) CreateSession(ctx context.Context, id, administratorID uuid.UUID, tokenHash []byte, expiresAt, now time.Time) error {
	_, err := s.db.Exec(ctx, `
		insert into pgbase_meta.admin_sessions
			(id, admin_id, token_hash, expires_at, last_seen_at, revoked_at, created_at)
		values ($1, $2, $3, $4, $5, null, $5)`, id, administratorID, tokenHash, expiresAt, now)
	if err != nil {
		return fmt.Errorf("create administrator session: %w", err)
	}
	return nil
}

// AuthenticateSession resolves an active, unexpired, unrevoked session and updates last-seen time.
func (s *Store) AuthenticateSession(ctx context.Context, tokenHash []byte, now time.Time) (Administrator, time.Time, error) {
	var administrator Administrator
	var expiresAt time.Time
	err := s.db.QueryRow(ctx, `
		with matched as (
			update pgbase_meta.admin_sessions as sessions
			set last_seen_at = $2
			from pgbase_meta.admins as admins
			where sessions.token_hash = $1
			  and sessions.admin_id = admins.id
			  and sessions.revoked_at is null
			  and sessions.expires_at > $2
			  and admins.active
			returning admins.id, admins.email, admins.active, admins.created_at, admins.updated_at, sessions.expires_at
		)
		select id, email, active, created_at, updated_at, expires_at from matched`, tokenHash, now).Scan(
		&administrator.ID, &administrator.Email, &administrator.Active,
		&administrator.CreatedAt, &administrator.UpdatedAt, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Administrator{}, time.Time{}, ErrInvalidCredentials
	}
	if err != nil {
		return Administrator{}, time.Time{}, fmt.Errorf("authenticate administrator session: %w", err)
	}
	return administratorUTC(administrator), expiresAt.UTC(), nil
}

// RevokeSession revokes a session if it exists; repeated logout is harmless.
func (s *Store) RevokeSession(ctx context.Context, tokenHash []byte, now time.Time) error {
	if _, err := s.db.Exec(ctx, `
		update pgbase_meta.admin_sessions
		set revoked_at = coalesce(revoked_at, $2)
		where token_hash = $1`, tokenHash, now); err != nil {
		return fmt.Errorf("revoke administrator session: %w", err)
	}
	return nil
}

// LockRateLimit serializes decisions for one rate-limit subject within a transaction.
func (s *Store) LockRateLimit(ctx context.Context, key int64) error {
	if _, err := s.db.Exec(ctx, "select pg_advisory_xact_lock($1)", key); err != nil {
		return fmt.Errorf("lock administrator rate limit: %w", err)
	}
	return nil
}

// RateLimitBlocked reports whether a subject is currently blocked.
func (s *Store) RateLimitBlocked(ctx context.Context, subjectHash []byte, now time.Time) (bool, error) {
	var blocked bool
	err := s.db.QueryRow(ctx, `
		select coalesce(blocked_until > $2, false)
		from pgbase_meta.admin_rate_limits
		where subject_hash = $1`, subjectHash, now).Scan(&blocked)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read administrator rate limit: %w", err)
	}
	return blocked, nil
}

// RecordRateLimitFailure atomically adds a failure and starts a block on the fifth failure.
func (s *Store) RecordRateLimitFailure(ctx context.Context, subjectHash []byte, now time.Time) (bool, error) {
	var blocked bool
	err := s.db.QueryRow(ctx, `
		insert into pgbase_meta.admin_rate_limits as limits
			(subject_hash, window_started_at, failures, blocked_until, updated_at)
		values ($1, $2, 1, null, $2)
		on conflict (subject_hash) do update set
			window_started_at = case
				when limits.window_started_at <= $2 - interval '15 minutes' then $2
				else limits.window_started_at
			end,
			failures = case
				when limits.window_started_at <= $2 - interval '15 minutes' then 1
				else limits.failures + 1
			end,
			blocked_until = case
				when (case when limits.window_started_at <= $2 - interval '15 minutes' then 1 else limits.failures + 1 end) >= 5
				then greatest(coalesce(limits.blocked_until, '-infinity'::timestamptz), $2 + interval '15 minutes')
				else null
			end,
			updated_at = $2
		returning failures >= 5`, subjectHash, now).Scan(&blocked)
	if err != nil {
		return false, fmt.Errorf("record administrator rate-limit failure: %w", err)
	}
	return blocked, nil
}

// ClearRateLimit removes a subject's failure history after successful authentication.
func (s *Store) ClearRateLimit(ctx context.Context, subjectHash []byte) error {
	if _, err := s.db.Exec(ctx, "delete from pgbase_meta.admin_rate_limits where subject_hash = $1", subjectHash); err != nil {
		return fmt.Errorf("clear administrator rate limit: %w", err)
	}
	return nil
}

func administratorUTC(administrator Administrator) Administrator {
	administrator.CreatedAt = administrator.CreatedAt.UTC()
	administrator.UpdatedAt = administrator.UpdatedAt.UTC()
	return administrator
}
