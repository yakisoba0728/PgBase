// Package recordidentity maps exact collection-facing text record IDs to the
// UUIDs used by PgBase's physical tables and transactional sidecars.
package recordidentity

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"

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
	err := db.QueryRow(ctx, `select internal_id from pgbase_meta.record_identities where collection_id=$1 and external_id=$2`, collection.ID, public).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return id, err
}

func (s *Service) ResolveMany(ctx context.Context, db DB, collection schema.Collection, public []string) ([]uuid.UUID, error) {
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
		  on identity.collection_id=$1 and identity.external_id=input.external_id
		order by input.ordinality`, collection.ID, public)
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
		if !id.Valid {
			return nil, ErrNotFound
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
	err := db.QueryRow(ctx, `select external_id from pgbase_meta.record_identities where collection_id=$1 and internal_id=$2`, collection.ID, internal).Scan(&external)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return external, err
}

func (s *Service) Forget(ctx context.Context, db DB, collection schema.Collection, internal uuid.UUID) error {
	if collection.RecordID.Kind != schema.RecordIDText {
		return nil
	}
	tag, err := db.Exec(ctx, `delete from pgbase_meta.record_identities where collection_id=$1 and internal_id=$2`, collection.ID, internal)
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
	tag, err := db.Exec(ctx, `DELETE FROM pgbase_meta.record_identities WHERE collection_id=$1 AND internal_id=ANY($2::uuid[])`, collection.ID, internal)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != int64(len(internal)) {
		return ErrNotFound
	}
	return nil
}

// Project replaces the root and relation IDs in hydrated records. Expanded
// relation records are recursively projected using the target collection.
func (s *Service) Project(ctx context.Context, db DB, catalog schema.Catalog, collection schema.Collection, records ...map[string]any) error {
	for _, record := range records {
		if err := s.projectRecord(ctx, db, catalog, collection, record); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) projectRecord(ctx context.Context, db DB, catalog schema.Catalog, collection schema.Collection, record map[string]any) error {
	if record == nil {
		return nil
	}
	if id, ok := stringUUID(record["id"]); ok {
		public, err := s.Public(ctx, db, collection, id)
		if err != nil {
			return err
		}
		record["id"] = public
	}
	for _, field := range collection.Fields {
		if field.Relation == nil {
			continue
		}
		target, ok := collectionByID(catalog, field.Relation.TargetCollectionID)
		if !ok {
			return fmt.Errorf("project relation %s: missing target collection", field.Name)
		}
		projected, err := s.projectRelationValue(ctx, db, target, record[field.Name])
		if err != nil {
			return err
		}
		record[field.Name] = projected
		if expands, ok := record["expand"].(map[string]any); ok {
			switch expanded := expands[field.Name].(type) {
			case map[string]any:
				if err := s.projectRecord(ctx, db, catalog, target, expanded); err != nil {
					return err
				}
			case []map[string]any:
				for _, child := range expanded {
					if err := s.projectRecord(ctx, db, catalog, target, child); err != nil {
						return err
					}
				}
			case []any:
				for _, raw := range expanded {
					if child, ok := raw.(map[string]any); ok {
						if err := s.projectRecord(ctx, db, catalog, target, child); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	return nil
}

func (s *Service) projectRelationValue(ctx context.Context, db DB, target schema.Collection, value any) (any, error) {
	switch ids := value.(type) {
	case string:
		id, ok := stringUUID(ids)
		if !ok || ids == "" {
			return value, nil
		}
		return s.Public(ctx, db, target, id)
	case []string:
		result := make([]string, len(ids))
		for i, raw := range ids {
			id, ok := stringUUID(raw)
			if !ok {
				return nil, ErrInvalid
			}
			public, err := s.Public(ctx, db, target, id)
			if err != nil {
				return nil, err
			}
			result[i] = public
		}
		return result, nil
	case []any:
		result := make([]any, len(ids))
		for i, raw := range ids {
			text, ok := raw.(string)
			if !ok {
				return nil, ErrInvalid
			}
			projected, err := s.projectRelationValue(ctx, db, target, text)
			if err != nil {
				return nil, err
			}
			result[i] = projected
		}
		return result, nil
	default:
		return value, nil
	}
}

func collectionByID(catalog schema.Catalog, id uuid.UUID) (schema.Collection, bool) {
	for _, collection := range catalog.Collections {
		if collection.ID == id {
			return collection, true
		}
	}
	return schema.Collection{}, false
}

func stringUUID(value any) (uuid.UUID, bool) {
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) != text {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(text)
	return id, err == nil && id != uuid.Nil
}

func uniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}
