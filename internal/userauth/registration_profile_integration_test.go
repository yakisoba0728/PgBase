//go:build integration

package userauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/passwordfield"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestPublicSignupProfileValidationAndPolicyRollback(t *testing.T) {
	for _, method := range []string{"password", "oauth"} {
		t.Run(method, func(t *testing.T) {
			service, _ := newEmailActionService(t)
			ctx := context.Background()
			id := uuid.New()
			minimum := 3
			fields := []schema.FieldInput{
				{Name: "display_name", Type: schema.TypeText, Required: true, Options: &schema.FieldOptions{MinLength: &minimum}},
				{Name: "joined_at", Type: schema.TypeAutoDate, Required: true, Options: &schema.FieldOptions{OnCreate: true}},
				{Name: "balance", Type: schema.TypeNumeric}, {Name: "large_count", Type: schema.TypeBigInt},
				{Name: "metadata", Type: schema.TypeJSONB}, {Name: "location", Type: schema.TypeGeoPoint},
				{Name: "recovery_pin", Type: schema.TypePassword}, {Name: "active", Type: schema.TypeBoolean},
			}
			rules := AuthCollectionRules{Create: "display_name = 'Allowed' && @request.body.fields:isset = true", View: "true", Update: "true"}
			if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: id, Name: "profile_" + id.String()[:8], RegistrationEnabled: true, Fields: fields, Rules: rules}); err != nil {
				t.Fatal(err)
			}
			address := 0
			register := func(input map[string]json.RawMessage) (Session, error) {
				address++
				return service.RegisterInCollectionWithFields(ctx, id, "profile@example.test", "correct horse battery staple", fmt.Sprintf("profile-%d", address), input)
			}
			var authorization OAuthAuthorization
			if method == "oauth" {
				redirect := "https://app.example/oauth/callback"
				if err := service.ConfigureOAuth(map[string]OAuthProvider{"local": realmOAuthProvider{identity: OAuthIdentity{Subject: "profile-subject", Email: "profile@example.test", EmailVerified: true}}}, []string{redirect}, time.Minute); err != nil {
					t.Fatal(err)
				}
				service.ConfigureOAuthRegistration(true)
				var err error
				authorization, err = service.BeginOAuthInCollection(ctx, id, "local", redirect, "profile-oauth")
				if err != nil {
					t.Fatal(err)
				}
				register = func(input map[string]json.RawMessage) (Session, error) {
					return service.CompleteOAuthWithFields(ctx, "local", authorization.State, "code", input)
				}
			}
			for _, tc := range []struct {
				name, input string
				denied      bool
			}{
				{"missing", `{}`, false}, {"wrong_type", `{"display_name":2}`, false},
				{"constraint", `{"display_name":"A"}`, false}, {"unknown", `{"display_name":"Allowed","extra":true}`, false},
				{"reserved_verified", `{"display_name":"Allowed","verified":true}`, false},
				{"reserved_declared_active", `{"display_name":"Allowed","active":false}`, false},
				{"readonly", `{"display_name":"Allowed","joined_at":"2000-01-01T00:00:00Z"}`, false},
				{"fractional_bigint", `{"display_name":"Allowed","large_count":1.5}`, false},
				{"overflow_bigint", `{"display_name":"Allowed","large_count":9223372036854775808}`, false},
				{"json_number_limit", `{"display_name":"Allowed","metadata":{"n":1e999999}}`, false},
				{"policy", `{"display_name":"Denied"}`, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					want := ErrValidation
					if tc.denied {
						want = ErrInvalidCredentials
					}
					if _, err := register(signupRawFields(t, tc.input)); !errors.Is(err, want) {
						t.Fatalf("signup=%v want %v", err, want)
					}
					records, err := service.ListAuthRecords(ctx, id, 10)
					if err != nil || len(records) != 0 {
						t.Fatalf("rejected signup persisted record: %#v %v", records, err)
					}
				})
			}
			input := signupRawFields(t, `{"display_name":"Allowed","balance":12345678901234567890.123456789,"large_count":9223372036854775807,"metadata":{"n":9007199254740993},"location":{"lon":127.123,"lat":37.5},"recovery_pin":"profile secret"}`)
			session, err := register(input)
			if err != nil {
				t.Fatal(err)
			}
			record, err := service.GetAuthRecord(ctx, id, session.User.ID)
			if err != nil || !record.Active || record.Verified != (method == "oauth") {
				t.Fatalf("wrong signup authority: %#v %v", record, err)
			}
			if _, exposed := record.Fields["recovery_pin"]; exposed {
				t.Fatal("custom password exposed")
			}
			if record.Fields["balance"] != json.Number("12345678901234567890.123456789") || record.Fields["large_count"] != json.Number("9223372036854775807") {
				t.Fatalf("numeric precision lost: %#v", record.Fields)
			}
			if _, err := time.Parse(time.RFC3339Nano, record.Fields["joined_at"].(string)); err != nil {
				t.Fatal(err)
			}
			var hash string
			if err := service.pool.QueryRow(ctx, `select attributes->>'recovery_pin' from pgbase_meta.user_auth_profiles where user_id=$1`, session.User.ID).Scan(&hash); err != nil || passwordfield.Verify(hash, "profile secret") != nil {
				t.Fatalf("custom password not hashed: %v", err)
			}
			updated, err := service.SetProfile(ctx, session.User.ID, map[string]any{"display_name": "Still Allowed"})
			if err != nil || updated["balance"] != json.Number("12345678901234567890.123456789") {
				t.Fatalf("unrelated patch lost numeric precision: %#v %v", updated, err)
			}
			if method == "oauth" {
				login, err := service.BeginOAuthInCollection(ctx, id, "local", "https://app.example/oauth/callback", "profile-existing")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := service.CompleteOAuthWithFields(ctx, "local", login.State, "code", signupRawFields(t, `{"display_name":"Overwritten"}`)); !errors.Is(err, ErrValidation) {
					t.Fatalf("existing OAuth login accepted profile: %v", err)
				}
				if _, err := service.CompleteOAuth(ctx, "local", login.State, "code"); err != nil {
					t.Fatalf("rejected profile consumed OAuth state: %v", err)
				}
				profile, err := service.GetProfile(ctx, session.User.ID)
				if err != nil || profile["display_name"] != "Still Allowed" {
					t.Fatalf("existing OAuth profile overwritten: %#v %v", profile, err)
				}
			}
		})
	}
}

func signupRawFields(t *testing.T, raw string) map[string]json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

func TestOAuthSignupProfileCannotMutateExplicitLink(t *testing.T) {
	service, _ := newEmailActionService(t)
	ctx := context.Background()
	user, err := service.Register(ctx, "link-profile@example.test", "correct horse battery staple", "link-profile")
	if err != nil {
		t.Fatal(err)
	}
	redirect := "https://app.example/oauth/callback"
	if err := service.ConfigureOAuth(map[string]OAuthProvider{"local": realmOAuthProvider{identity: OAuthIdentity{Subject: "link-profile-subject", Email: "link-profile@example.test", EmailVerified: true}}}, []string{redirect}, time.Minute); err != nil {
		t.Fatal(err)
	}
	state, err := service.BeginOAuthLink(ctx, user.User.ID, "correct horse battery staple", "local", redirect)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteOAuthWithFields(ctx, "local", state.State, "code", signupRawFields(t, `{"verified":true}`)); !errors.Is(err, ErrValidation) {
		t.Fatalf("link accepted signup profile: %v", err)
	}
	links, err := service.ListOAuthIdentities(ctx, user.User.ID)
	if err != nil || len(links) != 0 {
		t.Fatalf("rejected profile created identity: %#v %v", links, err)
	}
	if _, err := service.CompleteOAuth(ctx, "local", state.State, "code"); err != nil {
		t.Fatalf("rejected profile consumed link state: %v", err)
	}
	record, err := service.GetAuthRecord(ctx, DefaultCollectionID, user.User.ID)
	if err != nil || record.Verified || len(record.Fields) != 0 {
		t.Fatalf("link changed existing profile authority: %#v %v", record, err)
	}
}

func TestSignupProfileEnforcesRelationAndIdentityPolicies(t *testing.T) {
	service, _ := newEmailActionService(t)
	ctx := context.Background()
	target, err := service.Register(ctx, "team-profile@example.test", "correct horse battery staple", "team-profile")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	fields := []schema.FieldInput{
		{Name: "handle", Type: schema.TypeText, Required: true},
		{Name: "team", Type: schema.TypeRelation, Required: true, Relation: &schema.RelationInput{TargetCollection: "users", Cardinality: schema.RelationSingle}},
	}
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: id, Name: "identity_profile_" + id.String()[:8], RegistrationEnabled: true, Fields: fields, Rules: AuthCollectionRules{Create: "true"}}); err != nil {
		t.Fatal(err)
	}
	administration, err := service.GetAuthAdministration(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	administration.Policy.IdentityFields = []string{"email", "handle"}
	if _, err := service.SetAuthPolicy(ctx, id, administration.Policy); err != nil {
		t.Fatal(err)
	}
	input := signupRawFields(t, `{"handle":"member","team":"`+uuid.New().String()+`"}`)
	if _, err := service.RegisterInCollectionWithFields(ctx, id, "member@example.test", "correct horse battery staple", "missing-team", input); !errors.Is(err, ErrValidation) {
		t.Fatalf("missing relation target accepted: %v", err)
	}
	input["team"] = json.RawMessage(`"` + target.User.PublicID + `"`)
	member, err := service.RegisterInCollectionWithFields(ctx, id, "member@example.test", "correct horse battery staple", "valid-team", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterInCollectionWithFields(ctx, id, "duplicate@example.test", "correct horse battery staple", "duplicate-handle", input); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate identity accepted: %v", err)
	}
	records, err := service.ListAuthRecords(ctx, id, 10)
	if err != nil || len(records) != 1 || records[0].ID != member.User.ID {
		t.Fatalf("failed profile insert persisted: %#v %v", records, err)
	}
}
