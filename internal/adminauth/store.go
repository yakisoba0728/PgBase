package adminauth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
)

type Store struct {
	db database.DBTX
}

func NewStore(db database.DBTX) *Store { return &Store{db: db} }

func (s *Store) AdministratorCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRow(ctx, "select count(*) from pgbase_meta.admins").Scan(&count); err != nil {
		return 0, fmt.Errorf("count administrators: %w", err)
	}
	return count, nil
}

func (s *Store) ActiveAdministratorCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRow(ctx, "select count(*) from pgbase_meta.admins where active").Scan(&count); err != nil {
		return 0, fmt.Errorf("count active administrators: %w", err)
	}
	return count, nil
}

func (s *Store) DeleteAdministrator(ctx context.Context, email string) error {
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(ctx, "delete from pgbase_meta.admins where email = $1 and active", normalized)
	if err != nil {
		return fmt.Errorf("delete administrator: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrInvalidCredentials
	}
	return nil
}

func (s *Store) AdministratorID(ctx context.Context, email string) (uuid.UUID, error) {
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	if err := s.db.QueryRow(ctx, "select id from pgbase_meta.admins where email = $1 and active", normalized).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrInvalidCredentials
	} else if err != nil {
		return uuid.Nil, fmt.Errorf("read administrator ID: %w", err)
	}
	return id, nil
}

func (s *Store) SetTOTP(ctx context.Context, administratorID uuid.UUID, ciphertext []byte, now time.Time) error {
	_, err := s.db.Exec(ctx, `insert into pgbase_meta.admin_totp(administrator_id,secret_ciphertext,enabled_at)
		values ($1,$2,$3) on conflict(administrator_id) do update set secret_ciphertext=excluded.secret_ciphertext,enabled_at=excluded.enabled_at`, administratorID, ciphertext, now)
	if err != nil {
		return fmt.Errorf("set administrator OTP: %w", err)
	}
	return nil
}

func (s *Store) TOTP(ctx context.Context, administratorID uuid.UUID) ([]byte, bool, error) {
	var secret []byte
	err := s.db.QueryRow(ctx, "select secret_ciphertext from pgbase_meta.admin_totp where administrator_id=$1", administratorID).Scan(&secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read administrator OTP: %w", err)
	}
	return secret, true, nil
}

func (s *Store) DeleteTOTP(ctx context.Context, administratorID uuid.UUID) error {
	if _, err := s.db.Exec(ctx, "delete from pgbase_meta.admin_totp where administrator_id=$1", administratorID); err != nil {
		return fmt.Errorf("delete administrator OTP: %w", err)
	}
	return nil
}

func (s *Store) DeleteAdministratorSessions(ctx context.Context, administratorID uuid.UUID) error {
	if _, err := s.db.Exec(ctx, "delete from pgbase_meta.admin_sessions where admin_id=$1", administratorID); err != nil {
		return fmt.Errorf("revoke administrator sessions: %w", err)
	}
	return nil
}

func (s *Store) AddIPPrefix(ctx context.Context, administratorID uuid.UUID, prefix string) error {
	if _, err := s.db.Exec(ctx, "insert into pgbase_meta.admin_ip_allowlist(administrator_id,prefix) values($1,$2::inet) on conflict do nothing", administratorID, prefix); err != nil {
		return fmt.Errorf("allow administrator IP prefix: %w", err)
	}
	return nil
}

func (s *Store) DeleteIPPrefix(ctx context.Context, administratorID uuid.UUID, prefix string) error {
	if _, err := s.db.Exec(ctx, "delete from pgbase_meta.admin_ip_allowlist where administrator_id=$1 and prefix=$2::inet", administratorID, prefix); err != nil {
		return fmt.Errorf("deny administrator IP prefix: %w", err)
	}
	return nil
}

func (s *Store) IPPrefixes(ctx context.Context, administratorID uuid.UUID) ([]string, error) {
	rows, err := s.db.Query(ctx, "select prefix::text from pgbase_meta.admin_ip_allowlist where administrator_id=$1 order by family(prefix),masklen(prefix),prefix", administratorID)
	if err != nil {
		return nil, fmt.Errorf("list administrator IP prefixes: %w", err)
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list administrator IP prefixes: %w", err)
	}
	return values, nil
}

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
	var administratorID uuid.UUID
	err = s.db.QueryRow(ctx, `
		select id
		from pgbase_meta.admins
		where email = $1 and active
		for update`, normalizedEmail).Scan(&administratorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Administrator{}, ErrInvalidCredentials
	}
	if err != nil {
		return Administrator{}, fmt.Errorf("lock administrator for password reset: %w", err)
	}

	var administrator Administrator
	err = s.db.QueryRow(ctx, `
		update pgbase_meta.admins
		set password_hash = $2, updated_at = $3
		where id = $1 and active
		returning id, email, active, created_at, updated_at`, administratorID, passwordHash, now).Scan(
		&administrator.ID, &administrator.Email, &administrator.Active, &administrator.CreatedAt, &administrator.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Administrator{}, ErrInvalidCredentials
	}
	if err != nil {
		return Administrator{}, fmt.Errorf("reset administrator password: %w", err)
	}
	if _, err := s.db.Exec(ctx, "delete from pgbase_meta.admin_sessions where admin_id = $1", administratorID); err != nil {
		return Administrator{}, fmt.Errorf("revoke administrator sessions after password reset: %w", err)
	}
	return administratorUTC(administrator), nil
}

func (s *Store) CreatePasswordReset(ctx context.Context, tokenHash []byte, administratorID uuid.UUID, expiresAt time.Time) error {
	if _, err := s.db.Exec(ctx, `insert into pgbase_meta.admin_password_resets (token_hash,admin_id,expires_at) values ($1,$2,$3)`, tokenHash, administratorID, expiresAt); err != nil {
		return fmt.Errorf("create administrator password reset: %w", err)
	}
	return nil
}

// ConsumePasswordReset locks and marks one valid token consumed. The update is
// deliberately performed before the password mutation so simultaneous callers
// cannot both use the same credential.
func (s *Store) ConsumePasswordReset(ctx context.Context, tokenHash []byte, now time.Time) (uuid.UUID, error) {
	var administratorID uuid.UUID
	err := s.db.QueryRow(ctx, `update pgbase_meta.admin_password_resets
		set consumed_at=$2
		where token_hash=$1 and consumed_at is null and expires_at > $2
		returning admin_id`, tokenHash, now).Scan(&administratorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrInvalidCredentials
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("consume administrator password reset: %w", err)
	}
	return administratorID, nil
}

func (s *Store) ResetAdministratorPasswordByID(ctx context.Context, administratorID uuid.UUID, passwordHash string, now time.Time) (Administrator, error) {
	var administrator Administrator
	err := s.db.QueryRow(ctx, `update pgbase_meta.admins
		set password_hash=$2, updated_at=$3
		where id=$1 and active
		returning id,email,active,created_at,updated_at`, administratorID, passwordHash, now).Scan(
		&administrator.ID, &administrator.Email, &administrator.Active, &administrator.CreatedAt, &administrator.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Administrator{}, ErrInvalidCredentials
	}
	if err != nil {
		return Administrator{}, fmt.Errorf("reset administrator password: %w", err)
	}
	if err := s.DeleteAdministratorSessions(ctx, administratorID); err != nil {
		return Administrator{}, err
	}
	return administratorUTC(administrator), nil
}

// UpsertAdministratorPassword reactivates a locked existing administrator,
// replaces its password, and removes every existing session. TOTP and IP
// policy rows remain associated with the administrator's stable ID.
func (s *Store) UpsertAdministratorPassword(ctx context.Context, administratorID uuid.UUID, passwordHash string, now time.Time) (Administrator, error) {
	var administrator Administrator
	err := s.db.QueryRow(ctx, `
		update pgbase_meta.admins
		set password_hash = $2, active = true, updated_at = $3
		where id = $1
		returning id, email, active, created_at, updated_at`, administratorID, passwordHash, now).Scan(
		&administrator.ID, &administrator.Email, &administrator.Active, &administrator.CreatedAt, &administrator.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Administrator{}, ErrInvalidCredentials
	}
	if err != nil {
		return Administrator{}, fmt.Errorf("upsert administrator password: %w", err)
	}
	if err := s.DeleteAdministratorSessions(ctx, administratorID); err != nil {
		return Administrator{}, err
	}
	return administratorUTC(administrator), nil
}

// AdministratorByEmail reads an active administrator and its password hash
// without retaining a database resource across password verification.
func (s *Store) AdministratorByEmail(ctx context.Context, email string) (Administrator, string, error) {
	return s.administratorByEmail(ctx, email, false)
}

// AdministratorByEmailForUpdate fences session issuance against password and
// active-state changes after a password was verified outside the transaction.
func (s *Store) AdministratorByEmailForUpdate(ctx context.Context, email string) (Administrator, string, error) {
	return s.administratorByEmail(ctx, email, true)
}

// AdministratorByEmailAnyForUpdate locks an administrator regardless of its
// active state. It is reserved for lifecycle operations that may reactivate an
// existing administrator and must not mistake that row for an absent email.
func (s *Store) AdministratorByEmailAnyForUpdate(ctx context.Context, email string) (Administrator, string, error) {
	var administrator Administrator
	var passwordHash string
	err := s.db.QueryRow(ctx, `
		select id, email, active, created_at, updated_at, password_hash
		from pgbase_meta.admins
		where email = $1
		for update`, email).Scan(
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

func (s *Store) administratorByEmail(ctx context.Context, email string, forUpdate bool) (Administrator, string, error) {
	var administrator Administrator
	var passwordHash string
	query := `
		select id, email, active, created_at, updated_at, password_hash
		from pgbase_meta.admins
		where email = $1 and active`
	if forUpdate {
		query += " for update"
	}
	err := s.db.QueryRow(ctx, query, email).Scan(
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

// RateLimitFailures returns the current-window signal without interpreting it
// as a pre-verification account block.
func (s *Store) RateLimitFailures(ctx context.Context, subjectHash []byte, now time.Time) (int, error) {
	var failures int
	err := s.db.QueryRow(ctx, `
		select case
			when window_started_at > $2::timestamptz - interval '15 minutes' then failures
			else 0
		end
		from pgbase_meta.admin_rate_limits
		where subject_hash = $1`, subjectHash, now).Scan(&failures)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read administrator rate-limit failures: %w", err)
	}
	return failures, nil
}

// RecordRateLimitSignal records a post-verification failure. Email-scoped
// history drives bounded delay and alerting only, never a hard rejection.
func (s *Store) RecordRateLimitSignal(ctx context.Context, subjectHash []byte, now time.Time) error {
	_, err := s.db.Exec(ctx, `
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
			blocked_until = null,
			updated_at = $2`, subjectHash, now)
	if err != nil {
		return fmt.Errorf("record administrator rate-limit signal: %w", err)
	}
	return nil
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
		with refreshed as (
			update pgbase_meta.admin_sessions as sessions
			set last_seen_at = $2
			from pgbase_meta.admins as admins
			where sessions.token_hash = $1
			  and sessions.admin_id = admins.id
			  and sessions.revoked_at is null
			  and sessions.expires_at > $2
			  and admins.active
			  and sessions.last_seen_at <= $2 - interval '5 minutes'
			returning admins.id, admins.email, admins.active, admins.created_at, admins.updated_at, sessions.expires_at
		)
		select id, email, active, created_at, updated_at, expires_at from refreshed
		union all
		select admins.id, admins.email, admins.active, admins.created_at, admins.updated_at, sessions.expires_at
		from pgbase_meta.admin_sessions as sessions
		join pgbase_meta.admins as admins on admins.id = sessions.admin_id
		where sessions.token_hash = $1
		  and sessions.revoked_at is null
		  and sessions.expires_at > $2
		  and admins.active
		  and not exists (select 1 from refreshed)`, tokenHash, now).Scan(
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

// LockRateLimits serializes decisions for multiple rate-limit subjects in the
// caller-supplied stable order. Contention fails closed without waiting; callers
// must return the error from their transaction so any earlier locks roll back.
func (s *Store) LockRateLimits(ctx context.Context, keys []int64) error {
	for _, key := range keys {
		var acquired bool
		if err := s.db.QueryRow(ctx, "select pg_try_advisory_xact_lock($1)", key).Scan(&acquired); err != nil {
			return fmt.Errorf("lock administrator rate limit: %w", err)
		}
		if !acquired {
			return ErrRateLimited
		}
	}
	return nil
}

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

// RecordRateLimitFailure atomically adds a failure and starts a block when the
// caller-supplied threshold is reached.
func (s *Store) RecordRateLimitFailure(ctx context.Context, subjectHash []byte, now time.Time, limit int) (bool, error) {
	if limit <= 0 {
		return false, fmt.Errorf("record administrator rate-limit failure: invalid limit")
	}
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
				when (case when limits.window_started_at <= $2 - interval '15 minutes' then 1 else limits.failures + 1 end) >= $3
				then greatest(coalesce(limits.blocked_until, '-infinity'::timestamptz), $2 + interval '15 minutes')
				else null
			end,
			updated_at = $2
		returning failures >= $3`, subjectHash, now, limit).Scan(&blocked)
	if err != nil {
		return false, fmt.Errorf("record administrator rate-limit failure: %w", err)
	}
	return blocked, nil
}

func (s *Store) ClearRateLimit(ctx context.Context, subjectHash []byte) error {
	if _, err := s.db.Exec(ctx, "delete from pgbase_meta.admin_rate_limits where subject_hash = $1", subjectHash); err != nil {
		return fmt.Errorf("clear administrator rate limit: %w", err)
	}
	return nil
}

// ReleaseRateLimitAttempt removes only the current successful reservation from
// a shared network bucket without erasing other clients' failures.
func (s *Store) ReleaseRateLimitAttempt(ctx context.Context, subjectHash []byte) error {
	if _, err := s.db.Exec(ctx, `
		with deleted as (
			delete from pgbase_meta.admin_rate_limits
			where subject_hash = $1 and failures <= 1
			returning subject_hash
		)
		update pgbase_meta.admin_rate_limits
		set failures = failures - 1, blocked_until = null
		where subject_hash = $1 and failures > 1`, subjectHash); err != nil {
		return fmt.Errorf("release administrator rate limit attempt: %w", err)
	}
	return nil
}

func administratorUTC(administrator Administrator) Administrator {
	administrator.CreatedAt = administrator.CreatedAt.UTC()
	administrator.UpdatedAt = administrator.UpdatedAt.UTC()
	return administrator
}
