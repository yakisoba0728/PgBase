//go:build integration

package userauth

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/passwordfield"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestTypedAuthCollectionRoundTripCRUDRulesAndOrigins(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	id := uuid.New()
	min := 1
	fields := []schema.FieldInput{{Name: "display_name", Type: schema.TypeText, Required: true, Options: &schema.FieldOptions{MinLength: &min}}}
	rules := AuthCollectionRules{List: "true", View: "true", Create: "true", Update: "@request.auth.id = id", Delete: "@request.auth.id = id", Auth: "false", Manage: "@request.auth.id = id"}
	created, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: id, Name: "typed_" + id.String()[:8], RegistrationEnabled: true, Fields: fields, Rules: rules})
	if err != nil || len(created.Fields) != 1 || created.Rules.Auth != "false" {
		t.Fatalf("create=%#v err=%v", created, err)
	}
	loaded, err := service.GetAuthCollection(ctx, created.Name)
	if err != nil || loaded.Fields[0].Options.MinLength == nil || !reflect.DeepEqual(loaded.Rules, rules) {
		t.Fatalf("roundtrip=%#v err=%v", loaded, err)
	}
	if _, err := service.CreateAuthRecord(ctx, id, CreateAuthRecordInput{Email: "bad@example.test", Password: "correct horse battery staple", Active: true, Fields: map[string]any{}}); !errors.Is(err, ErrValidation) {
		t.Fatalf("required field err=%v", err)
	}
	record, err := service.CreateAuthRecord(ctx, id, CreateAuthRecordInput{Email: "typed@example.test", Password: "correct horse battery staple", Active: true, Fields: map[string]any{"display_name": "Ada"}})
	if err != nil || record.Fields["display_name"] != "Ada" {
		t.Fatalf("record=%#v err=%v", record, err)
	}
	listed, err := service.ListAuthorizedAuthRecords(ctx, id, record.ID, 10)
	if err != nil || len(listed) != 1 || listed[0].ID != record.ID {
		t.Fatalf("listRule result=%#v err=%v", listed, err)
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
	if err := service.DeleteAuthorizedAuthRecord(ctx, id, record.ID, record.ID); err != nil {
		t.Fatal(err)
	}
}

func TestAuthCollectionCreateRuleControlsPublicRegistration(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	id := uuid.New()
	rules := AuthCollectionRules{Create: "false", Auth: "true"}
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: id, Name: "create_rule_" + id.String()[:8], RegistrationEnabled: true, Rules: rules}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterInCollection(ctx, id, "blocked@example.test", "correct horse battery staple", "198.51.100.40"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("false create rule registration=%v", err)
	}
	rules.Create = "true"
	if _, err := service.UpdateAuthCollection(ctx, id, UpdateAuthCollectionInput{RegistrationEnabled: true, Rules: rules}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterInCollection(ctx, id, "allowed@example.test", "correct horse battery staple", "198.51.100.40"); err != nil {
		t.Fatalf("true create rule registration=%v", err)
	}
}

func TestAuthRuleEvaluatesJSONArraysAndAuthRelationsBeforeEverySession(t *testing.T) {
	pool := newUserAuthPool(t)
	service := NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	managersID := uuid.New()
	managersName := "rule_managers_" + managersID.String()[:8]
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: managersID, Name: managersName}); err != nil {
		t.Fatal(err)
	}
	boss, err := service.CreateAuthRecord(ctx, managersID, CreateAuthRecordInput{Email: "boss@example.test", Password: "correct horse battery staple", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	membersID := uuid.New()
	relation := schema.RelationInput{TargetCollection: managersName, Cardinality: schema.RelationMultiple, OnDelete: schema.RelationRestrict}
	fields := []schema.FieldInput{
		{Name: "settings", Type: schema.TypeJSONB},
		{Name: "tags", Type: schema.TypeJSONB},
		{Name: "managers", Type: schema.TypeRelation, Relation: &relation},
	}
	rules := AuthCollectionRules{Create: "true", Auth: `settings.level = 'gold' && tags ?= 'blue' && managers.email ?= 'boss@example.test' && @collection.` + managersName + `.email ?= 'boss@example.test'`}
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: membersID, Name: "rule_members_" + membersID.String()[:8], RegistrationEnabled: true, Fields: fields, Rules: rules}); err != nil {
		t.Fatalf("create JSON/relation rule: %v", err)
	}
	member, err := service.CreateAuthRecord(ctx, membersID, CreateAuthRecordInput{
		Email: "member@example.test", Password: "correct horse battery staple", Active: true,
		Fields: map[string]any{"settings": map[string]any{"level": "gold"}, "tags": []any{"blue", "green"}, "managers": []any{boss.ID.String()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.InTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		allowed, err := EvaluateAuthRecordRuleInTx(ctx, tx, membersID, rules.Auth, AuthRuleContext{Principal: identity.Subject{Kind: identity.User, ID: member.ID}, AuthEmail: member.Email, Current: member})
		if err != nil {
			return err
		}
		if !allowed {
			return errors.New("matching composite auth rule evaluated false")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		allowed, err := service.authorizeSessionTx(ctx, tx, User{ID: member.ID, AuthCollectionID: membersID, Email: member.Email})
		if err != nil {
			return err
		}
		if !allowed {
			return errors.New("shared session policy evaluated false")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var passwordHash string
	if err := pool.QueryRow(ctx, `select password_hash from pgbase_meta.users where id=$1`, member.ID).Scan(&passwordHash); err != nil {
		t.Fatal(err)
	}
	if _, err := service.issueCheckedSession(ctx, User{ID: member.ID, AuthCollectionID: membersID, Email: member.Email}, passwordHash, time.Now()); err != nil {
		t.Fatalf("shared issue path rejected matching rule: %v", err)
	}
	if _, err := service.UpdateAuthRecord(ctx, membersID, member.ID, UpdateAuthRecordInput{Fields: map[string]any{"settings": map[string]any{"level": "silver"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.issueCheckedSession(ctx, User{ID: member.ID, AuthCollectionID: membersID, Email: member.Email}, passwordHash, time.Now()); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("shared session policy allowed nonmatching record: %v", err)
	}
}

func TestManagedAuthRecordMutationUsesDistinctActorAndRevokesTargetCredentials(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	collectionID := uuid.New()
	hidden := schema.FieldInput{Name: "internal_note", Type: schema.TypeText, Hidden: true}
	managerFlag := schema.FieldInput{Name: "can_manage", Type: schema.TypeBoolean}
	rules := AuthCollectionRules{
		Create: "true", Update: "true", Auth: "true",
		Manage: `@request.auth.email = 'manager@example.test' && @request.auth.can_manage = true && id != @request.auth.id && @request.body.password:isset = true`,
	}
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{
		ID: collectionID, Name: "managed_" + collectionID.String()[:8], RegistrationEnabled: true,
		Fields: []schema.FieldInput{hidden, managerFlag}, Rules: rules,
	}); err != nil {
		t.Fatal(err)
	}
	manager, err := service.CreateAuthRecord(ctx, collectionID, CreateAuthRecordInput{
		Email: "manager@example.test", Password: "correct horse battery staple", Active: true, Fields: map[string]any{"can_manage": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.CreateAuthRecord(ctx, collectionID, CreateAuthRecordInput{
		Email: "target@example.test", Password: "correct horse battery staple", Active: true,
		Fields: map[string]any{"internal_note": "never return this", "can_manage": false},
	})
	if err != nil {
		t.Fatal(err)
	}
	targetSession, err := service.LoginInCollection(ctx, collectionID, target.Email, "correct horse battery staple", "198.51.100.30")
	if err != nil {
		t.Fatal(err)
	}
	verified := true
	newPassword := "replacement correct horse battery staple"
	updated, err := service.UpdateAuthorizedAuthRecord(ctx, collectionID, manager.ID, target.ID, UpdateAuthRecordInput{
		Password: &newPassword, Verified: &verified,
	})
	if err != nil {
		t.Fatalf("manager update: %v", err)
	}
	if !updated.Verified || updated.ID != target.ID || updated.Fields["internal_note"] != nil {
		t.Fatalf("managed response exposed or changed the wrong record: %#v", updated)
	}
	if _, err := service.AuthenticateForCollection(ctx, collectionID, targetSession.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("managed credential update retained target session: %v", err)
	}
	if _, err := service.LoginInCollection(ctx, collectionID, target.Email, newPassword, "198.51.100.31"); err != nil {
		t.Fatalf("managed password was not authoritative: %v", err)
	}

	active := false
	if _, err := service.UpdateAuthorizedAuthRecord(ctx, collectionID, target.ID, manager.ID, UpdateAuthRecordInput{Active: &active}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("target gained manager privilege: %v", err)
	}
	otherCollection := uuid.New()
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: otherCollection, Name: "other_" + otherCollection.String()[:8]}); err != nil {
		t.Fatal(err)
	}
	outsider, err := service.CreateAuthRecord(ctx, otherCollection, CreateAuthRecordInput{Email: "manager@example.test", Password: "correct horse battery staple", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateAuthorizedAuthRecord(ctx, collectionID, outsider.ID, target.ID, UpdateAuthRecordInput{Active: &active}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("cross-realm manager accepted: %v", err)
	}
	rules.Manage = "false"
	if _, err := service.UpdateAuthCollection(ctx, collectionID, UpdateAuthCollectionInput{RegistrationEnabled: true, Fields: []schema.FieldInput{hidden, managerFlag}, Rules: rules}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateAuthorizedAuthRecord(ctx, collectionID, manager.ID, target.ID, UpdateAuthRecordInput{Active: &active}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("false manage rule accepted: %v", err)
	}
}

func TestPrivateAuthEmailRequiresActiveSameRealmManager(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	collectionID := uuid.New()
	managerFlag := schema.FieldInput{Name: "can_manage", Type: schema.TypeBoolean}
	rules := AuthCollectionRules{List: "true", View: "true", Create: "true", Auth: "true", Manage: "@request.auth.can_manage = true && id != @request.auth.id"}
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: collectionID, Name: "email_managed_" + collectionID.String()[:8], RegistrationEnabled: true, Fields: []schema.FieldInput{managerFlag}, Rules: rules}); err != nil {
		t.Fatal(err)
	}
	manager, err := service.CreateAuthRecord(ctx, collectionID, CreateAuthRecordInput{Email: "email-manager@example.test", Password: "correct horse battery staple", Active: true, Fields: map[string]any{"can_manage": true}})
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.CreateAuthRecord(ctx, collectionID, CreateAuthRecordInput{Email: "email-target@example.test", Password: "correct horse battery staple", Active: true, Fields: map[string]any{"can_manage": false}})
	if err != nil {
		t.Fatal(err)
	}
	assertPrivateAuthEmail := func(actor uuid.UUID, want string) {
		t.Helper()
		record, err := service.GetAuthorizedAuthRecord(ctx, collectionID, actor, target.ID)
		if err != nil || record.Email != want {
			t.Fatalf("actor=%s email=%q want=%q err=%v", actor, record.Email, want, err)
		}
	}
	assertPrivateAuthEmail(manager.ID, target.Email)

	active := false
	if _, err := service.UpdateAuthRecord(ctx, collectionID, manager.ID, UpdateAuthRecordInput{Active: &active}); err != nil {
		t.Fatal(err)
	}
	assertPrivateAuthEmail(manager.ID, "")

	otherCollectionID := uuid.New()
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: otherCollectionID, Name: "email_other_" + otherCollectionID.String()[:8], RegistrationEnabled: true}); err != nil {
		t.Fatal(err)
	}
	outsider, err := service.CreateAuthRecord(ctx, otherCollectionID, CreateAuthRecordInput{Email: "email-outsider@example.test", Password: "correct horse battery staple", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	assertPrivateAuthEmail(outsider.ID, "")

	active = true
	if _, err := service.UpdateAuthRecord(ctx, collectionID, manager.ID, UpdateAuthRecordInput{Active: &active}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		rule string
		null bool
	}{
		{name: "false", rule: "false"},
		{name: "empty", rule: ""},
		{name: "null", null: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			updatedRules := rules
			updatedRules.Manage = test.rule
			if test.null {
				updatedRules.nulls = map[string]bool{"manage": true}
			}
			if _, err := service.UpdateAuthCollection(ctx, collectionID, UpdateAuthCollectionInput{RegistrationEnabled: true, Fields: []schema.FieldInput{managerFlag}, Rules: updatedRules}); err != nil {
				t.Fatal(err)
			}
			assertPrivateAuthEmail(manager.ID, "")
		})
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
