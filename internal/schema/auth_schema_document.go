package schema

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/authpolicy"
)

type SchemaDocumentAuthCollection struct {
	Name                string                   `json:"name"`
	RegistrationEnabled bool                     `json:"registrationEnabled"`
	Fields              []FieldInput             `json:"fields,omitempty"`
	RecordID            *RecordIDPolicy          `json:"recordId,omitempty"`
	Rules               json.RawMessage          `json:"rules"`
	Policy              SchemaDocumentAuthPolicy `json:"policy"`
	TokenTTLs           map[string]int           `json:"tokenTTLs,omitempty"`
}
type SchemaDocumentAuthPolicy struct {
	PasswordEnabled      bool     `json:"passwordEnabled"`
	MinimumPasswordBytes int      `json:"minimumPasswordBytes"`
	MaximumPasswordBytes int      `json:"maximumPasswordBytes"`
	PasswordPattern      string   `json:"passwordPattern"`
	IdentityFields       []string `json:"identityFields"`
	MFAEnabled           bool     `json:"mfaEnabled"`
	MFADurationSeconds   int      `json:"mfaDurationSeconds"`
	MFARule              string   `json:"mfaRule"`
	OTPEnabled           bool     `json:"otpEnabled"`
	OTPDurationSeconds   int      `json:"otpDurationSeconds"`
	OTPMaxAttempts       int      `json:"otpMaxAttempts"`
}

var portableAuthName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func exportDocumentAuthCollections(ctx context.Context, tx pgx.Tx) ([]SchemaDocumentAuthCollection, error) {
	rows, err := tx.Query(ctx, `select c.name,c.registration_enabled,c.fields_json,jsonb_build_object('list',c.list_rule,'view',c.view_rule,'create',c.create_rule,'update',c.update_rule,'delete',c.delete_rule,'auth',c.auth_rule,'manage',c.manage_rule),p.password_enabled,p.minimum_password_bytes,p.maximum_password_bytes,p.password_pattern,p.identity_fields,p.mfa_enabled,p.mfa_duration_seconds,p.mfa_rule,p.otp_enabled,p.otp_duration_seconds,p.otp_max_attempts,coalesce((select jsonb_object_agg(t.purpose,t.ttl_seconds) from pgbase_meta.auth_token_policies t where t.auth_collection_id=c.id),'{}'),c.record_id from pgbase_meta.auth_collections c join pgbase_meta.auth_collection_policies p on p.auth_collection_id=c.id order by c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SchemaDocumentAuthCollection{}
	for rows.Next() {
		var fields, rules, identities, tokens, recordIDRaw []byte
		var v SchemaDocumentAuthCollection
		if err := rows.Scan(&v.Name, &v.RegistrationEnabled, &fields, &rules, &v.Policy.PasswordEnabled, &v.Policy.MinimumPasswordBytes, &v.Policy.MaximumPasswordBytes, &v.Policy.PasswordPattern, &identities, &v.Policy.MFAEnabled, &v.Policy.MFADurationSeconds, &v.Policy.MFARule, &v.Policy.OTPEnabled, &v.Policy.OTPDurationSeconds, &v.Policy.OTPMaxAttempts, &tokens, &recordIDRaw); err != nil {
			return nil, err
		}
		if json.Unmarshal(fields, &v.Fields) != nil || json.Unmarshal(identities, &v.Policy.IdentityFields) != nil || json.Unmarshal(tokens, &v.TokenTTLs) != nil {
			return nil, ErrCatalogInvalid
		}
		var recordID RecordIDPolicy
		if json.Unmarshal(recordIDRaw, &recordID) != nil {
			return nil, ErrCatalogInvalid
		}
		if recordID.Kind == RecordIDText {
			v.RecordID = &recordID
		}
		v.Rules = append(json.RawMessage(nil), rules...)
		result = append(result, v)
	}
	return result, rows.Err()
}

func validateDocumentAuthCollections(values []SchemaDocumentAuthCollection) error {
	seen := map[string]bool{}
	for _, v := range values {
		if !portableAuthName.MatchString(v.Name) || seen[v.Name] {
			return validation("authCollections", "invalid")
		}
		seen[v.Name] = true
		if _, err := NormalizeRecordIDPolicy(v.RecordID); err != nil {
			return validation("authCollections."+v.Name+".recordId", "invalid")
		}
		fieldNames := map[string]bool{}
		for _, field := range v.Fields {
			if validateInputName("field", field.Name) != nil || fieldNames[field.Name] || validateFieldOptions(field.Type, field.Options) != nil {
				return validation("authCollections."+v.Name+".fields", "invalid")
			}
			fieldNames[field.Name] = true
		}
		var rules map[string]*json.RawMessage
		if json.Unmarshal(v.Rules, &rules) != nil {
			return validation("authCollections."+v.Name+".rules", "invalid")
		}
		for name, raw := range rules {
			known := false
			for _, n := range []string{"list", "view", "create", "update", "delete", "auth", "manage"} {
				if name == n {
					known = true
				}
			}
			if !known {
				return validation("authCollections."+v.Name+".rules", "invalid")
			}
			if raw != nil && string(*raw) != "null" {
				var source string
				if json.Unmarshal(*raw, &source) != nil {
					return validation("authCollections."+v.Name+".rules", "invalid")
				}
			}
		}
		if v.Policy.MinimumPasswordBytes < 12 || v.Policy.MaximumPasswordBytes < v.Policy.MinimumPasswordBytes || v.Policy.MaximumPasswordBytes > 4096 || v.Policy.MFADurationSeconds < 60 || v.Policy.MFADurationSeconds > 86400 || v.Policy.OTPDurationSeconds < 60 || v.Policy.OTPDurationSeconds > 86400 || v.Policy.OTPMaxAttempts < 1 {
			return validation("authCollections."+v.Name+".policy", "invalid")
		}
		if !documentIdentityFieldsMatchSchema(v.Policy.IdentityFields, v.Fields) {
			return validation("authCollections."+v.Name+".policy.identityFields", "invalid")
		}
		for purpose, ttl := range v.TokenTTLs {
			if !map[string]bool{"verify": true, "password_reset": true, "email_change": true, "session": true, "otp": true, "impersonation": true}[purpose] || ttl < 60 || ttl > 86400 {
				return validation("authCollections."+v.Name+".tokenTTLs", "invalid")
			}
		}
	}
	return nil
}

func documentIdentityFieldsMatchSchema(identityFields []string, fields []FieldInput) bool {
	if len(identityFields) == 0 || len(identityFields) > 8 {
		return false
	}
	available := make(map[string]FieldInput, len(fields))
	for _, field := range fields {
		available[field.Name] = field
	}
	seen := make(map[string]bool, len(identityFields))
	for _, name := range identityFields {
		if seen[name] || name != "email" && !portableAuthName.MatchString(name) {
			return false
		}
		seen[name] = true
		if name == "email" {
			continue
		}
		field, ok := available[name]
		if !ok || field.Type != TypeText || field.Hidden {
			return false
		}
	}
	return true
}

func (s *Service) validateDocumentAuthExecutables(values []SchemaDocumentAuthCollection) error {
	for _, value := range values {
		if source := value.Policy.PasswordPattern; source != "" && (s.authPolicyValidator == nil || !s.authPolicyValidator.ValidatePasswordPattern(source)) {
			return validation("authCollections."+value.Name+".policy.passwordPattern", "invalid")
		}
		if source := strings.TrimSpace(value.Policy.MFARule); source != "" && (s.authPolicyValidator == nil || !s.authPolicyValidator.ValidateMFARule(source)) {
			return validation("authCollections."+value.Name+".policy.mfaRule", "invalid")
		}
		var rules map[string]*string
		if err := json.Unmarshal(value.Rules, &rules); err != nil {
			return validation("authCollections."+value.Name+".rules", "invalid")
		}
		for _, name := range []string{"list", "view", "create", "update", "delete", "auth", "manage"} {
			source := rules[name]
			if source == nil || strings.TrimSpace(*source) == "" {
				continue
			}
			if s.authPolicyValidator == nil || !s.authPolicyValidator.ValidateAuthRule(*source, value.Fields) {
				return validation("authCollections."+value.Name+".rules."+name, "invalid")
			}
		}
	}
	return nil
}

func validateDocumentAuthIdentityData(ctx context.Context, tx pgx.Tx, values []SchemaDocumentAuthCollection) error {
	for _, value := range values {
		for _, field := range value.Policy.IdentityFields {
			if field == "email" {
				continue
			}
			var duplicate bool
			if err := tx.QueryRow(ctx, `select exists(select 1 from pgbase_meta.user_auth_profiles p join pgbase_meta.users u on u.id=p.user_id join pgbase_meta.auth_collections c on c.id=u.auth_collection_id where c.name=$1 and coalesce(p.attributes ->> $2,'')<>'' group by p.attributes ->> $2 having count(*)>1)`, value.Name, field).Scan(&duplicate); err != nil {
				return err
			}
			if duplicate {
				return validation("authCollections."+value.Name+".policy.identityFields", "duplicate")
			}
		}
	}
	return nil
}

func (s *Service) applyDocumentAuthCollections(ctx context.Context, tx pgx.Tx, catalog Catalog, values []SchemaDocumentAuthCollection) error {
	for _, v := range values {
		var rules map[string]*string
		if json.Unmarshal(v.Rules, &rules) != nil {
			return ErrCatalogInvalid
		}
		if v.Fields == nil {
			v.Fields = []FieldInput{}
		}
		fieldRaw, _ := json.Marshal(v.Fields)
		identityRaw, _ := json.Marshal(v.Policy.IdentityFields)
		var id uuid.UUID
		var currentRecordIDRaw []byte
		err := tx.QueryRow(ctx, `select id,record_id from pgbase_meta.auth_collections where name=$1 for update`, v.Name).Scan(&id, &currentRecordIDRaw)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		recordID, policyErr := documentAuthRecordID(catalog, v, currentRecordIDRaw)
		if policyErr != nil {
			return policyErr
		}
		if errors.Is(err, pgx.ErrNoRows) {
			id = uuid.New()
			recordIDRaw, _ := json.Marshal(recordID)
			_, err = tx.Exec(ctx, `insert into pgbase_meta.auth_collections(id,name,registration_enabled,profile_schema,fields_json,list_rule,view_rule,create_rule,update_rule,delete_rule,auth_rule,manage_rule,record_id,created_at,updated_at) values($1,$2,$3,'{}',$4,$5,$6,$7,$8,$9,$10,$11,$12,clock_timestamp(),clock_timestamp())`, id, v.Name, v.RegistrationEnabled, fieldRaw, rules["list"], rules["view"], rules["create"], rules["update"], rules["delete"], rules["auth"], rules["manage"], recordIDRaw)
		} else if err == nil {
			_, err = tx.Exec(ctx, `update pgbase_meta.auth_collections set registration_enabled=$2,fields_json=$3,list_rule=$4,view_rule=$5,create_rule=$6,update_rule=$7,delete_rule=$8,auth_rule=$9,manage_rule=$10,updated_at=clock_timestamp() where id=$1`, id, v.RegistrationEnabled, fieldRaw, rules["list"], rules["view"], rules["create"], rules["update"], rules["delete"], rules["auth"], rules["manage"])
		}
		if err != nil {
			return err
		}
		var previous authpolicy.Policy
		if err = tx.QueryRow(ctx, `select password_enabled,minimum_password_bytes,mfa_enabled,otp_enabled from pgbase_meta.auth_collection_policies where auth_collection_id=$1 for update`, id).Scan(&previous.PasswordEnabled, &previous.MinimumPasswordBytes, &previous.MFAEnabled, &previous.OTPEnabled); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `update pgbase_meta.auth_collection_policies set password_enabled=$2,minimum_password_bytes=$3,maximum_password_bytes=$4,password_pattern=$5,identity_fields=$6,mfa_enabled=$7,mfa_duration_seconds=$8,mfa_rule=$9,otp_enabled=$10,otp_duration_seconds=$11,otp_max_attempts=$12,updated_at=clock_timestamp() where auth_collection_id=$1`, id, v.Policy.PasswordEnabled, v.Policy.MinimumPasswordBytes, v.Policy.MaximumPasswordBytes, v.Policy.PasswordPattern, identityRaw, v.Policy.MFAEnabled, v.Policy.MFADurationSeconds, v.Policy.MFARule, v.Policy.OTPEnabled, v.Policy.OTPDurationSeconds, v.Policy.OTPMaxAttempts)
		if err != nil {
			return err
		}
		if err = authpolicy.ApplyTransition(ctx, tx, id, previous, authpolicy.Policy{
			PasswordEnabled: v.Policy.PasswordEnabled, MinimumPasswordBytes: v.Policy.MinimumPasswordBytes,
			MFAEnabled: v.Policy.MFAEnabled, OTPEnabled: v.Policy.OTPEnabled,
		}); err != nil {
			return err
		}
		for purpose, ttl := range v.TokenTTLs {
			tag, e := tx.Exec(ctx, `update pgbase_meta.auth_token_policies set ttl_seconds=$3,updated_at=clock_timestamp() where auth_collection_id=$1 and purpose=$2`, id, purpose, ttl)
			if e != nil {
				return e
			}
			if tag.RowsAffected() != 1 {
				version, ciphertext, keyErr := s.newImportedAuthTokenKey(purpose)
				if keyErr != nil {
					return keyErr
				}
				if _, e = tx.Exec(ctx, `insert into pgbase_meta.auth_token_policies(auth_collection_id,purpose,ttl_seconds,key_version,key_ciphertext,updated_at) values($1,$2,$3,$4,$5,clock_timestamp())`, id, purpose, ttl, version, ciphertext); e != nil {
					return e
				}
			}
		}
	}
	return nil
}

func (s *Service) newImportedAuthTokenKey(purpose string) (string, []byte, error) {
	if len(s.authSecret) < 16 {
		return "", nil, validation("authCollections.tokenTTLs", "encryption unavailable")
	}
	raw := make([]byte, 32)
	versionRaw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	if _, err := rand.Read(versionRaw); err != nil {
		return "", nil, err
	}
	domain := "auth-token-policy:" + purpose
	m := hmac.New(sha256.New, s.authSecret)
	m.Write([]byte("encryption-key\x00"))
	m.Write([]byte(domain))
	key := sha256.Sum256(m.Sum(nil))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", nil, err
	}
	return hex.EncodeToString(versionRaw), gcm.Seal(nonce, nonce, raw, []byte(domain)), nil
}
