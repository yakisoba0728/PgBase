// Package recordidentity maps exact collection-facing text record IDs to the
// UUIDs used by PgBase's physical tables and transactional sidecars.
package recordidentity

import (
	"context"
	"crypto/rand"
	"errors"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/yakisoba0728/PgBase/internal/fieldvalue"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

var (
	ErrInvalid  = errors.New("invalid record id")
	ErrConflict = errors.New("record id already exists")
	ErrNotFound = errors.New("record id not found")
)

const generatedAttempts = 16

type DB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type Service struct{ random io.Reader }

func New(randomSource io.Reader) *Service {
	if randomSource == nil {
		randomSource = rand.Reader
	}
	return &Service{random: randomSource}
}

// Reserve binds a caller-provided external ID or generates one when omitted.
// It must run in the same transaction as the record insert.
func (s *Service) Reserve(ctx context.Context, db DB, collection schema.Collection, internal uuid.UUID, requested *string) (string, error) {
	if internal == uuid.Nil {
		return "", ErrInvalid
	}
	if collection.RecordID.Kind != schema.RecordIDText {
		if requested != nil && *requested != internal.String() {
			return "", ErrInvalid
		}
		return internal.String(), nil
	}
	if requested != nil {
		if schema.ValidateRecordIDValue(collection.RecordID, *requested) != "" {
			return "", ErrInvalid
		}
		if err := reserve(ctx, db, collection.ID, internal, *requested); err != nil {
			if uniqueViolation(err) {
				return "", ErrConflict
			}
			return "", err
		}
		return *requested, nil
	}
	if collection.RecordID.AutogeneratePattern == "" {
		return "", ErrInvalid
	}
	for attempt := 0; attempt < generatedAttempts; attempt++ {
		candidate, err := fieldvalue.RandomStringByPattern(s.random, collection.RecordID.AutogeneratePattern)
		if err != nil || schema.ValidateRecordIDValue(collection.RecordID, candidate) != "" {
			return "", ErrInvalid
		}
		if err := reserve(ctx, db, collection.ID, internal, candidate); err == nil {
			return candidate, nil
		} else if !uniqueViolation(err) {
			return "", err
		}
	}
	return "", ErrConflict
}

func reserve(ctx context.Context, db DB, collectionID, internal uuid.UUID, external string) error {
	_, err := db.Exec(ctx, `insert into pgbase_meta.record_identities(collection_id,collection_kind,scope_id,internal_id,external_id) values($1,'base',$1,$2,$3)`, collectionID, internal, external)
	return err
}

func identityCollectionID(collection schema.Collection) uuid.UUID {
	return collection.AccessCollectionID()
}

func (s *Service) Resolve(ctx context.Context, db DB, collection schema.Collection, public string) (uuid.UUID, error) {
	if collection.RecordID.Kind != schema.RecordIDText {
		id, err := uuid.Parse(public)
		if err != nil || id == uuid.Nil {
			return uuid.Nil, ErrInvalid
		}
		return id, nil
	}
	if schema.ValidateRecordIDValue(collection.RecordID, public) != "" {
		return uuid.Nil, ErrInvalid
	}
	var id uuid.UUID
	err := db.QueryRow(ctx, `select internal_id from pgbase_meta.record_identities where collection_kind='base' and collection_id=$1 and external_id=$2`, identityCollectionID(collection), public).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return id, err
}

func (s *Service) ResolveMany(ctx context.Context, db DB, collection schema.Collection, public []string) ([]uuid.UUID, error) {
	ids, err := s.LookupMany(ctx, db, collection, public)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if id == uuid.Nil {
			return nil, ErrNotFound
		}
	}
	return ids, nil
}

// LookupMany preserves input order and leaves a zero UUID for an absent text ID.
// ResolveMany is the strict variant used by mutation inputs.
func (s *Service) LookupMany(ctx context.Context, db DB, collection schema.Collection, public []string) ([]uuid.UUID, error) {
	if len(public) == 0 {
		return []uuid.UUID{}, nil
	}
	if collection.RecordID.Kind != schema.RecordIDText {
		result := make([]uuid.UUID, len(public))
		for i, value := range public {
			id, err := s.Resolve(ctx, db, collection, value)
			if err != nil {
				return nil, err
			}
			result[i] = id
		}
		return result, nil
	}
	for _, value := range public {
		if schema.ValidateRecordIDValue(collection.RecordID, value) != "" {
			return nil, ErrInvalid
		}
	}
	rows, err := db.Query(ctx, `select identity.internal_id
		from unnest($2::text[]) with ordinality as input(external_id,ordinality)
		left join pgbase_meta.record_identities identity
		  on identity.collection_kind='base' and identity.collection_id=$1 and identity.external_id=input.external_id
		order by input.ordinality`, identityCollectionID(collection), public)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]uuid.UUID, 0, len(public))
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, uuid.UUID(id.Bytes))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) != len(public) {
		return nil, ErrNotFound
	}
	return result, nil
}

func (s *Service) Public(ctx context.Context, db DB, collection schema.Collection, internal uuid.UUID) (string, error) {
	if collection.RecordID.Kind != schema.RecordIDText {
		if internal == uuid.Nil {
			return "", ErrInvalid
		}
		return internal.String(), nil
	}
	var external string
	err := db.QueryRow(ctx, `select external_id from pgbase_meta.record_identities where collection_kind='base' and collection_id=$1 and internal_id=$2`, identityCollectionID(collection), internal).Scan(&external)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return external, err
}

func (s *Service) Forget(ctx context.Context, db DB, collection schema.Collection, internal uuid.UUID) error {
	if collection.RecordID.Kind != schema.RecordIDText {
		return nil
	}
	tag, err := db.Exec(ctx, `delete from pgbase_meta.record_identities where collection_kind='base' and collection_id=$1 and internal_id=$2`, identityCollectionID(collection), internal)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

// ForgetMany removes identities for records deleted by one relation cascade.
func (s *Service) ForgetMany(ctx context.Context, db DB, collection schema.Collection, internal []uuid.UUID) error {
	if collection.RecordID.Kind != schema.RecordIDText || len(internal) == 0 {
		return nil
	}
	tag, err := db.Exec(ctx, `DELETE FROM pgbase_meta.record_identities WHERE collection_kind='base' AND collection_id=$1 AND internal_id=ANY($2::uuid[])`, identityCollectionID(collection), internal)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != int64(len(internal)) {
		return ErrNotFound
	}
	return nil
}

func uniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}
