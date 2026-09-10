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
	"github.com/yakisoba0728/PgBase/internal/recordidentity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// AuthRecord is the collection-facing form of an identity. Credentials remain
// in users; collection-defined values remain in the profile JSON document.
type AuthRecord struct {
	ID               uuid.UUID      `json:"-"`
	PublicID         string         `json:"id"`
	AuthCollectionID uuid.UUID      `json:"authCollectionId"`
	Email            string         `json:"email,omitempty"`
	EmailVisibility  bool           `json:"emailVisibility"`
	Verified         bool           `json:"verified"`
	Active           bool           `json:"active"`
	Fields           map[string]any `json:"fields"`
	CreatedAt        time.Time      `json:"createdAt"`
	UpdatedAt        time.Time      `json:"updatedAt"`
}

type CreateAuthRecordInput struct {
	ID               string
	Email, Password  string
	EmailVisibility  bool
	Verified, Active bool
	Fields           map[string]any
}

func (r AuthRecord) MarshalJSON() ([]byte, error) {
	publicID := r.PublicID
	if publicID == "" && r.ID != uuid.Nil {
		publicID = r.ID.String()
	}
	type wire struct {
		ID               string         `json:"id"`
		AuthCollectionID uuid.UUID      `json:"authCollectionId"`
		Email            string         `json:"email,omitempty"`
		EmailVisibility  bool           `json:"emailVisibility"`
		Verified         bool           `json:"verified"`
		Active           bool           `json:"active"`
		Fields           map[string]any `json:"fields"`
		CreatedAt        time.Time      `json:"createdAt"`
		UpdatedAt        time.Time      `json:"updatedAt"`
	}
	return json.Marshal(wire{publicID, r.AuthCollectionID, r.Email, r.EmailVisibility, r.Verified, r.Active, r.Fields, r.CreatedAt, r.UpdatedAt})
}

type UpdateAuthRecordInput struct {
	Email            *string
	EmailVisibility  *bool
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
		if !rules.IsNull("list") && s.evaluateAuthRecordRuleDB(ctx, collectionID, rules.List, AuthRuleContext{Principal: identity.Subject{Kind: identity.User, ID: principal}, Current: value}) {
			result = append(result, s.visibleAuthRecordForPrincipal(ctx, value, fields, principal, rules))
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
	if rules.IsNull("view") || !s.evaluateAuthRecordRuleDB(ctx, collectionID, rules.View, AuthRuleContext{Principal: identity.Subject{Kind: identity.User, ID: principal}, Current: value}) {
		return AuthRecord{}, ErrInvalidCredentials
	}
	return s.visibleAuthRecordForPrincipal(ctx, value, fields, principal, rules), nil
}
func (s *Service) UpdateAuthorizedAuthRecord(ctx context.Context, collectionID, principal, target uuid.UUID, in UpdateAuthRecordInput) (AuthRecord, error) {
	fields, rules, err := s.authRecordRules(ctx, collectionID)
	if err != nil {
		return AuthRecord{}, err
	}
	request := authRecordMutationValues(in)
	updated, err := s.updateAuthRecord(ctx, collectionID, target, in, func(tx pgx.Tx, current AuthRecord) error {
		context := AuthRuleContext{Principal: identity.Subject{Kind: identity.User, ID: principal}, Current: current, Request: request}
		allowed, err := EvaluateAuthRecordRuleInTx(ctx, tx, collectionID, rules.Update, context)
		if rules.IsNull("update") {
			allowed = false
		}
		if err != nil {
			return fmt.Errorf("evaluate update rule: %w", err)
		}
		if !allowed {
			return ErrInvalidCredentials
		}
		if hasPrivilegedAuthMutation(in) {
			allowed, err = s.authorizeManagedAuthRecordTx(ctx, tx, collectionID, principal, current, fields, rules.Manage, request)
			if err != nil {
				return fmt.Errorf("evaluate manage rule: %w", err)
			}
			if !allowed {
				return ErrInvalidCredentials
			}
		}
		return nil
	})
	if err != nil {
		return AuthRecord{}, err
	}
	return s.visibleAuthRecordForPrincipal(ctx, updated, fields, principal, rules), nil
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

// visibleAuthRecordForPrincipal removes an email only after the record has
// passed its collection rule. Owners retain their own email. A principal that
// satisfies the persisted manage rule for this target also retains it, matching
// PocketBase's record projection after list or view authorization succeeds.
func (s *Service) visibleAuthRecordForPrincipal(ctx context.Context, record AuthRecord, fields []schema.FieldInput, principal uuid.UUID, rules AuthCollectionRules) AuthRecord {
	record = visibleAuthRecord(record, fields)
	if record.EmailVisibility || principal == record.ID {
		return record
	}
	managed := !rules.IsNull("manage") && s.authorizeManagedAuthRecord(ctx, record.AuthCollectionID, principal, record, fields, rules.Manage, nil)
	if !managed {
		record.Email = ""
	}
	return record
}
func (s *Service) DeleteAuthorizedAuthRecord(ctx context.Context, collectionID, principal, target uuid.UUID) error {
	_, rules, err := s.authRecordRules(ctx, collectionID)
	if err != nil {
		return err
	}
	current, err := s.GetAuthRecord(ctx, collectionID, target)
	if err != nil {
		return err
	}
	if rules.IsNull("delete") || !s.evaluateAuthRecordRuleDB(ctx, collectionID, rules.Delete, AuthRuleContext{Principal: identity.Subject{Kind: identity.User, ID: principal}, Current: current}) {
		return ErrInvalidCredentials
	}
	return s.DeleteAuthRecord(ctx, collectionID, target)
}

func (s *Service) authRecordRules(ctx context.Context, id uuid.UUID) ([]schema.FieldInput, AuthCollectionRules, error) {
	var raw []byte
	var rules AuthCollectionRules
	var values [7]*string
	err := s.pool.QueryRow(ctx, `select fields_json,list_rule,view_rule,create_rule,update_rule,delete_rule,auth_rule,manage_rule from pgbase_meta.auth_collections where id=$1`, id).Scan(&raw, &values[0], &values[1], &values[2], &values[3], &values[4], &values[5], &values[6])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rules, ErrNotFound
	}
	if err != nil {
		return nil, rules, err
	}
	applyNullableAuthRules(&rules, values)
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
	rows, err := s.pool.Query(ctx, `select u.id,u.auth_collection_id,u.email,u.email_visibility,u.verified,u.active,coalesce(p.attributes,'{}'),u.created_at,u.updated_at from pgbase_meta.users u left join pgbase_meta.user_auth_profiles p on p.user_id=u.id where u.auth_collection_id=$1 order by u.id limit $2`, collectionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AuthRecord{}
	for rows.Next() {
		var v AuthRecord
		var raw []byte
		if err := rows.Scan(&v.ID, &v.AuthCollectionID, &v.Email, &v.EmailVisibility, &v.Verified, &v.Active, &raw, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &v.Fields) != nil {
			return nil, errors.New("invalid stored auth profile")
		}
		v.Fields = publicAuthFields(v.Fields, fields)
		if err := s.projectAuthRelationIdentities(ctx, s.pool, v.Fields, fields); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.projectAuthRecords(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) GetAuthRecord(ctx context.Context, collectionID, id uuid.UUID) (AuthRecord, error) {
	if collectionID == uuid.Nil || id == uuid.Nil {
		return AuthRecord{}, ErrValidation
	}
	var v AuthRecord
	var raw []byte
	err := s.pool.QueryRow(ctx, `select u.id,u.auth_collection_id,u.email,u.email_visibility,u.verified,u.active,coalesce(p.attributes,'{}'),u.created_at,u.updated_at from pgbase_meta.users u left join pgbase_meta.user_auth_profiles p on p.user_id=u.id where u.auth_collection_id=$1 and u.id=$2`, collectionID, id).Scan(&v.ID, &v.AuthCollectionID, &v.Email, &v.EmailVisibility, &v.Verified, &v.Active, &raw, &v.CreatedAt, &v.UpdatedAt)
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
	if err := s.projectAuthRelationIdentities(ctx, s.pool, v.Fields, fields); err != nil {
		return AuthRecord{}, err
	}
	if err := s.projectAuthRecord(ctx, s.pool, &v); err != nil {
		return AuthRecord{}, err
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
	in.Fields = applyAuthAutoDates(in.Fields, fields, true, time.Now())
	if err := s.resolveAuthRelationIdentities(ctx, s.pool, in.Fields, fields); err != nil {
		return AuthRecord{}, err
	}
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
		recordIDPolicy, err := authRecordIDPolicy(ctx, tx, collectionID)
		if err != nil {
			return err
		}
		var requestedID *string
		if in.ID != "" {
			requestedID = &in.ID
		}
		publicID, err := s.recordIdentities.ReserveAuth(ctx, tx, collectionID, id, recordIDPolicy, requestedID)
		if errors.Is(err, recordidentity.ErrInvalid) {
			return ErrValidation
		}
		if errors.Is(err, recordidentity.ErrConflict) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
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
		_, err = tx.Exec(ctx, `insert into pgbase_meta.users(id,auth_collection_id,email,password_hash,email_visibility,active,verified,created_at,updated_at) values($1,$2,$3,$4,$5,$6,$7,$8,$8)`, id, collectionID, normalized, hash, in.EmailVisibility, in.Active, in.Verified, now)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `insert into pgbase_meta.user_auth_profiles(user_id,attributes,updated_at) values($1,$2,$3)`, id, raw, now)
		if err == nil {
			out = AuthRecord{ID: id, PublicID: publicID, AuthCollectionID: collectionID, Email: normalized, EmailVisibility: in.EmailVisibility, Verified: in.Verified, Active: in.Active, Fields: publicAuthFields(storedFields, fields), CreatedAt: now, UpdatedAt: now}
			if projectErr := s.projectAuthRelationIdentities(ctx, tx, out.Fields, fields); projectErr != nil {
				return projectErr
			}
		}
		return err
	})
	if database.Classify(err) == database.KindConflict {
		return AuthRecord{}, ErrConflict
	}
	return out, err
}

func (s *Service) UpdateAuthRecord(ctx context.Context, collectionID, id uuid.UUID, in UpdateAuthRecordInput) (AuthRecord, error) {
	return s.updateAuthRecord(ctx, collectionID, id, in, nil)
}

func (s *Service) updateAuthRecord(ctx context.Context, collectionID, id uuid.UUID, in UpdateAuthRecordInput, authorize func(pgx.Tx, AuthRecord) error) (AuthRecord, error) {
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
		var current AuthRecord
		if err := tx.QueryRow(ctx, `select u.id,u.auth_collection_id,u.email,u.email_visibility,u.verified,u.active,coalesce(p.attributes,'{}'),u.created_at,u.updated_at from pgbase_meta.users u left join pgbase_meta.user_auth_profiles p on p.user_id=u.id where u.auth_collection_id=$1 and u.id=$2 for update of u`, collectionID, id).Scan(&current.ID, &current.AuthCollectionID, &current.Email, &current.EmailVisibility, &current.Verified, &current.Active, &rawExisting, &current.CreatedAt, &current.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		merged := map[string]any{}
		if json.Unmarshal(rawExisting, &merged) != nil {
			return errors.New("invalid stored auth profile")
		}
		current.Fields = merged
		if authorize != nil {
			if err := authorize(tx, current); err != nil {
				return err
			}
		}
		if err := s.resolveAuthRelationIdentities(ctx, tx, in.Fields, fields); err != nil {
			return err
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
		tag, e := tx.Exec(ctx, `update pgbase_meta.users set email=coalesce($3,email),email_visibility=coalesce($4,email_visibility),password_hash=coalesce($5,password_hash),verified=coalesce($6,verified),active=coalesce($7,active),updated_at=current_timestamp where auth_collection_id=$1 and id=$2`, collectionID, id, email, in.EmailVisibility, hash, in.Verified, in.Active)
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
	var create *string
	err := s.pool.QueryRow(ctx, `select fields_json,create_rule from pgbase_meta.auth_collections where id=$1`, id).Scan(&raw, &create)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, r, ErrNotFound
	}
	if err != nil {
		return nil, r, err
	}
	if create == nil {
		r.nulls = map[string]bool{"create": true}
	} else {
		r.Create = *create
	}
	var fields []schema.FieldInput
	if json.Unmarshal(raw, &fields) != nil {
		return nil, r, fmt.Errorf("invalid stored auth fields")
	}
	return fields, r, nil
}
