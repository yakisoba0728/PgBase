//go:build integration

package userauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
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
	origins, err := service.ListAuthAuxiliary(ctx, id, record.ID, "authOrigins")
	if err != nil || len(origins) != 1 || origins[0].FirstSeenAt == nil {
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
