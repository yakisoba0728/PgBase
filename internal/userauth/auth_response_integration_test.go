//go:build integration

package userauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

func TestIssueAuthResponseSessionEnforcesAuthRuleActiveAndMFA(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	registered, err := service.Register(ctx, "extension-auth@example.test", "correct horse battery staple", "register")
	if err != nil {
		t.Fatal(err)
	}
	setAuthRuleForTest(t, service, DefaultCollectionID, "false")
	if _, err = service.IssueAuthResponseSession(ctx, DefaultCollectionID, registered.User.ID, "extension:test"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("authRule bypassed: %v", err)
	}
	setAuthRuleForTest(t, service, DefaultCollectionID, "true")
	issued, err := service.IssueAuthResponseSession(ctx, DefaultCollectionID, registered.User.ID, "extension:test")
	if err != nil || issued.RawToken == "" {
		t.Fatalf("session=%#v err=%v", issued, err)
	}
	if _, err = service.Authenticate(ctx, issued.RawToken); err != nil {
		t.Fatalf("issued token is not a real session: %v", err)
	}
	policy, err := service.authPolicy(ctx, DefaultCollectionID)
	if err != nil {
		t.Fatal(err)
	}
	policy.MFAEnabled = true
	policy.MFADurationSeconds = 120
	if _, err = service.SetAuthPolicy(ctx, DefaultCollectionID, policy); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err = service.pool.Exec(ctx, `insert into pgbase_meta.user_totp_factors(user_id,secret_ciphertext,active,created_at,updated_at) values($1,$2,true,$3,$3)`, registered.User.ID, []byte("encrypted-test-secret"), now); err != nil {
		t.Fatal(err)
	}
	if _, err = service.IssueAuthResponseSession(ctx, DefaultCollectionID, registered.User.ID, "extension:test"); err == nil {
		t.Fatal("MFA-enabled record received a session without a challenge")
	} else {
		var challenge *MFARequiredError
		if !errors.As(err, &challenge) || challenge.ChallengeToken == "" {
			t.Fatalf("MFA result=%v", err)
		}
	}
	if _, err = service.pool.Exec(ctx, `update pgbase_meta.users set active=false where id=$1`, registered.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.IssueAuthResponseSession(ctx, DefaultCollectionID, registered.User.ID, "extension:test"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("inactive record received session: %v", err)
	}
}
