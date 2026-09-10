package userauth

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

var DefaultCollectionID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
var authCollectionName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type AuthCollection struct {
	ID                  uuid.UUID
	Name                string
	RegistrationEnabled bool
	ProfileSchema       map[string]string
	Fields              []schema.FieldInput
	Rules               AuthCollectionRules
	RecordID            schema.RecordIDPolicy
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type AuthCollectionRules struct {
	List   string `json:"list"`
	View   string `json:"view"`
	Create string `json:"create"`
	Update string `json:"update"`
	Delete string `json:"delete"`
	Auth   string `json:"auth"`
	Manage string `json:"manage"`
	nulls  map[string]bool
}

var authRuleNames = []string{"list", "view", "create", "update", "delete", "auth", "manage"}

func (r AuthCollectionRules) MarshalJSON() ([]byte, error) {
	values := map[string]any{"list": r.List, "view": r.View, "create": r.Create, "update": r.Update, "delete": r.Delete, "auth": r.Auth, "manage": r.Manage}
	for name := range r.nulls {
		values[name] = nil
	}
	return json.Marshal(values)
}
func (r *AuthCollectionRules) UnmarshalJSON(raw []byte) error {
	var values map[string]*json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return ErrValidation
	}
	*r = AuthCollectionRules{nulls: map[string]bool{}}
	for name, value := range values {
		known := false
		for _, candidate := range authRuleNames {
			if name == candidate {
				known = true
				break
			}
		}
		if !known {
			return ErrValidation
		}
		if value == nil || string(*value) == "null" {
			r.nulls[name] = true
			continue
		}
		var source string
		if json.Unmarshal(*value, &source) != nil {
			return ErrValidation
		}
		switch name {
		case "list":
			r.List = source
		case "view":
			r.View = source
		case "create":
			r.Create = source
		case "update":
			r.Update = source
		case "delete":
			r.Delete = source
		case "auth":
			r.Auth = source
		case "manage":
			r.Manage = source
		}
	}
	return nil
}
func (r AuthCollectionRules) IsNull(name string) bool { return r.nulls != nil && r.nulls[name] }
func (r AuthCollectionRules) databaseValues() []any {
	values := []string{r.List, r.View, r.Create, r.Update, r.Delete, r.Auth, r.Manage}
	out := make([]any, len(values))
	for i, name := range authRuleNames {
		if r.IsNull(name) {
			out[i] = nil
		} else {
			out[i] = values[i]
		}
	}
	return out
}
func applyNullableAuthRules(r *AuthCollectionRules, values [7]*string) {
	r.nulls = map[string]bool{}
	for i, name := range authRuleNames {
		if values[i] == nil {
			r.nulls[name] = true
			continue
		}
		switch name {
		case "list":
			r.List = *values[i]
		case "view":
			r.View = *values[i]
		case "create":
			r.Create = *values[i]
		case "update":
			r.Update = *values[i]
		case "delete":
			r.Delete = *values[i]
		case "auth":
			r.Auth = *values[i]
		case "manage":
			r.Manage = *values[i]
		}
	}
	if len(r.nulls) == 0 {
		r.nulls = nil
	}
}

type CreateAuthCollectionInput struct {
	ID                  uuid.UUID
	Name                string
	RegistrationEnabled bool
	ProfileSchema       map[string]string
	Fields              []schema.FieldInput
	Rules               AuthCollectionRules
	RecordID            *schema.RecordIDPolicy
}
type UpdateAuthCollectionInput struct {
	RegistrationEnabled bool
	ProfileSchema       map[string]string
	Fields              []schema.FieldInput
	Rules               AuthCollectionRules
	RecordID            *schema.RecordIDPolicy
}

func (s *Service) ConfigureDefaultRegistration(enabled bool) { s.defaultRegistration = enabled }

func (s *Service) CreateAuthCollection(ctx context.Context, input CreateAuthCollectionInput) (AuthCollection, error) {
	fields, legacy, ok := normalizeAuthFields(input.Fields, input.ProfileSchema)
	recordID, recordIDErr := schema.NormalizeRecordIDPolicy(input.RecordID)
	if input.ID == uuid.Nil || !authCollectionName.MatchString(input.Name) || !ok || recordIDErr != nil || !ValidateAuthRecordRules(input.Rules, fields) {
		return AuthCollection{}, ErrValidation
	}
	profileRaw, _ := json.Marshal(legacy)
	fieldsRaw, _ := json.Marshal(fields)
	var value AuthCollection
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := validateAuthRelationDefinitions(ctx, tx, input.Name, fields); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('pgbase-auth-collection-limit'))`); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow(ctx, `select count(*) from pgbase_meta.auth_collections`).Scan(&count); err != nil {
			return err
		}
		if count >= 100 {
			return ErrConflict
		}
		rv := input.Rules.databaseValues()
		recordIDRaw, _ := json.Marshal(recordID)
		row := tx.QueryRow(ctx, `insert into pgbase_meta.auth_collections(id,name,registration_enabled,profile_schema,fields_json,list_rule,view_rule,create_rule,update_rule,delete_rule,auth_rule,manage_rule,record_id,created_at,updated_at) values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP) returning `+authCollectionColumns, input.ID, input.Name, input.RegistrationEnabled, profileRaw, fieldsRaw, rv[0], rv[1], rv[2], rv[3], rv[4], rv[5], rv[6], recordIDRaw)
		return scanAuthCollection(row, &value)
	})
	if database.Classify(err) == database.KindConflict {
		return AuthCollection{}, ErrConflict
	}
	if err != nil {
		return AuthCollection{}, err
	}
	return value, nil
}

func (s *Service) UpdateAuthCollection(ctx context.Context, id uuid.UUID, input UpdateAuthCollectionInput) (AuthCollection, error) {
	fields, legacy, ok := normalizeAuthFields(input.Fields, input.ProfileSchema)
	if id == uuid.Nil || !ok || !ValidateAuthRecordRules(input.Rules, fields) {
		return AuthCollection{}, ErrValidation
	}
	profileRaw, _ := json.Marshal(legacy)
	fieldsRaw, _ := json.Marshal(fields)
	var value AuthCollection
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := validateAuthRelationDefinitions(ctx, tx, "", fields); err != nil {
			return err
		}
		var currentRecordIDRaw []byte
		if err := tx.QueryRow(ctx, `select id,record_id from pgbase_meta.auth_collections where id=$1 for update`, id).Scan(&value.ID, &currentRecordIDRaw); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		var currentRecordID schema.RecordIDPolicy
		if json.Unmarshal(currentRecordIDRaw, &currentRecordID) != nil {
			return errors.New("invalid stored auth record ID policy")
		}
		if input.RecordID != nil {
			requested, err := schema.NormalizeRecordIDPolicy(input.RecordID)
			if err != nil || requested != currentRecordID {
				return ErrConflict
			}
		}
		rows, err := tx.Query(ctx, `select p.attributes from pgbase_meta.user_auth_profiles p join pgbase_meta.users u on u.id=p.user_id where u.auth_collection_id=$1 for update of p`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var profileRaw []byte
			var profile map[string]any
			if rows.Scan(&profileRaw) != nil || json.Unmarshal(profileRaw, &profile) != nil || !validateTypedProfile(profile, fields, true) {
				return ErrConflict
			}
		}
		if rows.Err() != nil {
			return rows.Err()
		}
		rv := input.Rules.databaseValues()
		row := tx.QueryRow(ctx, `update pgbase_meta.auth_collections set registration_enabled=$2,profile_schema=$3,fields_json=$4,list_rule=$5,view_rule=$6,create_rule=$7,update_rule=$8,delete_rule=$9,auth_rule=$10,manage_rule=$11,updated_at=CURRENT_TIMESTAMP where id=$1 returning `+authCollectionColumns, id, input.RegistrationEnabled, profileRaw, fieldsRaw, rv[0], rv[1], rv[2], rv[3], rv[4], rv[5], rv[6])
		return scanAuthCollection(row, &value)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthCollection{}, ErrNotFound
	}
	if err != nil {
		return AuthCollection{}, err
	}
	return value, nil
}

func (s *Service) DeleteAuthCollection(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return ErrValidation
	}
	if id == DefaultCollectionID {
		return ErrConflict
	}
	tag, err := s.pool.Exec(ctx, `delete from pgbase_meta.auth_collections where id=$1`, id)
	if isAuthCollectionDeleteConflict(err) {
		return ErrConflict
	}
	if database.Classify(err) == database.KindConflict {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func isAuthCollectionDeleteConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "23503" || pgErr.Code == "23001")
}

func (s *Service) ListAuthCollections(ctx context.Context) ([]AuthCollection, error) {
	rows, err := s.pool.Query(ctx, `select `+authCollectionColumns+` from pgbase_meta.auth_collections order by name limit 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]AuthCollection, 0)
	for rows.Next() {
		var v AuthCollection
		if err := scanAuthCollection(rows, &v); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}

func (s *Service) GetAuthCollection(ctx context.Context, name string) (AuthCollection, error) {
	if !authCollectionName.MatchString(name) {
		return AuthCollection{}, ErrValidation
	}
	var v AuthCollection
	err := scanAuthCollection(s.pool.QueryRow(ctx, `select `+authCollectionColumns+` from pgbase_meta.auth_collections where name=$1`, name), &v)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthCollection{}, ErrNotFound
	}
	if err != nil {
		return AuthCollection{}, err
	}
	return v, nil
}

const authCollectionColumns = `id,name,registration_enabled,profile_schema,fields_json,list_rule,view_rule,create_rule,update_rule,delete_rule,auth_rule,manage_rule,record_id,created_at,updated_at`

type rowScanner interface{ Scan(...any) error }

func scanAuthCollection(row rowScanner, value *AuthCollection) error {
	var profileRaw, fieldsRaw, recordIDRaw []byte
	var rules [7]*string
	if err := row.Scan(&value.ID, &value.Name, &value.RegistrationEnabled, &profileRaw, &fieldsRaw, &rules[0], &rules[1], &rules[2], &rules[3], &rules[4], &rules[5], &rules[6], &recordIDRaw, &value.CreatedAt, &value.UpdatedAt); err != nil {
		return err
	}
	applyNullableAuthRules(&value.Rules, rules)
	if json.Unmarshal(profileRaw, &value.ProfileSchema) != nil || json.Unmarshal(fieldsRaw, &value.Fields) != nil || json.Unmarshal(recordIDRaw, &value.RecordID) != nil {
		return errors.New("invalid stored auth collection")
	}
	return nil
}

func validProfileSchema(schema map[string]string) bool {
	if len(schema) > 64 {
		return false
	}
	for name, kind := range schema {
		if !authCollectionName.MatchString(name) {
			return false
		}
		switch kind {
		case "boolean", "integer", "numeric", "text", "timestamptz", "uuid", "jsonb":
		default:
			return false
		}
	}
	return true
}

func (s *Service) SetProfile(ctx context.Context, userID uuid.UUID, attributes map[string]any) (map[string]any, error) {
	if userID == uuid.Nil || len(attributes) > 64 {
		return nil, ErrValidation
	}
	var public map[string]any
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var fieldsRaw []byte
		var updateRule string
		var existingRaw []byte
		if err := tx.QueryRow(ctx, `select c.fields_json,c.update_rule,coalesce(p.attributes,'{}') from pgbase_meta.users u join pgbase_meta.auth_collections c on c.id=u.auth_collection_id left join pgbase_meta.user_auth_profiles p on p.user_id=u.id where u.id=$1 and u.active for update of u`, userID).Scan(&fieldsRaw, &updateRule, &existingRaw); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		var fields []schema.FieldInput
		if json.Unmarshal(fieldsRaw, &fields) != nil {
			return ErrValidation
		}
		if err := s.resolveAuthRelationIdentities(ctx, tx, attributes, fields); err != nil {
			return err
		}
		changes, ok := prepareAuthPasswordFields(fields, attributes, false)
		if !ok || !ruleAllowsOperation(updateRule, userID, userID) {
			return ErrValidation
		}
		merged := map[string]any{}
		if json.Unmarshal(existingRaw, &merged) != nil {
			return ErrValidation
		}
		for name, value := range changes {
			if value == nil {
				delete(merged, name)
			} else {
				merged[name] = value
			}
		}
		merged = applyAuthAutoDates(merged, fields, false, time.Now())
		if !validateTypedProfile(merged, fields, true) {
			return ErrValidation
		}
		if err := validateAuthRelationTargets(ctx, tx, userID, merged, fields); err != nil {
			return err
		}
		var collectionID uuid.UUID
		if err := tx.QueryRow(ctx, `select auth_collection_id from pgbase_meta.users where id=$1 for share`, userID).Scan(&collectionID); err != nil {
			return err
		}
		policy, err := authPolicyTx(ctx, tx, collectionID)
		if err != nil {
			return err
		}
		if err := validateAuthIdentityUnique(ctx, tx, collectionID, userID, merged, policy.IdentityFields); err != nil {
			return err
		}
		raw, marshalErr := json.Marshal(merged)
		if marshalErr != nil {
			return ErrValidation
		}
		if _, err := tx.Exec(ctx, `insert into pgbase_meta.user_auth_profiles(user_id,attributes,updated_at) values($1,$2,CURRENT_TIMESTAMP) on conflict(user_id) do update set attributes=excluded.attributes,updated_at=excluded.updated_at`, userID, raw); err != nil {
			return err
		}
		public = publicAuthFields(merged, fields)
		if err := s.projectAuthRelationIdentities(ctx, tx, public, fields); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return public, nil
}

func authPolicyTx(ctx context.Context, tx pgx.Tx, collectionID uuid.UUID) (AuthPolicy, error) {
	var p AuthPolicy
	err := tx.QueryRow(ctx, `select password_enabled,minimum_password_bytes,maximum_password_bytes,password_pattern,identity_fields,mfa_enabled,mfa_duration_seconds,mfa_rule,otp_enabled,otp_duration_seconds,otp_max_attempts from pgbase_meta.auth_collection_policies where auth_collection_id=$1`, collectionID).Scan(&p.PasswordEnabled, &p.MinimumPasswordBytes, &p.MaximumPasswordBytes, &p.PasswordPattern, &p.IdentityFields, &p.MFAEnabled, &p.MFADurationSeconds, &p.MFARule, &p.OTPEnabled, &p.OTPDurationSeconds, &p.OTPMaxAttempts)
	return p, err
}

func (s *Service) GetProfile(ctx context.Context, userID uuid.UUID) (map[string]any, error) {
	if userID == uuid.Nil {
		return nil, ErrValidation
	}
	var raw, fieldsRaw []byte
	err := s.pool.QueryRow(ctx, `select coalesce(p.attributes,'{}'),c.fields_json from pgbase_meta.users u join pgbase_meta.auth_collections c on c.id=u.auth_collection_id left join pgbase_meta.user_auth_profiles p on p.user_id=u.id where u.id=$1`, userID).Scan(&raw, &fieldsRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var value map[string]any
	var fields []schema.FieldInput
	if json.Unmarshal(raw, &value) != nil || json.Unmarshal(fieldsRaw, &fields) != nil {
		return nil, errors.New("invalid stored auth profile")
	}
	value = publicAuthFields(value, fields)
	if err := s.projectAuthRelationIdentities(ctx, s.pool, value, fields); err != nil {
		return nil, err
	}
	return value, nil
}

func validateProfile(attributes map[string]any, schema map[string]string) bool {
	for name, value := range attributes {
		kind, ok := schema[name]
		if !ok {
			return false
		}
		switch kind {
		case "boolean":
			if _, ok := value.(bool); !ok {
				return false
			}
		case "integer":
			n, ok := value.(float64)
			if !ok || n != float64(int64(n)) {
				return false
			}
		case "numeric":
			if _, ok := value.(float64); !ok {
				return false
			}
		case "text":
			if v, ok := value.(string); !ok || len(v) > 4096 {
				return false
			}
		case "timestamptz":
			v, ok := value.(string)
			if !ok {
				return false
			}
			if _, err := time.Parse(time.RFC3339, v); err != nil {
				return false
			}
		case "uuid":
			v, ok := value.(string)
			if !ok {
				return false
			}
			if _, err := uuid.Parse(v); err != nil {
				return false
			}
		case "jsonb":
			if _, err := json.Marshal(value); err != nil {
				return false
			}
		default:
			return false
		}
	}
	raw, err := json.Marshal(attributes)
	return err == nil && len(raw) <= 64<<10
}
