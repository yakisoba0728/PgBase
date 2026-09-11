package recordidentity

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/fieldvalue"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// ReserveAuth registers every auth principal, including UUID-policy records,
// in the shared auth scope. This makes mixed UUID/text collisions visible to
// one database uniqueness constraint across every auth collection.
func (s *Service) ReserveAuth(ctx context.Context, db DB, collectionID, internal uuid.UUID, policy schema.RecordIDPolicy, requested *string) (string, error) {
	if collectionID == uuid.Nil || internal == uuid.Nil {
		return "", ErrInvalid
	}
	if policy.Kind != schema.RecordIDText {
		public := internal.String()
		if requested != nil && *requested != public {
			return "", ErrInvalid
		}
		return public, reserveAuth(ctx, db, collectionID, internal, public)
	}
	if requested != nil {
		if schema.ValidateRecordIDValue(policy, *requested) != "" {
			return "", ErrInvalid
		}
		if err := reserveAuth(ctx, db, collectionID, internal, *requested); err != nil {
			return "", err
		}
		return *requested, nil
	}
	if policy.AutogeneratePattern == "" {
		return "", ErrInvalid
	}
	for attempt := 0; attempt < generatedAttempts; attempt++ {
		candidate, err := fieldvalue.RandomStringByPattern(s.random, policy.AutogeneratePattern)
		if err != nil || schema.ValidateRecordIDValue(policy, candidate) != "" {
			return "", ErrInvalid
		}
		if err := reserveAuth(ctx, db, collectionID, internal, candidate); err == nil {
			return candidate, nil
		} else if !errors.Is(err, ErrConflict) {
			return "", err
		}
	}
	return "", ErrConflict
}

func reserveAuth(ctx context.Context, db DB, collectionID, internal uuid.UUID, external string) error {
	_, err := db.Exec(ctx, `insert into pgbase_meta.record_identities(collection_id,collection_kind,scope_id,internal_id,external_id) values($1,'auth','00000000-0000-0000-0000-000000000000',$2,$3)`, collectionID, internal, external)
	if uniqueViolation(err) {
		return ErrConflict
	}
	return err
}

func (s *Service) ResolveAuth(ctx context.Context, db DB, collectionID uuid.UUID, policy schema.RecordIDPolicy, public string) (uuid.UUID, error) {
	if collectionID == uuid.Nil || public == "" {
		return uuid.Nil, ErrInvalid
	}
	if policy.Kind == schema.RecordIDText && schema.ValidateRecordIDValue(policy, public) != "" {
		return uuid.Nil, ErrInvalid
	}
	if policy.Kind != schema.RecordIDText {
		if id, err := uuid.Parse(public); err != nil || id == uuid.Nil {
			return uuid.Nil, ErrInvalid
		}
	}
	var internal uuid.UUID
	err := db.QueryRow(ctx, `select internal_id from pgbase_meta.record_identities where collection_kind='auth' and collection_id=$1 and external_id=$2`, collectionID, public).Scan(&internal)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return internal, err
}

func (s *Service) PublicAuth(ctx context.Context, db DB, collectionID, internal uuid.UUID) (string, error) {
	if collectionID == uuid.Nil || internal == uuid.Nil {
		return "", ErrInvalid
	}
	var external string
	err := db.QueryRow(ctx, `select external_id from pgbase_meta.record_identities where collection_kind='auth' and collection_id=$1 and internal_id=$2`, collectionID, internal).Scan(&external)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return external, err
}
