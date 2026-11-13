//go:build integration

package userauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestDesignerFieldSyncAndPartialUpdateKeepRealmFields(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	id := uuid.New()
	name := "sync_" + id.String()[:8]
	realmOnly := schema.FieldInput{Name: "realm_only", Type: schema.TypeText}
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: id, Name: name, RegistrationEnabled: true, Fields: []schema.FieldInput{realmOnly}}); err != nil {
		t.Fatal(err)
	}
	session, err := service.RegisterInCollection(ctx, id, "sync@example.test", "correct horse battery staple", "198.51.100.41")
	if err != nil {
		t.Fatal(err)
	}
	sync := func(removed []string, upserts ...schema.FieldInput) error {
		return service.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			return service.SyncAuthCollectionFields(ctx, tx, name, removed, upserts)
		})
	}
	code := func(err error) string {
		var invalid *schema.ValidationError
		if !errors.As(err, &invalid) || len(invalid.Details) != 1 {
			return ""
		}
		return invalid.Details[0].Code
	}
	names := func() []string {
		value, err := service.GetAuthCollection(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, field := range value.Fields {
			out = append(out, field.Name)
		}
		return out
	}
	if err := sync(nil, schema.FieldInput{Name: "nickname", Type: schema.TypeText}); err != nil {
		t.Fatal(err)
	}
	if got := names(); len(got) != 2 || got[0] != "realm_only" || got[1] != "nickname" {
		t.Fatalf("synced fields = %v", got)
	}
	if err := sync(nil, schema.FieldInput{Name: "badge", Type: schema.TypeText, Required: true}); code(err) != "auth_profiles_incompatible" {
		t.Fatalf("required field with existing users = %v", err)
	}
	if err := sync(nil, schema.FieldInput{Name: "handle", Type: schema.TypeText, Unique: true}); code(err) != "invalid_auth_fields" {
		t.Fatalf("unique profile field = %v", err)
	}
	// A partial update (registration toggle) keeps the stored fields and rules.
	updated, err := service.UpdateAuthCollection(ctx, id, UpdateAuthCollectionInput{RegistrationEnabled: false})
	if err != nil || updated.RegistrationEnabled || len(updated.Fields) != 2 {
		t.Fatalf("partial update = %+v, %v", updated, err)
	}
	if _, err := service.SetProfile(ctx, session.User.ID, map[string]any{"nickname": "Nick", "realm_only": "kept"}); err != nil {
		t.Fatal(err)
	}
	// Dropping a field that users have values for drops those values with it.
	if err := sync([]string{"nickname"}); err != nil {
		t.Fatal(err)
	}
	if got := names(); len(got) != 1 || got[0] != "realm_only" {
		t.Fatalf("fields after removal = %v", got)
	}
	if profile, err := service.GetProfile(ctx, session.User.ID); err != nil || profile["nickname"] != nil || profile["realm_only"] != "kept" {
		t.Fatalf("profile after removal = %v, %v", profile, err)
	}
	if err := service.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return service.SyncAuthCollectionFields(ctx, tx, "no_such_realm", nil, []schema.FieldInput{{Name: "x", Type: schema.TypeText}})
	}); err != nil {
		t.Fatalf("missing realm = %v", err)
	}
}
