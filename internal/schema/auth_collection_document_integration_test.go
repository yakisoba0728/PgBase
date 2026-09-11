//go:build integration

package schema

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
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
