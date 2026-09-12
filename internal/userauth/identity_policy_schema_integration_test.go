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

func TestAuthPolicyIdentityFieldsRequireVisibleDeclaredTextFields(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	realm := uuid.New()
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: realm, Name: "identity_" + realm.String()[:8], Fields: []schema.FieldInput{{Name: "handle", Type: schema.TypeText}, {Name: "bio", Type: schema.TypeEditor}, {Name: "hidden_handle", Type: schema.TypeText, Hidden: true}}}); err != nil {
		t.Fatal(err)
	}
	policy, err := service.authPolicy(ctx, realm)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"missing", "bio", "hidden_handle"} {
		policy.IdentityFields = []string{field}
		if _, err := service.SetAuthPolicy(ctx, realm, policy); !errors.Is(err, ErrValidation) {
			t.Fatalf("identity field %q accepted: %v", field, err)
		}
	}
	policy.IdentityFields = []string{"handle"}
	if _, err := service.SetAuthPolicy(ctx, realm, policy); err != nil {
		t.Fatalf("declared text identity rejected: %v", err)
	}
}

func TestAuthCollectionUpdatePreservesPublishedIdentityFieldContract(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	realm := uuid.New()
	rules := AuthCollectionRules{}
	visibleText := schema.FieldInput{Name: "handle", Type: schema.TypeText}
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: realm, Name: "identity_update_" + realm.String()[:8], Fields: []schema.FieldInput{visibleText}, Rules: rules}); err != nil {
		t.Fatal(err)
	}
	policy, err := service.authPolicy(ctx, realm)
	if err != nil {
		t.Fatal(err)
	}
	policy.IdentityFields = []string{"handle"}
	if _, err := service.SetAuthPolicy(ctx, realm, policy); err != nil {
		t.Fatal(err)
	}
	for _, fields := range [][]schema.FieldInput{
		nil,
		{{Name: "handle", Type: schema.TypeEditor}},
		{{Name: "handle", Type: schema.TypeText, Hidden: true}},
	} {
		if _, err := service.UpdateAuthCollection(ctx, realm, UpdateAuthCollectionInput{Fields: fields, Rules: rules}); !errors.Is(err, ErrValidation) {
			t.Fatalf("invalid identity schema transition accepted for %#v: %v", fields, err)
		}
	}
	if _, err := service.UpdateAuthCollection(ctx, realm, UpdateAuthCollectionInput{Fields: []schema.FieldInput{visibleText}, Rules: rules}); err != nil {
		t.Fatalf("valid identity schema update rejected: %v", err)
	}
}
