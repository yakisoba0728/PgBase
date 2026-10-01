package adminauth

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
)

var ErrAdministratorNotFound = errors.New("administrator account not found")

// ListAdministratorAccounts returns a bounded ID-ordered page, including inactive
// accounts. The actor's active state is checked in the same read transaction.
func (s *Service) ListAdministratorAccounts(ctx context.Context, actor, after uuid.UUID, limit int) ([]Administrator, uuid.UUID, error) {
	if limit < 1 || limit > 100 {
		return nil, uuid.Nil, ErrValidation
	}
	if err := s.ready(); err != nil {
		return nil, uuid.Nil, err
	}
	items := make([]Administrator, 0, limit+1)
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := activeAccountActor(ctx, tx, actor); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `select id,email,active,created_at,updated_at from pgbase_meta.admins where id>$1 order by id limit $2`, after, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var value Administrator
			if err := rows.Scan(&value.ID, &value.Email, &value.Active, &value.CreatedAt, &value.UpdatedAt); err != nil {
				return err
			}
			items = append(items, administratorUTC(value))
		}
		return rows.Err()
	})
	next := uuid.Nil
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1].ID
	}
	return items, next, err
}

// CreateAdministratorAccount is the authenticated management counterpart to
// the local recovery CLI. It never issues a session for the new account.
func (s *Service) CreateAdministratorAccount(ctx context.Context, actor uuid.UUID, email, password string) (Administrator, error) {
	if err := s.ready(); err != nil {
		return Administrator{}, err
	}
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return Administrator{}, err
	}
	hash, err := s.hashPassword(ctx, password)
	if err != nil {
		return Administrator{}, err
	}
	var result Administrator
	err = s.accountTransaction(ctx, actor, func(tx pgx.Tx) error {
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		now := s.clock.Now().UTC()
		result, err = NewStore(tx).CreateAdministrator(ctx, Administrator{ID: id, Email: normalized, Active: true, CreatedAt: now, UpdatedAt: now}, hash)
		return err
	})
	if database.Classify(err) == database.KindConflict {
		err = ErrConflict
	}
	return result, err
}

// SetAdministratorActive serializes with CLI lifecycle operations. A request
// cannot deactivate its own actor or remove the last active administrator.
func (s *Service) SetAdministratorActive(ctx context.Context, actor, target uuid.UUID, active bool) (Administrator, error) {
	var result Administrator
	err := s.accountTransaction(ctx, actor, func(tx pgx.Tx) error {
		current, err := lockAdministratorAccount(ctx, tx, target)
		if err != nil {
			return err
		}
		if !active && actor == target {
			return ErrConflict
		}
		if current.Active && !active {
			count, err := NewStore(tx).ActiveAdministratorCount(ctx)
			if err != nil {
				return err
			}
			if count <= 1 {
				return ErrConflict
			}
		}
		if _, err := tx.Exec(ctx, `update pgbase_meta.admins set active=$2,updated_at=$3 where id=$1`, target, active, s.clock.Now().UTC()); err != nil {
			return err
		}
		if !active {
			if err := invalidateAccountCredentials(ctx, tx, target); err != nil {
				return err
			}
		}
		result, err = lockAdministratorAccount(ctx, tx, target)
		return err
	})
	return result, err
}

// SetAdministratorPassword preserves active state, TOTP and network policy.
// Existing sessions and pending email credentials are invalidated atomically.
func (s *Service) SetAdministratorPassword(ctx context.Context, actor, target uuid.UUID, password string) error {
	if err := s.ready(); err != nil {
		return err
	}
	hash, err := s.hashPassword(ctx, password)
	if err != nil {
		return err
	}
	return s.accountTransaction(ctx, actor, func(tx pgx.Tx) error {
		if _, err := lockAdministratorAccount(ctx, tx, target); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update pgbase_meta.admins set password_hash=$2,updated_at=$3 where id=$1`, target, hash, s.clock.Now().UTC()); err != nil {
			return err
		}
		return invalidateAccountCredentials(ctx, tx, target)
	})
}

func (s *Service) RevokeAdministratorSessions(ctx context.Context, actor, target uuid.UUID) error {
	return s.accountTransaction(ctx, actor, func(tx pgx.Tx) error {
		if _, err := lockAdministratorAccount(ctx, tx, target); err != nil {
			return err
		}
		return NewStore(tx).DeleteAdministratorSessions(ctx, target)
	})
}

func (s *Service) accountTransaction(ctx context.Context, actor uuid.UUID, apply func(pgx.Tx) error) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, administratorLifecycleLock); err != nil {
			return err
		}
		if err := activeAccountActor(ctx, tx, actor); err != nil {
			return err
		}
		return apply(tx)
	})
}

func activeAccountActor(ctx context.Context, tx pgx.Tx, actor uuid.UUID) error {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `select id from pgbase_meta.admins where id=$1 and active for share`, actor).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) || actor == uuid.Nil {
		return ErrInvalidCredentials
	}
	return err
}

func lockAdministratorAccount(ctx context.Context, tx pgx.Tx, target uuid.UUID) (Administrator, error) {
	var value Administrator
	err := tx.QueryRow(ctx, `select id,email,active,created_at,updated_at from pgbase_meta.admins where id=$1 for update`, target).Scan(&value.ID, &value.Email, &value.Active, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Administrator{}, ErrAdministratorNotFound
	}
	return administratorUTC(value), err
}

func invalidateAccountCredentials(ctx context.Context, tx pgx.Tx, target uuid.UUID) error {
	if err := NewStore(tx).DeleteAdministratorSessions(ctx, target); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `delete from pgbase_meta.admin_password_resets where admin_id=$1`, target); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `delete from pgbase_meta.admin_email_otps where admin_id=$1`, target)
	return err
}
