package filestore

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) CollectionID(ctx context.Context, name string) (uuid.UUID, error) {
	if !fieldPattern.MatchString(name) {
		return uuid.Nil, ErrInvalid
	}
	var id uuid.UUID
	if err := s.pool.QueryRow(ctx, `select id from (select id,name from pgbase_meta.collections union all select id,name from pgbase_meta.auth_collections) c where name=$1`, name).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	} else if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// IsFileField resolves modifier-looking record keys without teaching the HTTP
// batch decoder about catalog storage.
func (s *Service) IsFileField(ctx context.Context, collectionID uuid.UUID, field string) (bool, error) {
	if collectionID == uuid.Nil || !fieldPattern.MatchString(field) {
		return false, ErrInvalid
	}
	found := false
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, err := s.policy(ctx, tx, collectionID, field)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		found = err == nil
		return err
	})
	return found, err
}
