package userauth

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) ListUsers(ctx context.Context, after uuid.UUID, limit int) (UserPage, error) {
	if err := s.ready(); err != nil {
		return UserPage{}, err
	}
	if limit < 1 || limit > 100 {
		return UserPage{}, ErrValidation
	}
	rows, err := s.pool.Query(ctx, `
		select id,email,active,created_at,updated_at
		from pgbase_meta.users
		where id > $1
		order by id
		limit $2`, after, limit+1)
	if err != nil {
		return UserPage{}, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	items := make([]ManagedUser, 0, limit+1)
	for rows.Next() {
		var user ManagedUser
		if err := rows.Scan(&user.ID, &user.Email, &user.Active, &user.CreatedAt, &user.UpdatedAt); err != nil {
			return UserPage{}, fmt.Errorf("scan user: %w", err)
		}
		user.CreatedAt = user.CreatedAt.UTC()
		user.UpdatedAt = user.UpdatedAt.UTC()
		items = append(items, user)
	}
	if err := rows.Err(); err != nil {
		return UserPage{}, fmt.Errorf("list users: %w", err)
	}
	page := UserPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextAfter = page.Items[len(page.Items)-1].ID
	}
	return page, nil
}

func (s *Service) SetUserActive(ctx context.Context, id uuid.UUID, active bool) (ManagedUser, error) {
	if err := s.ready(); err != nil {
		return ManagedUser{}, err
	}
	if id == uuid.Nil {
		return ManagedUser{}, ErrValidation
	}
	var user ManagedUser
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		// Login takes the same user-row lock before issuing a session, and user
		// record authorization holds a share lock on this row. Deactivation
		// therefore cannot interleave with either decision inside its transaction.
		if err := lockManagedUser(ctx, tx, id, &user); err != nil {
			return err
		}
		if user.Active != active {
			if err := tx.QueryRow(ctx, `update pgbase_meta.users set active=$2,updated_at=CURRENT_TIMESTAMP where id=$1 returning updated_at`, id, active).Scan(&user.UpdatedAt); err != nil {
				return fmt.Errorf("set user active state: %w", err)
			}
			user.Active = active
		}
		if !active {
			if _, err := revokeSessionsInTx(ctx, tx, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return ManagedUser{}, err
	}
	user.UpdatedAt = user.UpdatedAt.UTC()
	return user, nil
}

func (s *Service) RevokeUserSessions(ctx context.Context, id uuid.UUID) (int64, error) {
	if err := s.ready(); err != nil {
		return 0, err
	}
	if id == uuid.Nil {
		return 0, ErrValidation
	}
	var revoked int64
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		// Serialize with login session issuance and password/deactivation changes
		// without holding a database connection during credential KDF work.
		var exists bool
		if err := tx.QueryRow(ctx, `select true from pgbase_meta.users where id=$1 for update`, id).Scan(&exists); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return fmt.Errorf("lock user for session revocation: %w", err)
		}
		var err error
		revoked, err = revokeSessionsInTx(ctx, tx, id)
		return err
	})
	if err != nil {
		return 0, err
	}
	return revoked, nil
}

func (s *Service) ChangePassword(ctx context.Context, id uuid.UUID, currentPassword, newPassword string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if id == uuid.Nil || validatePassword(newPassword) != nil {
		return ErrValidation
	}
	release, err := admit(s.credentialSlots)
	if err != nil {
		return err
	}
	defer release()
	now, err := s.databaseNow(ctx)
	if err != nil {
		return err
	}
	buckets := []rateLimitBucket{{hash: s.subjectHash("password-change", "account", id.String()), limit: loginAccountLimit}}
	if err := s.ensureBucketsNotBlocked(ctx, buckets, now); err != nil {
		return err
	}
	user, currentHash, lookupErr := userPasswordByIDDB(ctx, s.pool, id, false)
	if lookupErr != nil && !errors.Is(lookupErr, ErrInvalidCredentials) {
		return lookupErr
	}
	releasePassword, err := admit(s.passwordSlots)
	if err != nil {
		return err
	}
	shapeValid := validatePassword(currentPassword) == nil && lookupErr == nil && user.Active
	verifyHash, verifyPassword := currentHash, currentPassword
	if !shapeValid {
		verifyHash, verifyPassword = dummyPasswordHash, dummyPassword
	}
	verifyErr := s.passwords.Verify(verifyHash, verifyPassword)
	if verifyErr != nil && !isPasswordMismatch(verifyErr) {
		releasePassword()
		return verifyErr
	}
	if !shapeValid || verifyErr != nil {
		releasePassword()
		if err := s.recordFailures(ctx, buckets, now); err != nil {
			return err
		}
		return ErrInvalidCredentials
	}
	newHash, err := s.passwords.Hash(newPassword)
	releasePassword()
	if err != nil {
		return err
	}

	// Both KDF operations intentionally finish before taking the row lock. The
	// transaction then rechecks the exact hash and active state under FOR UPDATE,
	// fencing concurrent login, deactivation, revocation, and password changes.
	var operationErr error
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		locked, lockedHash, err := userPasswordByIDDB(ctx, tx, id, true)
		if err != nil {
			if errors.Is(err, ErrInvalidCredentials) {
				operationErr = ErrInvalidCredentials
				return nil
			}
			return err
		}
		if !locked.Active || !hmac.Equal([]byte(lockedHash), []byte(currentHash)) {
			operationErr = ErrInvalidCredentials
			return nil
		}
		if _, err := tx.Exec(ctx, `update pgbase_meta.users set password_hash=$2,updated_at=CURRENT_TIMESTAMP where id=$1`, id, newHash); err != nil {
			return fmt.Errorf("change user password: %w", err)
		}
		_, err = revokeSessionsInTx(ctx, tx, id)
		return err
	})
	if err != nil {
		return err
	}
	if operationErr != nil {
		if err := s.recordFailures(ctx, buckets, now); err != nil {
			return err
		}
		return operationErr
	}
	return nil
}

func userPasswordByIDDB(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id uuid.UUID, lock bool) (ManagedUser, string, error) {
	query := `select id,email,active,created_at,updated_at,password_hash from pgbase_meta.users where id=$1`
	if lock {
		query += ` for update`
	}
	var user ManagedUser
	var hash string
	err := db.QueryRow(ctx, query, id).Scan(&user.ID, &user.Email, &user.Active, &user.CreatedAt, &user.UpdatedAt, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedUser{}, "", ErrInvalidCredentials
	}
	if err != nil {
		return ManagedUser{}, "", fmt.Errorf("read user credential: %w", err)
	}
	user.CreatedAt = user.CreatedAt.UTC()
	user.UpdatedAt = user.UpdatedAt.UTC()
	return user, hash, nil
}

func lockManagedUser(ctx context.Context, tx pgx.Tx, id uuid.UUID, user *ManagedUser) error {
	err := tx.QueryRow(ctx, `select id,email,active,created_at,updated_at from pgbase_meta.users where id=$1 for update`, id).Scan(&user.ID, &user.Email, &user.Active, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock managed user: %w", err)
	}
	user.CreatedAt = user.CreatedAt.UTC()
	user.UpdatedAt = user.UpdatedAt.UTC()
	return nil
}

func revokeSessionsInTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (int64, error) {
	tag, err := tx.Exec(ctx, `update pgbase_meta.user_sessions set revoked_at=CURRENT_TIMESTAMP where user_id=$1 and revoked_at is null`, id)
	if err != nil {
		return 0, fmt.Errorf("revoke user sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}
