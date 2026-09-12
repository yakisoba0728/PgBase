//go:build integration

package userauth

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

func TestSetAuthPolicyRollsBackPolicyWhenTransitionCleanupFails(t *testing.T) {
	pool := newUserAuthPool(t)
	service := NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	realmID := uuid.New()
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: realmID, Name: "rollback_" + realmID.String()[:8], RegistrationEnabled: true}); err != nil {
		t.Fatal(err)
	}
	session, err := service.RegisterInCollection(ctx, realmID, "rollback@example.test", "correct horse battery staple", "policy-rollback-register")
	if err != nil {
		t.Fatal(err)
	}
	administration, err := service.GetAuthAdministration(ctx, realmID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `create function pgbase_meta.reject_auth_policy_transition() returns trigger language plpgsql as $$ begin raise exception 'forced transition failure'; end $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `create trigger reject_auth_policy_transition before update of revoked_at on pgbase_meta.user_sessions for each row execute function pgbase_meta.reject_auth_policy_transition()`); err != nil {
		t.Fatal(err)
	}

	next := administration.Policy
	next.PasswordEnabled = false
	if _, err := service.SetAuthPolicy(ctx, realmID, next); err == nil {
		t.Fatal("policy update unexpectedly succeeded despite transition cleanup failure")
	}
	var passwordEnabled, revoked bool
	if err := pool.QueryRow(ctx, `select p.password_enabled,s.revoked_at is not null from pgbase_meta.auth_collection_policies p join pgbase_meta.users u on u.auth_collection_id=p.auth_collection_id join pgbase_meta.user_sessions s on s.user_id=u.id where p.auth_collection_id=$1 and u.id=$2`, realmID, session.User.ID).Scan(&passwordEnabled, &revoked); err != nil {
		t.Fatal(err)
	}
	if !passwordEnabled || revoked {
		t.Fatalf("failed transition persisted policy/session state: password_enabled=%t revoked=%t", passwordEnabled, revoked)
	}
	if _, err := service.Authenticate(ctx, session.RawToken); err != nil {
		t.Fatalf("session from rolled-back transition is unusable: %v", err)
	}
}
