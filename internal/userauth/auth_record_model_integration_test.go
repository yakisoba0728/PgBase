//go:build integration

package userauth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/passwordfield"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestTypedAuthCollectionRoundTripCRUDRulesAndOrigins(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	id := uuid.New()
	min := 1
	fields := []schema.FieldInput{{Name: "display_name", Type: schema.TypeText, Required: true, Options: &schema.FieldOptions{MinLength: &min}}}
	rules := AuthCollectionRules{Create: "true", Update: "@request.auth.id = id", Auth: "false", Manage: "@request.auth.id = id"}
	created, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: id, Name: "typed_" + id.String()[:8], RegistrationEnabled: true, Fields: fields, Rules: rules})
	if err != nil || len(created.Fields) != 1 || created.Rules.Auth != "false" {
		t.Fatalf("create=%#v err=%v", created, err)
	}
	loaded, err := service.GetAuthCollection(ctx, created.Name)
	if err != nil || loaded.Fields[0].Options.MinLength == nil || loaded.Rules.Update != rules.Update {
		t.Fatalf("roundtrip=%#v err=%v", loaded, err)
	}
	if _, err := service.CreateAuthRecord(ctx, id, CreateAuthRecordInput{Email: "bad@example.test", Password: "correct horse battery staple", Active: true, Fields: map[string]any{}}); !errors.Is(err, ErrValidation) {
		t.Fatalf("required field err=%v", err)
	}
	record, err := service.CreateAuthRecord(ctx, id, CreateAuthRecordInput{Email: "typed@example.test", Password: "correct horse battery staple", Active: true, Fields: map[string]any{"display_name": "Ada"}})
	if err != nil || record.Fields["display_name"] != "Ada" {
		t.Fatalf("record=%#v err=%v", record, err)
	}
	if _, err := service.LoginInCollection(ctx, id, record.Email, "correct horse battery staple", "198.51.100.9"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("authRule refusal=%v", err)
	}
	rules.Auth = "true"
	if _, err := service.UpdateAuthCollection(ctx, id, UpdateAuthCollectionInput{RegistrationEnabled: true, Fields: fields, Rules: rules}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LoginInCollection(ctx, id, record.Email, "correct horse battery staple", "198.51.100.9"); err != nil {
		t.Fatal(err)
	}
	session, err := service.LoginInCollection(ctx, id, record.Email, "correct horse battery staple", "198.51.100.10")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateAuthorizedAuthRecord(ctx, id, record.ID, record.ID, UpdateAuthRecordInput{Fields: map[string]any{"display_name": "Ada Lovelace"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthenticateForCollection(ctx, id, session.RawToken); err != nil {
		t.Fatalf("field patch revoked session: %v", err)
	}
	newPassword := "another correct horse battery staple"
	if _, err := service.UpdateAuthRecord(ctx, id, record.ID, UpdateAuthRecordInput{Password: &newPassword}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthenticateForCollection(ctx, id, session.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("credential update retained session: %v", err)
	}
	rules.View = "false"
	if _, err := service.UpdateAuthCollection(ctx, id, UpdateAuthCollectionInput{RegistrationEnabled: true, Fields: fields, Rules: rules}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetAuthorizedAuthRecord(ctx, id, record.ID, record.ID); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("viewRule refusal=%v", err)
	}
	origins, err := service.ListAuthAuxiliary(ctx, id, record.ID, "authOrigins")
	if err != nil || len(origins) < 1 || origins[0].FirstSeenAt == nil {
		t.Fatalf("origins=%#v err=%v", origins, err)
	}
	if err := service.DeleteAuthAuxiliary(ctx, id, record.ID, "authOrigins", origins[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteAuthRecord(ctx, id, record.ID); err != nil {
		t.Fatal(err)
	}
}

func TestAuthRecordRelationValidatesTargetAndAppliesSetNull(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	targetID := uuid.New()
	targetName := "people_" + targetID.String()[:8]
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: targetID, Name: targetName, RegistrationEnabled: true}); err != nil {
		t.Fatal(err)
	}
	target, err := service.CreateAuthRecord(ctx, targetID, CreateAuthRecordInput{Email: "target@example.test", Password: "correct horse battery staple", Active: true, Fields: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	sourceID := uuid.New()
	relation := schema.RelationInput{TargetCollection: targetName, Cardinality: schema.RelationSingle, OnDelete: schema.RelationSetNull}
	fields := []schema.FieldInput{{Name: "manager", Type: schema.TypeRelation, Relation: &relation}}
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: sourceID, Name: "staff_" + sourceID.String()[:8], Fields: fields}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateAuthRecord(ctx, sourceID, CreateAuthRecordInput{Email: "badref@example.test", Password: "correct horse battery staple", Active: true, Fields: map[string]any{"manager": uuid.New().String()}}); !errors.Is(err, ErrValidation) {
		t.Fatalf("missing target=%v", err)
	}
	source, err := service.CreateAuthRecord(ctx, sourceID, CreateAuthRecordInput{Email: "source@example.test", Password: "correct horse battery staple", Active: true, Fields: map[string]any{"manager": target.ID.String()}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteAuthRecord(ctx, targetID, target.ID); err != nil {
		t.Fatal(err)
	}
	got, err := service.GetAuthRecord(ctx, sourceID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Fields["manager"]; ok {
		t.Fatalf("set-null retained relation: %#v", got.Fields)
	}
}

func TestCustomAuthPasswordFieldsHashRedactAndPatch(t *testing.T) {
	pool := newUserAuthPool(t)
	service := NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	collectionID := uuid.New()
	minimum, maximum := 8, 16
	fields := []schema.FieldInput{
		{Name: "display_name", Type: schema.TypeText},
		{Name: "recovery_pin", Type: schema.TypePassword, Required: true, Options: &schema.FieldOptions{MinLength: &minimum, MaxLength: &maximum, Pattern: `^[A-Z0-9]+$`}},
	}
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: collectionID, Name: "password_" + collectionID.String()[:8], RegistrationEnabled: true, Fields: fields}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateAuthRecord(ctx, collectionID, CreateAuthRecordInput{Email: "invalid-pin@example.test", Password: "correct horse battery staple", Active: true, Fields: map[string]any{"display_name": "Ada", "recovery_pin": "too-short"}}); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid custom password accepted: %v", err)
	}
	const pin = "ABCD1234"
	record, err := service.CreateAuthRecord(ctx, collectionID, CreateAuthRecordInput{Email: "custom-pin@example.test", Password: "correct horse battery staple", Active: true, Fields: map[string]any{"display_name": "Ada", "recovery_pin": pin}})
	if err != nil {
		t.Fatal(err)
	}
	if _, present := record.Fields["recovery_pin"]; present {
		t.Fatalf("create response leaked custom password: %#v", record.Fields)
	}
	var raw []byte
	var systemHash string
	if err := pool.QueryRow(ctx, `select p.attributes,u.password_hash from pgbase_meta.user_auth_profiles p join pgbase_meta.users u on u.id=p.user_id where p.user_id=$1`, record.ID).Scan(&raw, &systemHash); err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	storedPin, ok := stored["recovery_pin"].(string)
	if !ok || storedPin == pin || !strings.HasPrefix(storedPin, "$argon2id$") || passwordfield.Verify(storedPin, pin) != nil {
		t.Fatalf("custom password was not an Argon2 hash: %#v", stored["recovery_pin"])
	}
	loaded, err := service.GetAuthRecord(ctx, collectionID, record.ID)
	if err != nil || loaded.Fields["display_name"] != "Ada" {
		t.Fatalf("record=%#v err=%v", loaded, err)
	}
	if _, present := loaded.Fields["recovery_pin"]; present {
		t.Fatalf("get response leaked custom password: %#v", loaded.Fields)
	}
	profile, err := service.GetProfile(ctx, record.ID)
	if err != nil || profile["display_name"] != "Ada" {
		t.Fatalf("profile=%#v err=%v", profile, err)
	}
	if _, present := profile["recovery_pin"]; present {
		t.Fatalf("profile response leaked custom password: %#v", profile)
	}
	updated, err := service.UpdateAuthRecord(ctx, collectionID, record.ID, UpdateAuthRecordInput{Fields: map[string]any{"display_name": "Ada Lovelace", "recovery_pin": ""}})
	if err != nil || updated.Fields["display_name"] != "Ada Lovelace" {
		t.Fatalf("blank patch=%#v err=%v", updated, err)
	}
	if err := pool.QueryRow(ctx, `select p.attributes,u.password_hash from pgbase_meta.user_auth_profiles p join pgbase_meta.users u on u.id=p.user_id where p.user_id=$1`, record.ID).Scan(&raw, &systemHash); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored["recovery_pin"] != storedPin {
		t.Fatalf("blank patch changed password: %#v", stored["recovery_pin"])
	}
	if _, err := service.SetProfile(ctx, record.ID, map[string]any{"display_name": "Grace", "recovery_pin": ""}); err != nil {
		t.Fatalf("profile blank patch: %v", err)
	}
	if err := pool.QueryRow(ctx, `select p.attributes,u.password_hash from pgbase_meta.user_auth_profiles p join pgbase_meta.users u on u.id=p.user_id where p.user_id=$1`, record.ID).Scan(&raw, &systemHash); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored["recovery_pin"] != storedPin || stored["display_name"] != "Grace" {
		t.Fatalf("profile patch did not preserve custom password: %#v", stored)
	}
	const newPin = "ZXCV1234"
	profile, err = service.SetProfile(ctx, record.ID, map[string]any{"recovery_pin": newPin})
	if err != nil {
		t.Fatalf("profile password patch: %v", err)
	}
	if _, present := profile["recovery_pin"]; present {
		t.Fatalf("profile password response leaked hash: %#v", profile)
	}
	if err := pool.QueryRow(ctx, `select p.attributes,u.password_hash from pgbase_meta.user_auth_profiles p join pgbase_meta.users u on u.id=p.user_id where p.user_id=$1`, record.ID).Scan(&raw, &systemHash); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	currentPin, ok := stored["recovery_pin"].(string)
	if !ok || currentPin == storedPin || passwordfield.Verify(currentPin, newPin) != nil {
		t.Fatalf("profile password patch did not rotate hash: %#v", stored["recovery_pin"])
	}
	if passwordfield.Verify(systemHash, "correct horse battery staple") != nil {
		t.Fatal("custom password patch changed system credential password")
	}
}
