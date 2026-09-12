//go:build integration

package userauth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestPasswordDisabledUserCannotLoginWithRetainedHash(t *testing.T) {
	ctx := context.Background()
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	registered, err := service.Register(ctx, "disabled-password@example.test", "correct horse battery staple", "registration-client")
	if err != nil {
		t.Fatal(err)
	}
	var hash string
	if err := service.pool.QueryRow(ctx, `select password_hash from pgbase_meta.users where id=$1`, registered.User.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `update pgbase_meta.users set password_enabled=false where id=$1`, registered.User.ID); err != nil {
		t.Fatal(err)
	}
	var sessionsBefore int
	if err := service.pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_sessions where user_id=$1`, registered.User.ID).Scan(&sessionsBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Login(ctx, registered.User.Email, "correct horse battery staple", "login-client"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("password-disabled login error=%v, want ErrInvalidCredentials", err)
	}
	now, err := service.databaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.issueCheckedSession(ctx, registered.User, hash, now); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("locked issuance after password disable error=%v, want ErrInvalidCredentials", err)
	}
	var sessionsAfter int
	if err := service.pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_sessions where user_id=$1`, registered.User.ID).Scan(&sessionsAfter); err != nil {
		t.Fatal(err)
	}
	if sessionsAfter != sessionsBefore {
		t.Fatalf("password-disabled login created sessions: before=%d after=%d", sessionsBefore, sessionsAfter)
	}
	if _, err := service.pool.Exec(ctx, `update pgbase_meta.users set password_enabled=true where id=$1`, registered.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Login(ctx, registered.User.Email, "correct horse battery staple", "enabled-login-client"); err != nil {
		t.Fatalf("re-enabled password login: %v", err)
	}
}

func TestAuthorizedAuthRecordListLimitsAfterRuleFiltering(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	realm := uuid.New()
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: realm, Name: "authorized_list_" + realm.String()[:8], Rules: AuthCollectionRules{List: "verified = true"}}); err != nil {
		t.Fatal(err)
	}
	var allowed uuid.UUID
	for i := 1; i <= 51; i++ {
		id := uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", i))
		verified := i == 51
		if verified {
			allowed = id
		}
		if _, err := service.pool.Exec(ctx, `insert into pgbase_meta.users(id,auth_collection_id,email,password_hash,email_visibility,verified,active,created_at,updated_at) values($1,$2,$3,'x',true,$4,true,current_timestamp,current_timestamp)`, id, realm, fmt.Sprintf("user-%02d@example.test", i), verified); err != nil {
			t.Fatal(err)
		}
	}
	visible, err := service.ListAuthorizedAuthRecords(ctx, realm, uuid.Nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 1 || visible[0].ID != allowed {
		t.Fatalf("visible=%v, want only allowed record %s", visible, allowed)
	}
}

func TestSetProfileUsesExecutableUpdateRule(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	realm := uuid.New()
	rules := AuthCollectionRules{Create: "true", Update: "@request.auth.id = id && @request.body.team:changed = true", Auth: "true"}
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: realm, Name: "profile_rule_" + realm.String()[:8], Fields: []schema.FieldInput{{Name: "team", Type: schema.TypeText}}, Rules: rules}); err != nil {
		t.Fatal(err)
	}
	record, err := service.CreateAuthRecord(ctx, realm, CreateAuthRecordInput{Email: "profile@example.test", Password: "correct horse battery staple", Active: true, Fields: map[string]any{"team": "alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := service.SetProfile(ctx, record.ID, map[string]any{"team": "beta"})
	if err != nil || profile["team"] != "beta" {
		t.Fatalf("matching profile update=%v err=%v", profile, err)
	}
	if _, err := service.SetProfile(ctx, record.ID, map[string]any{"team": "beta"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("unchanged-field profile update error=%v, want ErrValidation", err)
	}
}
