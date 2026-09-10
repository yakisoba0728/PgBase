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
	"github.com/yakisoba0728/PgBase/internal/identity"
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

func (s *Service) ListAuthorizedAuthRecords(ctx context.Context, collectionID, principal uuid.UUID, limit int) ([]AuthRecord, error) {
	fields, rules, err := s.authRecordRules(ctx, collectionID)
	if err != nil {
		return nil, err
	}
	values, err := s.ListAuthRecords(ctx, collectionID, limit)
	if err != nil {
		return nil, err
	}
	result := values[:0]
	for _, value := range values {
		if EvaluateAuthRecordRule(rules.List, fields, AuthRuleContext{Principal: identity.Subject{Kind: identity.User, ID: principal}, Current: value}) {
			result = append(result, visibleAuthRecord(value, fields))
		}
	}
	return result, nil
}
func (s *Service) GetAuthorizedAuthRecord(ctx context.Context, collectionID, principal, target uuid.UUID) (AuthRecord, error) {
	fields, rules, err := s.authRecordRules(ctx, collectionID)
	if err != nil {
		return AuthRecord{}, err
	}
	value, err := s.GetAuthRecord(ctx, collectionID, target)
	if err != nil {
		return AuthRecord{}, err
	}
	if !EvaluateAuthRecordRule(rules.View, fields, AuthRuleContext{Principal: identity.Subject{Kind: identity.User, ID: principal}, Current: value}) {
		return AuthRecord{}, ErrInvalidCredentials
	}
	return visibleAuthRecord(value, fields), nil
}
func (s *Service) UpdateAuthorizedAuthRecord(ctx context.Context, collectionID, principal, target uuid.UUID, in UpdateAuthRecordInput) (AuthRecord, error) {
	fields, rules, err := s.authRecordRules(ctx, collectionID)
	if err != nil {
		return AuthRecord{}, err
	}
	current, err := s.GetAuthRecord(ctx, collectionID, target)
	if err != nil {
		return AuthRecord{}, err
	}
	if !EvaluateAuthRecordRule(rules.Update, fields, AuthRuleContext{Principal: identity.Subject{Kind: identity.User, ID: principal}, Current: current, Request: in.Fields}) {
		return AuthRecord{}, ErrInvalidCredentials
	}
	in.Email = nil
	in.Password = nil
	in.Verified = nil
	in.Active = nil
	updated, err := s.UpdateAuthRecord(ctx, collectionID, target, in)
	return visibleAuthRecord(updated, fields), err
}

func visibleAuthRecord(record AuthRecord, fields []schema.FieldInput) AuthRecord {
	visible := make(map[string]any, len(record.Fields))
	hidden := map[string]bool{}
	for _, field := range fields {
		hidden[field.Name] = field.Hidden
	}
	for name, value := range record.Fields {
		if !hidden[name] {
			visible[name] = value
		}
	}
	record.Fields = visible
	return record
}
func (s *Service) DeleteAuthorizedAuthRecord(ctx context.Context, collectionID, principal, target uuid.UUID) error {
	fields, rules, err := s.authRecordRules(ctx, collectionID)
	if err != nil {
		return err
	}
	current, err := s.GetAuthRecord(ctx, collectionID, target)
	if err != nil {
		return err
	}
	if !EvaluateAuthRecordRule(rules.Delete, fields, AuthRuleContext{Principal: identity.Subject{Kind: identity.User, ID: principal}, Current: current}) {
		return ErrInvalidCredentials
	}
	return s.DeleteAuthRecord(ctx, collectionID, target)
}

func (s *Service) authRecordRules(ctx context.Context, id uuid.UUID) ([]schema.FieldInput, AuthCollectionRules, error) {
	var raw []byte
	var rules AuthCollectionRules
	err := s.pool.QueryRow(ctx, `select fields_json,list_rule,view_rule,create_rule,update_rule,delete_rule,auth_rule,manage_rule from pgbase_meta.auth_collections where id=$1`, id).Scan(&raw, &rules.List, &rules.View, &rules.Create, &rules.Update, &rules.Delete, &rules.Auth, &rules.Manage)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rules, ErrNotFound
	}
	if err != nil {
		return nil, rules, err
	}
	var fields []schema.FieldInput
	if json.Unmarshal(raw, &fields) != nil {
		return nil, rules, errors.New("invalid stored auth fields")
	}
	return fields, rules, nil
}

func (s *Service) ListAuthRecords(ctx context.Context, collectionID uuid.UUID, limit int) ([]AuthRecord, error) {
	if collectionID == uuid.Nil || limit < 1 || limit > 100 {
		return nil, ErrValidation
	}
	fields, _, err := s.authRecordContract(ctx, collectionID)
	if err != nil {
		return nil, err
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
		v.Fields = publicAuthFields(v.Fields, fields)
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
	fields, _, err := s.authRecordContract(ctx, collectionID)
	if err != nil {
		return AuthRecord{}, err
	}
	v.Fields = publicAuthFields(v.Fields, fields)
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
	in.Fields = applyAuthAutoDates(in.Fields, fields, true, time.Now())
	if !validateTypedProfile(in.Fields, fields, false) {
		return AuthRecord{}, ErrValidation
	}
	storedFields, ok := prepareAuthPasswordFields(fields, in.Fields, true)
	if !ok || !validateTypedProfile(storedFields, fields, true) {
		return AuthRecord{}, ErrValidation
	}
	policy, err := s.authPolicy(ctx, collectionID)
	if err != nil || !passwordMatchesPolicy(in.Password, policy) {
		return AuthRecord{}, ErrValidation
	}
	hash, err := s.passwords.Hash(in.Password)
	if err != nil {
		return AuthRecord{}, err
	}
	raw, _ := json.Marshal(storedFields)
	id := uuid.New()
	var out AuthRecord
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := validateAuthRelationTargets(ctx, tx, id, storedFields, fields); err != nil {
			return err
		}
		if err := validateAuthIdentityUnique(ctx, tx, collectionID, id, storedFields, policy.IdentityFields); err != nil {
			return err
		}
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
			out = AuthRecord{id, collectionID, normalized, in.Verified, in.Active, publicAuthFields(storedFields, fields), now, now}
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
	policy, err := s.authPolicy(ctx, collectionID)
	if err != nil {
		return AuthRecord{}, err
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
		if validatePassword(*in.Password) != nil || !passwordMatchesPolicy(*in.Password, policy) {
			return AuthRecord{}, ErrValidation
		}
		v, e := s.passwords.Hash(*in.Password)
		if e != nil {
			return AuthRecord{}, e
		}
		hash = &v
	}
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var rawExisting []byte
		if err := tx.QueryRow(ctx, `select coalesce(p.attributes,'{}') from pgbase_meta.users u left join pgbase_meta.user_auth_profiles p on p.user_id=u.id where u.auth_collection_id=$1 and u.id=$2 for update of u`, collectionID, id).Scan(&rawExisting); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		merged := map[string]any{}
		if json.Unmarshal(rawExisting, &merged) != nil {
			return errors.New("invalid stored auth profile")
		}
		changes, ok := prepareAuthPasswordFields(fields, in.Fields, false)
		if !ok {
			return ErrValidation
		}
		for k, v := range changes {
			if v == nil {
				delete(merged, k)
			} else {
				merged[k] = v
			}
		}
		merged = applyAuthAutoDates(merged, fields, false, time.Now())
		if !validateTypedProfile(merged, fields, true) {
			return ErrValidation
		}
		if in.Fields != nil {
			if err := validateAuthRelationTargets(ctx, tx, id, merged, fields); err != nil {
				return err
			}
			if err := validateAuthIdentityUnique(ctx, tx, collectionID, id, merged, policy.IdentityFields); err != nil {
				return err
			}
		}
		tag, e := tx.Exec(ctx, `update pgbase_meta.users set email=coalesce($3,email),password_hash=coalesce($4,password_hash),verified=coalesce($5,verified),active=coalesce($6,active),updated_at=current_timestamp where auth_collection_id=$1 and id=$2`, collectionID, id, email, hash, in.Verified, in.Active)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return ErrNotFound
		}
		if in.Fields != nil {
			raw, _ := json.Marshal(merged)
			_, e = tx.Exec(ctx, `insert into pgbase_meta.user_auth_profiles(user_id,attributes,updated_at) values($1,$2,current_timestamp) on conflict(user_id) do update set attributes=excluded.attributes,updated_at=excluded.updated_at`, id, raw)
		}
		if e == nil && (hash != nil || email != nil || in.Active != nil && !*in.Active) {
			_, e = tx.Exec(ctx, `delete from pgbase_meta.user_sessions where user_id=$1`, id)
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

func validateAuthIdentityUnique(ctx context.Context, tx pgx.Tx, collectionID, userID uuid.UUID, attributes map[string]any, identityFields []string) error {
	for _, field := range identityFields {
		if field == "email" {
			continue
		}
		value, ok := attributes[field].(string)
		if !ok || value == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, collectionID.String()+":"+field+":"+value); err != nil {
			return err
		}
		var duplicate bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from pgbase_meta.user_auth_profiles p join pgbase_meta.users u on u.id=p.user_id where u.auth_collection_id=$1 and u.id<>$2 and p.attributes->>$3=$4)`, collectionID, userID, field, value).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate {
			return ErrConflict
		}
	}
	return nil
}

func (s *Service) DeleteAuthRecord(ctx context.Context, collectionID, id uuid.UUID) error {
	if collectionID == uuid.Nil || id == uuid.Nil {
		return ErrValidation
	}
	return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var name string
		err := tx.QueryRow(ctx, `select c.name from pgbase_meta.auth_collections c join pgbase_meta.users u on u.auth_collection_id=c.id where c.id=$1 and u.id=$2 for update of u`, collectionID, id).Scan(&name)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := enforceAuthRelationDeletes(ctx, tx, name, id); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `delete from pgbase_meta.users where id=$1`, id)
		return err
	})
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
