// Package recordaccess owns the opt-in public user access policy for records.
package recordaccess

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
)

var (
	ErrNotFound    = errors.New("collection access policy not found")
	ErrUnsupported = errors.New("collection access policy unsupported")
	ErrDenied      = errors.New("collection access denied")
)

type Policy struct {
	Collection string
	Enabled    bool
}

type Service struct{ pool *database.Pool }

func NewService(pool *database.Pool) *Service { return &Service{pool: pool} }

func (s *Service) Get(ctx context.Context, collection string) (Policy, error) {
	if s == nil || s.pool == nil {
		return Policy{}, ErrNotFound
	}
	var policy Policy
	err := s.pool.QueryRow(ctx, `SELECT c.name, COALESCE(a.enabled,false) FROM pgbase_meta.collections c LEFT JOIN pgbase_meta.collection_access a ON a.collection_id=c.id WHERE c.name=$1`, collection).Scan(&policy.Collection, &policy.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return Policy{}, ErrNotFound
	}
	return policy, err
}

func (s *Service) Set(ctx context.Context, collection string, enabled bool) (Policy, error) {
	if s == nil || s.pool == nil {
		return Policy{}, ErrNotFound
	}
	var policy Policy
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM pgbase_meta.schema_state WHERE singleton FOR SHARE`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT id FROM pgbase_meta.collections WHERE name=$1 FOR UPDATE`, collection); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO pgbase_meta.collection_access(collection_id, enabled)
SELECT id, $2 FROM pgbase_meta.collections WHERE name=$1
ON CONFLICT (collection_id) DO UPDATE SET enabled=EXCLUDED.enabled
RETURNING (SELECT name FROM pgbase_meta.collections WHERE id=collection_id), enabled`, collection, enabled).Scan(&policy.Collection, &policy.Enabled)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Policy{}, ErrNotFound
	}
	return policy, err
}

// AuthorizeUser locks the policy row for the surrounding operation and checks
// active status in the same transaction snapshot. Callers must separately
// reject catalogs that have relations, which this package deliberately does not
// understand.
func AuthorizeUser(ctx context.Context, tx pgx.Tx, collectionID, userID uuid.UUID) error {
	var enabled, active bool
	err := tx.QueryRow(ctx, `SELECT a.enabled,u.active FROM pgbase_meta.collection_access a JOIN pgbase_meta.users u ON u.id=$2 WHERE a.collection_id=$1 FOR SHARE OF a,u`, collectionID, userID).Scan(&enabled, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDenied
	}
	if err != nil {
		return err
	}
	if !enabled || !active {
		return ErrDenied
	}
	return err
}
