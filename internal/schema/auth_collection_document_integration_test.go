//go:build integration

package schema

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/database"
)

type authExecutableValidator struct {
	rejectPassword bool
	rejectMFA      bool
	rejectRule     bool
}

func (v authExecutableValidator) ValidatePasswordPattern(string) bool { return !v.rejectPassword }
func (v authExecutableValidator) ValidateMFARule(string) bool         { return !v.rejectMFA }
func (v authExecutableValidator) ValidateAuthRule(string, []FieldInput) bool {
	return !v.rejectRule
}

func TestSchemaDocumentAuthCollectionNullableRulesAndPolicyRoundTrip(t *testing.T) {
	pool, service, _ := newSchemaServiceTest(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `update pgbase_meta.auth_collections set auth_rule=null,manage_rule=null,view_rule='' where id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.auth_collection_policies set minimum_password_bytes=19,maximum_password_bytes=99,otp_enabled=true where auth_collection_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.auth_token_policies(auth_collection_id,purpose,ttl_seconds,key_version,key_ciphertext,updated_at) values('00000000-0000-0000-0000-000000000001','session',600,$1,$2,now())`, uuid.NewString()[:32], []byte("source-ciphertext")); err != nil {
		t.Fatal(err)
	}
	document, err := service.ExportSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var users SchemaDocumentAuthCollection
	for _, v := range document.AuthCollections {
		if v.Name == "users" {
			users = v
		}
	}
	var rules map[string]*string
	if json.Unmarshal(users.Rules, &rules) != nil || rules["auth"] != nil || rules["manage"] != nil || rules["view"] == nil || *rules["view"] != "" {
		t.Fatalf("rules=%s", users.Rules)
	}
	if users.Policy.MinimumPasswordBytes != 19 || !users.Policy.OTPEnabled {
		t.Fatalf("policy=%+v", users.Policy)
	}
	targetPool, target, actor := newSchemaServiceTest(t)
	target.SetAuthExecutablePolicyValidator(authExecutableValidator{})
	target.authSecret = []byte("0123456789abcdef0123456789abcdef")
	plan, err := target.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatalf("preview=%#v auth=%+v", err, document.AuthCollections)
	}
	if _, err = target.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "auth-document-roundtrip-0001"}); err != nil {
		t.Fatal(err)
	}
	roundTrip, err := target.ExportSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range roundTrip.AuthCollections {
		if v.Name == "users" {
			if string(v.Rules) != string(users.Rules) || v.Policy.MinimumPasswordBytes != 19 || !v.Policy.OTPEnabled || v.TokenTTLs["session"] != 600 {
				t.Fatalf("roundtrip=%+v", v)
			}
			var cipher []byte
			if err := targetPool.QueryRow(ctx, `select key_ciphertext from pgbase_meta.auth_token_policies where auth_collection_id='00000000-0000-0000-0000-000000000001' and purpose='session'`).Scan(&cipher); err != nil || len(cipher) <= len("source-ciphertext") {
				t.Fatalf("generated token policy ciphertext=%d err=%v", len(cipher), err)
			}
			return
		}
	}
	t.Fatal("users auth collection missing")
}

func TestSchemaDocumentRejectsInvalidAuthPolicyBeforeMutation(t *testing.T) {
	pool, service, _ := newSchemaServiceTest(t)
	ctx := context.Background()
	document, err := service.ExportSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	document.AuthCollections[0].Policy.MinimumPasswordBytes = 1
	if _, err = service.PreviewSchemaImport(ctx, document); err == nil {
		t.Fatal("invalid auth policy preview accepted")
	}
	var minimum int
	if err = pool.QueryRow(ctx, `select minimum_password_bytes from pgbase_meta.auth_collection_policies where auth_collection_id=(select id from pgbase_meta.auth_collections where name=$1)`, document.AuthCollections[0].Name).Scan(&minimum); err != nil {
		t.Fatal(err)
	}
	if minimum == 1 {
		t.Fatal("failed preview mutated auth policy")
	}
}

func TestSchemaDocumentRejectsInvalidIdentityFieldsAtPreview(t *testing.T) {
	_, service, _ := newSchemaServiceTest(t)
	service.SetAuthExecutablePolicyValidator(authExecutableValidator{})
	ctx := context.Background()
	document, err := service.ExportSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		identities []string
		fields     []FieldInput
	}{
		{name: "empty", identities: []string{}},
		{name: "duplicate", identities: []string{"email", "email"}},
		{name: "missing", identities: []string{"handle"}},
		{name: "hidden", identities: []string{"handle"}, fields: []FieldInput{{Name: "handle", Type: TypeText, Hidden: true}}},
		{name: "non-text", identities: []string{"handle"}, fields: []FieldInput{{Name: "handle", Type: TypeInteger}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneSchemaDocument(document)
			candidate.AuthCollections[0].Policy.IdentityFields = test.identities
			candidate.AuthCollections[0].Fields = test.fields
			_, err := service.PreviewSchemaImport(ctx, candidate)
			assertAuthValidationPath(t, err, "authCollections.users.policy.identityFields")
		})
	}
}

func TestSchemaDocumentRejectsDuplicateStoredCustomIdentityAtPreview(t *testing.T) {
	pool, service, _ := newSchemaServiceTest(t)
	service.SetAuthExecutablePolicyValidator(authExecutableValidator{})
	ctx := context.Background()
	document, err := service.ExportSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	document.AuthCollections[0].Fields = []FieldInput{{Name: "handle", Type: TypeText}}
	document.AuthCollections[0].Policy.IdentityFields = []string{"handle"}
	first, second := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.users(id,email,password_hash,active,password_enabled,verified,auth_collection_id,email_visibility,created_at,updated_at) values($1,'first@example.test','test',true,true,false,'00000000-0000-0000-0000-000000000001',false,current_timestamp,current_timestamp),($2,'second@example.test','test',true,true,false,'00000000-0000-0000-0000-000000000001',false,current_timestamp,current_timestamp)`, first, second); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.user_auth_profiles(user_id,attributes,updated_at) values($1,'{"handle":"same"}',current_timestamp),($2,'{"handle":"same"}',current_timestamp)`, first, second); err != nil {
		t.Fatal(err)
	}
	_, err = service.PreviewSchemaImport(ctx, document)
	assertAuthValidationPath(t, err, "authCollections.users.policy.identityFields")
}

func TestSchemaDocumentAuthExecutableValidationIsPathSpecificAndCannotBeBypassedAtApply(t *testing.T) {
	_, service, actor := newSchemaServiceTest(t)
	ctx := context.Background()
	document, err := service.ExportSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	document.AuthCollections[0].Policy.PasswordPattern = "["
	service.SetAuthExecutablePolicyValidator(authExecutableValidator{rejectPassword: true})
	_, err = service.PreviewSchemaImport(ctx, document)
	assertAuthValidationPath(t, err, "authCollections."+document.AuthCollections[0].Name+".policy.passwordPattern")

	document.AuthCollections[0].Policy.PasswordPattern = "[A-Z]"
	document.AuthCollections[0].Rules = json.RawMessage(`{"list":"broken (","view":null,"create":null,"update":null,"delete":null,"auth":null,"manage":null}`)
	service.SetAuthExecutablePolicyValidator(authExecutableValidator{})
	plan, err := service.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	service.SetAuthExecutablePolicyValidator(authExecutableValidator{rejectRule: true})
	_, err = service.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "auth-executable-apply-0001"})
	assertAuthValidationPath(t, err, "authCollections."+document.AuthCollections[0].Name+".rules.list")
}

func assertAuthValidationPath(t *testing.T, err error, path string) {
	t.Helper()
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || len(validationErr.Details) != 1 || validationErr.Details[0].Field != path {
		t.Fatalf("validation error=%#v want path=%q", err, path)
	}
}

func TestSchemaDocumentAuthPolicyTransitionMatrix(t *testing.T) {
	tests := []struct {
		name     string
		previous authPolicyTransitionFixture
		next     authPolicyTransitionFixture
	}{
		{name: "password enable", previous: authPolicyTransitionFixture{minimum: 12, mfa: true}, next: authPolicyTransitionFixture{password: true, minimum: 12, mfa: true}},
		{name: "password disable", previous: authPolicyTransitionFixture{password: true, minimum: 12, mfa: true}, next: authPolicyTransitionFixture{minimum: 12, mfa: true}},
		{name: "minimum increase", previous: authPolicyTransitionFixture{password: true, minimum: 12, mfa: true}, next: authPolicyTransitionFixture{password: true, minimum: 24, mfa: true}},
		{name: "minimum decrease", previous: authPolicyTransitionFixture{password: true, minimum: 24, mfa: true}, next: authPolicyTransitionFixture{password: true, minimum: 12, mfa: true}},
		{name: "MFA enable", previous: authPolicyTransitionFixture{password: true, minimum: 12}, next: authPolicyTransitionFixture{password: true, minimum: 12, mfa: true}},
		{name: "MFA disable", previous: authPolicyTransitionFixture{password: true, minimum: 12, mfa: true}, next: authPolicyTransitionFixture{password: true, minimum: 12}},
		{name: "OTP enable", previous: authPolicyTransitionFixture{password: true, minimum: 12, mfa: true}, next: authPolicyTransitionFixture{password: true, minimum: 12, mfa: true, otp: true}},
		{name: "OTP disable", previous: authPolicyTransitionFixture{password: true, minimum: 12, mfa: true, otp: true}, next: authPolicyTransitionFixture{password: true, minimum: 12, mfa: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool, service, actor := newSchemaServiceTest(t)
			service.SetAuthExecutablePolicyValidator(authExecutableValidator{})
			ctx := context.Background()
			seedSchemaAuthTransition(t, pool, test.previous)
			document, err := service.ExportSchema(ctx)
			if err != nil {
				t.Fatal(err)
			}
			policy := &document.AuthCollections[0].Policy
			policy.PasswordEnabled = test.next.password
			policy.MinimumPasswordBytes = test.next.minimum
			policy.MFAEnabled = test.next.mfa
			policy.OTPEnabled = test.next.otp
			plan, err := service.PreviewSchemaImport(ctx, document)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "auth-transition-" + uuid.NewString()}); err != nil {
				t.Fatal(err)
			}
			got := readSchemaAuthTransitionState(t, pool)
			wantRevoked := test.previous.password && !test.next.password || test.next.minimum > test.previous.minimum || !test.previous.mfa && test.next.mfa || test.previous.otp && !test.next.otp
			if got.sessionRevoked != wantRevoked {
				t.Fatalf("session revoked=%t want=%t", got.sessionRevoked, wantRevoked)
			}
			if got.mfaRows != boolInt(test.next.mfa) || got.recoveryRows != boolInt(test.next.mfa) {
				t.Fatalf("MFA rows=%d recovery rows=%d want=%d", got.mfaRows, got.recoveryRows, boolInt(test.next.mfa))
			}
			wantOTPActive := !(test.previous.otp && !test.next.otp)
			if got.otpActive != wantOTPActive {
				t.Fatalf("OTP active=%t want=%t", got.otpActive, wantOTPActive)
			}
		})
	}
}

func TestSchemaDocumentAuthPolicyTransitionRollsBackWithImportFailure(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	service.SetAuthExecutablePolicyValidator(authExecutableValidator{})
	ctx := context.Background()
	previous := authPolicyTransitionFixture{password: true, minimum: 12, mfa: true, otp: true}
	seedSchemaAuthTransition(t, pool, previous)
	document, err := service.ExportSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	policy := &document.AuthCollections[0].Policy
	policy.PasswordEnabled = false
	policy.MFAEnabled = false
	policy.OTPEnabled = false
	if document.AuthCollections[0].TokenTTLs == nil {
		document.AuthCollections[0].TokenTTLs = map[string]int{}
	}
	document.AuthCollections[0].TokenTTLs["session"] = 600
	plan, err := service.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "auth-transition-rollback-0001"}); err == nil {
		t.Fatal("import unexpectedly succeeded without an auth token encryption key")
	}
	got := readSchemaAuthTransitionState(t, pool)
	if got.sessionRevoked || got.mfaRows != 1 || got.recoveryRows != 1 || !got.otpActive {
		t.Fatalf("failed import did not roll back transition state: %+v", got)
	}
	var password, mfa, otp bool
	if err := pool.QueryRow(ctx, `select password_enabled,mfa_enabled,otp_enabled from pgbase_meta.auth_collection_policies where auth_collection_id='00000000-0000-0000-0000-000000000001'`).Scan(&password, &mfa, &otp); err != nil {
		t.Fatal(err)
	}
	if !password || !mfa || !otp {
		t.Fatalf("failed import persisted policy password=%t mfa=%t otp=%t", password, mfa, otp)
	}
}

type authPolicyTransitionFixture struct {
	password bool
	minimum  int
	mfa      bool
	otp      bool
}

type authPolicyTransitionState struct {
	sessionRevoked bool
	mfaRows        int
	recoveryRows   int
	otpActive      bool
}

func seedSchemaAuthTransition(t *testing.T, pool *database.Pool, policy authPolicyTransitionFixture) {
	t.Helper()
	ctx := context.Background()
	const collectionID = "00000000-0000-0000-0000-000000000001"
	const userID = "019b0000-0000-7000-8000-000000000301"
	statements := []struct {
		query string
		args  []any
	}{
		{`update pgbase_meta.auth_collection_policies set password_enabled=$2,minimum_password_bytes=$3,mfa_enabled=$4,otp_enabled=$5 where auth_collection_id=$1`, []any{collectionID, policy.password, policy.minimum, policy.mfa, policy.otp}},
		{`insert into pgbase_meta.users(id,email,password_hash,active,password_enabled,verified,auth_collection_id,email_visibility,created_at,updated_at) values($1,'transition@example.test','test',true,true,false,$2,false,current_timestamp,current_timestamp)`, []any{userID, collectionID}},
		{`insert into pgbase_meta.user_sessions(id,user_id,token_hash,expires_at,created_at) values(gen_random_uuid(),$1,$2,current_timestamp+interval '1 hour',current_timestamp)`, []any{userID, make([]byte, 32)}},
		{`insert into pgbase_meta.user_totp_factors(user_id,secret_ciphertext,active,created_at,updated_at) values($1,$2,true,current_timestamp,current_timestamp)`, []any{userID, []byte("encrypted")}},
		{`insert into pgbase_meta.user_totp_recovery_codes(user_id,code_hash,created_at) values($1,$2,current_timestamp)`, []any{userID, make([]byte, 32)}},
		{`insert into pgbase_meta.user_email_otps(id,auth_collection_id,user_id,code_hash,max_attempts,expires_at) values(gen_random_uuid(),$1,$2,$3,5,current_timestamp+interval '5 minutes')`, []any{collectionID, userID, make([]byte, 32)}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func readSchemaAuthTransitionState(t *testing.T, pool *database.Pool) authPolicyTransitionState {
	t.Helper()
	const userID = "019b0000-0000-7000-8000-000000000301"
	var result authPolicyTransitionState
	if err := pool.QueryRow(context.Background(), `select revoked_at is not null,(select count(*) from pgbase_meta.user_totp_factors where user_id=$1),(select count(*) from pgbase_meta.user_totp_recovery_codes where user_id=$1),(select count(*)=1 from pgbase_meta.user_email_otps where user_id=$1 and consumed_at is null) from pgbase_meta.user_sessions where user_id=$1`, userID).Scan(&result.sessionRevoked, &result.mfaRows, &result.recoveryRows, &result.otpActive); err != nil {
		t.Fatal(err)
	}
	return result
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
