package userauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// AuthRecord is the collection-facing form of an identity. Credentials remain
// in users; collection-defined values remain in the profile JSON document.
type AuthRecord struct {
	ID               uuid.UUID      `json:"id"`
	AuthCollectionID uuid.UUID      `json:"authCollectionId"`
	Email            string         `json:"email"`
	Verified         bool           `json:"verified"`
	Active           bool           `json:"active"`
	Fields           map[string]any `json:"fields"`
	CreatedAt        time.Time      `json:"createdAt"`
	UpdatedAt        time.Time      `json:"updatedAt"`
}

type CreateAuthRecordInput struct {
	Email, Password  string
	Verified, Active bool
	Fields           map[string]any
}
type UpdateAuthRecordInput struct {
	Email            *string
	Password         *string
	Verified, Active *bool
	Fields           map[string]any
}

func (s *Service) ListAuthRecords(ctx context.Context, collectionID uuid.UUID, limit int) ([]AuthRecord, error) {
	if collectionID == uuid.Nil || limit < 1 || limit > 100 {
		return nil, ErrValidation
	}
	rows, err := s.pool.Query(ctx, `select u.id,u.auth_collection_id,u.email,u.verified,u.active,coalesce(p.attributes,'{}'),u.created_at,u.updated_at from pgbase_meta.users u left join pgbase_meta.user_auth_profiles p on p.user_id=u.id where u.auth_collection_id=$1 order by u.id limit $2`, collectionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AuthRecord{}
	for rows.Next() {
		var v AuthRecord
		var raw []byte
		if err := rows.Scan(&v.ID, &v.AuthCollectionID, &v.Email, &v.Verified, &v.Active, &raw, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &v.Fields) != nil {
			return nil, errors.New("invalid stored auth profile")
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

func (s *Service) GetAuthRecord(ctx context.Context, collectionID, id uuid.UUID) (AuthRecord, error) {
	if collectionID == uuid.Nil || id == uuid.Nil {
		return AuthRecord{}, ErrValidation
	}
	var v AuthRecord
	var raw []byte
	err := s.pool.QueryRow(ctx, `select u.id,u.auth_collection_id,u.email,u.verified,u.active,coalesce(p.attributes,'{}'),u.created_at,u.updated_at from pgbase_meta.users u left join pgbase_meta.user_auth_profiles p on p.user_id=u.id where u.auth_collection_id=$1 and u.id=$2`, collectionID, id).Scan(&v.ID, &v.AuthCollectionID, &v.Email, &v.Verified, &v.Active, &raw, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthRecord{}, ErrNotFound
	}
	if err != nil {
		return AuthRecord{}, err
	}
	if json.Unmarshal(raw, &v.Fields) != nil {
		return AuthRecord{}, errors.New("invalid stored auth profile")
	}
	return v, nil
}

func (s *Service) CreateAuthRecord(ctx context.Context, collectionID uuid.UUID, in CreateAuthRecordInput) (AuthRecord, error) {
	normalized, err := normalizeEmail(in.Email)
	if err != nil || validatePassword(in.Password) != nil {
		return AuthRecord{}, ErrValidation
	}
	fields, _, err := s.authRecordContract(ctx, collectionID)
	if err != nil {
		return AuthRecord{}, err
	}
	if !validateTypedProfile(in.Fields, fields) {
		return AuthRecord{}, ErrValidation
	}
	hash, err := s.passwords.Hash(in.Password)
	if err != nil {
		return AuthRecord{}, err
	}
	raw, _ := json.Marshal(in.Fields)
	id := uuid.New()
	var out AuthRecord
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var now time.Time
		if err := tx.QueryRow(ctx, `select current_timestamp`).Scan(&now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into pgbase_meta.users(id,auth_collection_id,email,password_hash,active,verified,created_at,updated_at) values($1,$2,$3,$4,$5,$6,$7,$7)`, id, collectionID, normalized, hash, in.Active, in.Verified, now)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `insert into pgbase_meta.user_auth_profiles(user_id,attributes,updated_at) values($1,$2,$3)`, id, raw, now)
		if err == nil {
			out = AuthRecord{id, collectionID, normalized, in.Verified, in.Active, in.Fields, now, now}
		}
		return err
	})
	if database.Classify(err) == database.KindConflict {
		return AuthRecord{}, ErrConflict
	}
	return out, err
}

func (s *Service) UpdateAuthRecord(ctx context.Context, collectionID, id uuid.UUID, in UpdateAuthRecordInput) (AuthRecord, error) {
	if collectionID == uuid.Nil || id == uuid.Nil {
		return AuthRecord{}, ErrValidation
	}
	fields, _, err := s.authRecordContract(ctx, collectionID)
	if err != nil {
		return AuthRecord{}, err
	}
	if in.Fields != nil && !validateTypedProfile(in.Fields, fields) {
		return AuthRecord{}, ErrValidation
	}
	var email *string
	if in.Email != nil {
		v, e := normalizeEmail(*in.Email)
		if e != nil {
			return AuthRecord{}, ErrValidation
		}
		email = &v
	}
	var hash *string
	if in.Password != nil {
		if validatePassword(*in.Password) != nil {
			return AuthRecord{}, ErrValidation
		}
		v, e := s.passwords.Hash(*in.Password)
		if e != nil {
			return AuthRecord{}, e
		}
		hash = &v
	}
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `update pgbase_meta.users set email=coalesce($3,email),password_hash=coalesce($4,password_hash),verified=coalesce($5,verified),active=coalesce($6,active),updated_at=current_timestamp where auth_collection_id=$1 and id=$2`, collectionID, id, email, hash, in.Verified, in.Active)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return ErrNotFound
		}
		if in.Fields != nil {
			raw, _ := json.Marshal(in.Fields)
			_, e = tx.Exec(ctx, `insert into pgbase_meta.user_auth_profiles(user_id,attributes,updated_at) values($1,$2,current_timestamp) on conflict(user_id) do update set attributes=excluded.attributes,updated_at=excluded.updated_at`, id, raw)
		}
		return e
	})
	if database.Classify(err) == database.KindConflict {
		return AuthRecord{}, ErrConflict
	}
	if err != nil {
		return AuthRecord{}, err
	}
	return s.GetAuthRecord(ctx, collectionID, id)
}

func (s *Service) DeleteAuthRecord(ctx context.Context, collectionID, id uuid.UUID) error {
	if collectionID == uuid.Nil || id == uuid.Nil {
		return ErrValidation
	}
	tag, err := s.pool.Exec(ctx, `delete from pgbase_meta.users where auth_collection_id=$1 and id=$2`, collectionID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) authRecordContract(ctx context.Context, id uuid.UUID) ([]schema.FieldInput, AuthCollectionRules, error) {
	var raw []byte
	var r AuthCollectionRules
	err := s.pool.QueryRow(ctx, `select fields_json,create_rule from pgbase_meta.auth_collections where id=$1`, id).Scan(&raw, &r.Create)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, r, ErrNotFound
	}
	if err != nil {
		return nil, r, err
	}
	var fields []schema.FieldInput
	if json.Unmarshal(raw, &fields) != nil {
		return nil, r, fmt.Errorf("invalid stored auth fields")
	}
	return fields, r, nil
}
