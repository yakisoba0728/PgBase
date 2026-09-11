//go:build integration

package userauth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

func TestNullableAuthRuleRoundTripsAndDeniesEverySessionPath(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	id := uuid.New()
	var rules AuthCollectionRules
	if err := json.Unmarshal([]byte(`{"create":"","auth":null,"manage":null,"list":null,"view":"","update":"","delete":""}`), &rules); err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: id, Name: "nullable_" + id.String()[:8], RegistrationEnabled: true, Rules: rules})
	if err != nil || !created.Rules.IsNull("auth") || !created.Rules.IsNull("manage") || !created.Rules.IsNull("list") {
		t.Fatalf("created=%#v err=%v", created.Rules, err)
	}
	wire, _ := json.Marshal(created.Rules)
	if string(wire) != `{"auth":null,"create":"","delete":"","list":null,"manage":null,"update":"","view":""}` {
		t.Fatalf("rules wire=%s", wire)
	}
	if _, err = service.RegisterInCollection(ctx, id, "locked@example.test", "correct horse battery staple", "198.51.100.60"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("null auth rule issued registration session: %v", err)
	}
	var users, sessions int
	if err = service.pool.QueryRow(ctx, `select count(*),(select count(*) from pgbase_meta.user_sessions s join pgbase_meta.users u on u.id=s.user_id where u.auth_collection_id=$1) from pgbase_meta.users where auth_collection_id=$1`, id).Scan(&users, &sessions); err != nil || users != 0 || sessions != 0 {
		t.Fatalf("denied registration left users/sessions=%d/%d err=%v", users, sessions, err)
	}
	rules.nulls = map[string]bool{"manage": true, "list": true}
	updated, err := service.UpdateAuthCollection(ctx, id, UpdateAuthCollectionInput{RegistrationEnabled: true, Rules: rules})
	if err != nil || updated.Rules.IsNull("auth") || updated.Rules.Auth != "" {
		t.Fatalf("empty auth update=%#v err=%v", updated.Rules, err)
	}
	if _, err = service.RegisterInCollection(ctx, id, "open@example.test", "correct horse battery staple", "198.51.100.60"); err != nil {
		t.Fatalf("empty auth rule rejected: %v", err)
	}
}
