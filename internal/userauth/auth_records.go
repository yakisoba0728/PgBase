package userauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	if collectionID == uuid.Nil || limit < 1 || limit > 100 {
		return nil, ErrValidation
	}
	var page []AuthRecord
	err := s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		var err error
		page, err = s.listAuthorizedAuthRecordPage(ctx, tx, collectionID, principal, limit)
		return err
	})
	if err != nil {
		return nil, err
	}
	return page, nil
}

func (s *Service) listAuthorizedAuthRecordPage(ctx context.Context, db database.DBTX, collectionID, principal uuid.UUID, limit int) ([]AuthRecord, error) {
	fields, rules, err := authRecordRulesDB(ctx, db, collectionID)
	if err != nil {
		return nil, err
	}
	if rules.IsNull("list") {
		return []AuthRecord{}, nil
	}
	subject := authRecordPrincipal(principal)
	ruleContext := AuthRuleContext{Principal: subject}
	list, listArgs, err := compileAuthRecordRuleInTx(ctx, db, collectionID, rules.List, ruleContext, 3)
	if err != nil {
		return nil, err
	}
	args := append([]any{collectionID, principal, limit}, listArgs...)
	manage := "false"
	if principal != uuid.Nil && !rules.IsNull("manage") && strings.TrimSpace(rules.Manage) != "" {
		predicate, manageArgs, err := compileAuthRecordRuleInTx(ctx, db, collectionID, rules.Manage, ruleContext, len(args))
		switch {
		case err == nil:
			manage = predicate
			args = append(args, manageArgs...)
		case !errors.Is(err, ErrValidation):
			return nil, err
		}
	}
	// A non-null, true manage predicate exposes only the matched record's
	// email. Public visibility and ownership also work without manage.
	email := `case when u.email_visibility or u.id=$2 or (` + manage + `) then u.email else '' end`
	page, err := readAuthRecordPage(ctx, db, email, `where u.auth_collection_id=$1 and (`+list+`) order by u.id limit $3`, args...)
	if err != nil {
		return nil, err
	}
	for i := range page {
		page[i] = visibleAuthRecord(page[i], fields)
	}
	if err := s.projectAuthRecordPage(ctx, db, collectionID, page, fields, &subject); err != nil {
		return nil, err
	}
	return page, nil
}

func readAuthRecordPage(ctx context.Context, db database.DBTX, email, suffix string, args ...any) ([]AuthRecord, error) {
	rows, err := db.Query(ctx, `select u.id,u.auth_collection_id,`+email+`,u.email_visibility,u.verified,u.active,
		coalesce(p.attributes,'{}'),u.created_at,u.updated_at from pgbase_meta.users u
		left join pgbase_meta.user_auth_profiles p on p.user_id=u.id `+suffix, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := []AuthRecord{}
	for rows.Next() {
		var record AuthRecord
		var raw []byte
		if err := rows.Scan(&record.ID, &record.AuthCollectionID, &record.Email, &record.EmailVisibility, &record.Verified, &record.Active, &raw, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return nil, err
		}
		if decodeAuthProfile(raw, &record.Fields) != nil {
			return nil, errors.New("invalid stored auth profile")
		}
		page = append(page, record)
	}
	return page, rows.Err()
}

func (s *Service) projectAuthRecordPage(ctx context.Context, db database.DBTX, collectionID uuid.UUID, page []AuthRecord, fields []schema.FieldInput, principal *identity.Subject) error {
	documents := make([]map[string]any, len(page))
	ids := make(map[uuid.UUID]string, len(page))
	for i := range page {
		page[i].Fields = publicAuthFields(page[i].Fields, fields)
		documents[i] = page[i].Fields
		ids[page[i].ID] = ""
	}
	if err := recordidentity.LoadPublicIDs(ctx, db, "auth", collectionID, ids); err != nil {
		return err
	}
	for i := range page {
		page[i].PublicID = ids[page[i].ID]
	}
	if principal != nil {
		tx, ok := db.(pgx.Tx)
		if !ok {
			return ErrValidation
		}
		return s.projectAuthorizedAuthRelationDocuments(ctx, tx, documents, fields, *principal)
	}
	return s.projectAuthRelationDocuments(ctx, db, documents, fields)
}

func (s *Service) GetAuthorizedAuthRecord(ctx context.Context, collectionID, principal, target uuid.UUID) (AuthRecord, error) {
	return s.authRecordResponseForPrincipal(ctx, collectionID, authRecordPrincipal(principal), target, true)
}

func (s *Service) authRecordResponseForPrincipal(ctx context.Context, collectionID uuid.UUID, principal identity.Subject, target uuid.UUID, requireView bool) (AuthRecord, error) {
	if collectionID == uuid.Nil || target == uuid.Nil || !principal.Valid() {
		return AuthRecord{}, ErrValidation
	}
	var result AuthRecord
	err := s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		fields, rules, err := authRecordRulesDB(ctx, tx, collectionID)
		if err != nil {
			return err
		}
		page, err := readAuthRecordPage(ctx, tx, "u.email", `where u.auth_collection_id=$1 and u.id=$2`, collectionID, target)
		if err != nil {
			return err
		}
		if len(page) != 1 {
			return ErrNotFound
		}
		value := page[0]
		if requireView {
			if rules.IsNull("view") {
				return ErrInvalidCredentials
			}
			allowed, err := EvaluateAuthRecordRuleInTx(ctx, tx, collectionID, rules.View, AuthRuleContext{Principal: principal, Current: value})
			if err != nil {
				return err
			}
			if !allowed {
				return ErrInvalidCredentials
			}
		}
		if !value.EmailVisibility && principal.ID != value.ID {
			managed := false
			if !rules.IsNull("manage") {
				managed, err = s.authorizeManagedAuthRecordTx(ctx, tx, collectionID, principal.ID, value, fields, rules.Manage, nil)
				if err != nil {
					return err
				}
			}
			if !managed {
				value.Email = ""
			}
		}
		// Root view/manage rules use internal values. Response redaction is the
		// last step and cannot alter the data against which those rules run.
		page[0] = visibleAuthRecord(value, fields)
		if err := s.projectAuthRecordPage(ctx, tx, collectionID, page, fields, &principal); err != nil {
			return err
		}
		result = page[0]
		return nil
	})
	return result, err
}

func (s *Service) UpdateAuthorizedAuthRecord(ctx context.Context, collectionID, principal, target uuid.UUID, in UpdateAuthRecordInput) (AuthRecord, error) {
	var fields []schema.FieldInput
	var rules AuthCollectionRules
	request := authRecordMutationValues(in)
	subject := authRecordPrincipal(principal)
	updated, err := s.updateAuthRecord(ctx, collectionID, target, in, &subject, func(tx pgx.Tx, current AuthRecord) error {
		var err error
		fields, rules, err = authRecordRulesDB(ctx, tx, collectionID)
		if err != nil {
			return err
		}
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
	return updated, nil
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
	return s.deleteAuthRecord(ctx, collectionID, target, func(tx pgx.Tx, collectionID, target uuid.UUID) error {
		return AuthorizeAuthRecordDeleteInTx(ctx, tx, identity.Subject{Kind: identity.User, ID: principal}, collectionID, target)
	})
}

// AuthorizeAuthRecordDeleteInTx checks the current auth collection rule for a
// cascading user deletion in the caller's transaction.
func AuthorizeAuthRecordDeleteInTx(ctx context.Context, tx pgx.Tx, principal identity.Subject, collectionID, target uuid.UUID) error {
	if principal.Kind != identity.User || principal.ID == uuid.Nil {
		return ErrInvalidCredentials
	}
	var locked uuid.UUID
	if err := tx.QueryRow(ctx, `select id from pgbase_meta.users where auth_collection_id=$1 and id=$2 for update`, collectionID, target).Scan(&locked); err != nil {
		return err
	}
	var rule *string
	if err := tx.QueryRow(ctx, `select delete_rule from pgbase_meta.auth_collections where id=$1 for share`, collectionID).Scan(&rule); err != nil {
		return err
	}
	if rule == nil {
		return ErrInvalidCredentials
	}
	allowed, err := EvaluateAuthRecordRuleInTx(ctx, tx, collectionID, *rule, AuthRuleContext{Principal: principal, Current: AuthRecord{ID: target}})
	if err != nil {
		return err
	}
	if !allowed {
		return ErrInvalidCredentials
	}
	return nil
}

func (s *Service) authRecordRules(ctx context.Context, id uuid.UUID) ([]schema.FieldInput, AuthCollectionRules, error) {
	return authRecordRulesDB(ctx, s.pool, id)
}

func authRecordRulesDB(ctx context.Context, db database.DBTX, id uuid.UUID) ([]schema.FieldInput, AuthCollectionRules, error) {
	var raw []byte
	var rules AuthCollectionRules
	var values [7]*string
	err := db.QueryRow(ctx, `select fields_json,list_rule,view_rule,create_rule,update_rule,delete_rule,auth_rule,manage_rule from pgbase_meta.auth_collections where id=$1`, id).Scan(&raw, &values[0], &values[1], &values[2], &values[3], &values[4], &values[5], &values[6])
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

// ListAuthRecords pages a realm's records by internal user id (the
// users_auth_collection_id_id_idx keyset); next is uuid.Nil on the last page.
func (s *Service) ListAuthRecords(ctx context.Context, collectionID, after uuid.UUID, limit int) ([]AuthRecord, uuid.UUID, error) {
	if collectionID == uuid.Nil || limit < 1 || limit > 100 {
		return nil, uuid.Nil, ErrValidation
	}
	var result []AuthRecord
	next := uuid.Nil
	err := s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		fields, err := authRecordContractDB(ctx, tx, collectionID)
		if err != nil {
			return err
		}
		result, err = readAuthRecordPage(ctx, tx, "u.email", `where u.auth_collection_id=$1 and u.id>$2 order by u.id limit $3`, collectionID, after, limit+1)
		if err != nil {
			return err
		}
		if len(result) > limit {
			result = result[:limit]
			next = result[limit-1].ID
		}
		return s.projectAuthRecordPage(ctx, tx, collectionID, result, fields, nil)
	})
	if err != nil {
		return nil, uuid.Nil, err
	}
	return result, next, nil
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
	if decodeAuthProfile(raw, &v.Fields) != nil {
		return AuthRecord{}, errors.New("invalid stored auth profile")
	}
	fields, err := s.authRecordContract(ctx, collectionID)
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
	if err != nil {
		return AuthRecord{}, fieldError(ErrValidation, "email", "invalid")
	}
	if validatePassword(in.Password) != nil {
		return AuthRecord{}, fieldError(ErrValidation, "password", "invalid")
	}
	hash, err := s.passwords.Hash(in.Password)
	if err != nil {
		return AuthRecord{}, err
	}
	id := uuid.New()
	var out AuthRecord
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		policy, err := lockedAuthPolicyTx(ctx, tx, collectionID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !passwordMatchesPolicy(in.Password, policy) {
			return fieldError(ErrValidation, "password", "policy")
		}
		var fieldsRaw []byte
		if err := tx.QueryRow(ctx, `select fields_json from pgbase_meta.auth_collections where id=$1 for share`, collectionID).Scan(&fieldsRaw); err != nil {
			return err
		}
		var fields []schema.FieldInput
		if err := json.Unmarshal(fieldsRaw, &fields); err != nil {
			return errors.New("invalid stored auth fields")
		}
		var now time.Time
		if err := tx.QueryRow(ctx, `select current_timestamp`).Scan(&now); err != nil {
			return err
		}
		attributes := applyAuthAutoDates(in.Fields, fields, true, now)
		if err := s.resolveAuthRelationIdentities(ctx, tx, attributes, fields); err != nil {
			return err
		}
		if !validateTypedProfile(attributes, fields, false) {
			return fieldError(ErrValidation, "fields", "invalid")
		}
		storedFields, ok := prepareAuthPasswordFields(fields, attributes, true)
		if !ok || !validateTypedProfile(storedFields, fields, true) {
			return fieldError(ErrValidation, "fields", "invalid")
		}
		raw, err := json.Marshal(storedFields)
		if err != nil {
			return err
		}
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
	return s.updateAuthRecord(ctx, collectionID, id, in, nil, nil)
}

func (s *Service) updateAuthRecord(ctx context.Context, collectionID, id uuid.UUID, in UpdateAuthRecordInput, responsePrincipal *identity.Subject, authorize func(pgx.Tx, AuthRecord) error) (AuthRecord, error) {
	if collectionID == uuid.Nil || id == uuid.Nil {
		return AuthRecord{}, ErrValidation
	}
	fields, err := s.authRecordContract(ctx, collectionID)
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

	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		policy, err := lockedAuthPolicyTx(ctx, tx, collectionID)
		if err != nil {
			return err
		}

		var rawExisting []byte
		var current AuthRecord
		if err := tx.QueryRow(ctx, `select u.id,u.auth_collection_id,u.email,u.email_visibility,u.verified,u.active,coalesce(p.attributes,'{}'),u.created_at,u.updated_at from pgbase_meta.users u left join pgbase_meta.user_auth_profiles p on p.user_id=u.id where u.auth_collection_id=$1 and u.id=$2 for update of u`, collectionID, id).Scan(&current.ID, &current.AuthCollectionID, &current.Email, &current.EmailVisibility, &current.Verified, &current.Active, &rawExisting, &current.CreatedAt, &current.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		var realm uuid.UUID
		if err := tx.QueryRow(ctx, `select id from pgbase_meta.auth_collections where id=$1 for share`, collectionID).Scan(&realm); err != nil {
			return err
		}
		fields, _, err = authRecordRulesDB(ctx, tx, collectionID)
		if err != nil {
			return err
		}

		merged := map[string]any{}
		if decodeAuthProfile(rawExisting, &merged) != nil {
			return errors.New("invalid stored auth profile")
		}
		current.Fields = merged
		if authorize != nil {
			if err := authorize(tx, current); err != nil {
				return err
			}
		}
		var hash *string
		if in.Password != nil {
			if validatePassword(*in.Password) != nil || !passwordMatchesPolicy(*in.Password, policy) {
				return ErrValidation
			}
			release, e := admit(s.passwordSlots)
			if e != nil {
				return e
			}
			v, e := s.passwords.Hash(*in.Password)
			release()
			if e != nil {
				return e
			}
			hash = &v
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
		raw, marshalErr := json.Marshal(merged)
		if marshalErr != nil {
			return marshalErr
		}
		_, e = tx.Exec(ctx, `insert into pgbase_meta.user_auth_profiles(user_id,attributes,updated_at) values($1,$2,current_timestamp) on conflict(user_id) do update set attributes=excluded.attributes,updated_at=excluded.updated_at`, id, raw)
		if e == nil && (hash != nil || email != nil || in.Active != nil && !*in.Active || in.Verified != nil && !*in.Verified) {
			_, e = revokeSessionsInTx(ctx, tx, id)
		}
		return e
	})
	if database.Classify(err) == database.KindConflict {
		return AuthRecord{}, ErrConflict
	}
	if err != nil {
		return AuthRecord{}, err
	}
	if responsePrincipal != nil {
		return s.authRecordResponseForPrincipal(ctx, collectionID, *responsePrincipal, id, false)
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
	return s.deleteAuthRecord(ctx, collectionID, id, nil)
}

func (s *Service) deleteAuthRecord(ctx context.Context, collectionID, id uuid.UUID, authorize func(pgx.Tx, uuid.UUID, uuid.UUID) error) error {
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
		if authorize != nil {
			if err := authorize(tx, collectionID, id); err != nil {
				return err
			}
		}
		if err := enforceAuthRelationDeletes(ctx, tx, name, id, authorize); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `delete from pgbase_meta.users where id=$1`, id)
		return err
	})
}

func (s *Service) authRecordContract(ctx context.Context, id uuid.UUID) ([]schema.FieldInput, error) {
	return authRecordContractDB(ctx, s.pool, id)
}

func authRecordContractDB(ctx context.Context, db database.DBTX, id uuid.UUID) ([]schema.FieldInput, error) {
	var raw []byte
	err := db.QueryRow(ctx, `select fields_json from pgbase_meta.auth_collections where id=$1`, id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var fields []schema.FieldInput
	if json.Unmarshal(raw, &fields) != nil {
		return nil, fmt.Errorf("invalid stored auth fields")
	}
	return fields, nil
}
