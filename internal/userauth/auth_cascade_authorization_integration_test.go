//go:build integration

package userauth

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"testing"
	"time"
)

func TestPublicAuthCascadeRequiresAffectedDeleteRules(t *testing.T) {
	for _, mode := range []string{"denied-descendant", "allowed-descendant", "trusted"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s := NewService(newUserAuthPool(t), stubPasswords{}, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
			createRealm := func(name, rule string, fields []schema.FieldInput) uuid.UUID {
				t.Helper()
				id := uuid.New()
				if _, err := s.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: id, Name: name, Fields: fields, Rules: AuthCollectionRules{Delete: rule}}); err != nil {
					t.Fatal(err)
				}
				return id
			}
			rootRealm := createRealm("cascade_roots", "@request.auth.id = id", nil)
			fields := []schema.FieldInput{{Name: "parent", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "cascade_roots", Cardinality: schema.RelationSingle, OnDelete: schema.RelationCascade}}}
			deniedRule := "false"
			if mode == "allowed-descendant" {
				deniedRule = "true"
			}
			bobRealm := createRealm("cascade_bob", deniedRule, fields)
			charlieRealm := createRealm("cascade_charlie", "true", fields)
			nullFields := []schema.FieldInput{{Name: "parent", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "cascade_roots", Cardinality: schema.RelationSingle, OnDelete: schema.RelationSetNull}}}
			survivorRealm := createRealm("cascade_survivors", "false", nullFields)
			create := func(realm uuid.UUID, email string, profile map[string]any) AuthRecord {
				t.Helper()
				v, err := s.CreateAuthRecord(ctx, realm, CreateAuthRecordInput{Email: email, Password: "correct horse battery staple", Active: true, Fields: profile})
				if err != nil {
					t.Fatal(err)
				}
				return v
			}
			alice := create(rootRealm, "alice@example.test", nil)
			profile := func() map[string]any { return map[string]any{"parent": alice.ID.String()} }
			bob := create(bobRealm, "bob@example.test", profile())
			charlie := create(charlieRealm, "charlie@example.test", profile())
			survivor := create(survivorRealm, "survivor@example.test", profile())
			var err error
			if mode == "trusted" {
				err = s.DeleteAuthRecord(ctx, rootRealm, alice.ID)
			} else {
				err = s.DeleteAuthorizedAuthRecord(ctx, rootRealm, alice.ID, alice.ID)
			}
			denied := mode == "denied-descendant"
			if denied && !errors.Is(err, ErrInvalidCredentials) || !denied && err != nil {
				t.Errorf("delete mode=%s: %v", mode, err)
			}
			for _, record := range []AuthRecord{alice, bob, charlie} {
				_, err := s.GetAuthRecord(ctx, record.AuthCollectionID, record.ID)
				if denied && err != nil || !denied && !errors.Is(err, ErrNotFound) {
					t.Errorf("graph record %s: %v", record.Email, err)
				}
			}
			got, err := s.GetAuthRecord(ctx, survivorRealm, survivor.ID)
			if err != nil {
				t.Fatal(err)
			}
			if denied && got.Fields["parent"] != alice.ID.String() {
				t.Errorf("failed delete changed surviving relation: %v", got.Fields)
			}
			if !denied && got.Fields["parent"] != nil {
				t.Errorf("set-null behavior changed: %v", got.Fields)
			}
		})
	}
}
