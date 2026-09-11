package userauth

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/recordidentity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func authRecordIDPolicy(ctx context.Context, db recordidentity.DB, collectionID uuid.UUID) (schema.RecordIDPolicy, error) {
	var raw []byte
	if err := db.QueryRow(ctx, `select record_id from pgbase_meta.auth_collections where id=$1`, collectionID).Scan(&raw); errors.Is(err, pgx.ErrNoRows) {
		return schema.RecordIDPolicy{}, ErrNotFound
	} else if err != nil {
		return schema.RecordIDPolicy{}, err
	}
	var policy schema.RecordIDPolicy
	if json.Unmarshal(raw, &policy) != nil {
		return schema.RecordIDPolicy{}, errors.New("invalid stored auth record ID policy")
	}
	return policy, nil
}

// ResolveAuthRecordID is the public-path adapter. Text policies only consult
// the durable mapping and never reinterpret the supplied value as a UUID.
func (s *Service) ResolveAuthRecordID(ctx context.Context, collectionID uuid.UUID, public string) (uuid.UUID, error) {
	policy, err := authRecordIDPolicy(ctx, s.pool, collectionID)
	if err != nil {
		return uuid.Nil, err
	}
	id, err := s.recordIdentities.ResolveAuth(ctx, s.pool, collectionID, policy, public)
	if errors.Is(err, recordidentity.ErrInvalid) {
		return uuid.Nil, ErrValidation
	}
	if errors.Is(err, recordidentity.ErrNotFound) {
		return uuid.Nil, ErrNotFound
	}
	return id, err
}

func (s *Service) projectAuthRecord(ctx context.Context, db recordidentity.DB, record *AuthRecord) error {
	if record == nil || record.ID == uuid.Nil {
		return nil
	}
	public, err := s.recordIdentities.PublicAuth(ctx, db, record.AuthCollectionID, record.ID)
	if err != nil {
		return err
	}
	record.PublicID = public
	return nil
}

func (s *Service) projectAuthRecords(ctx context.Context, records []AuthRecord) error {
	for i := range records {
		if err := s.projectAuthRecord(ctx, s.pool, &records[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) projectUser(ctx context.Context, db recordidentity.DB, user *User) error {
	if user == nil || user.ID == uuid.Nil {
		return nil
	}
	public, err := s.recordIdentities.PublicAuth(ctx, db, user.AuthCollectionID, user.ID)
	if err != nil {
		return err
	}
	user.PublicID = public
	return nil
}
