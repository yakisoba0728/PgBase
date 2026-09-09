//go:build integration

package userauth

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/adminauth"
)

func TestAdministratorImpersonationIsBoundToActorAndTarget(t *testing.T) {
	pool := newUserAuthPool(t)
	secret := []byte("0123456789abcdef0123456789abcdef")
	service := NewService(pool, adminauth.NewArgon2idPasswords(), secret, time.Hour)
	ctx := context.Background()
	registered, err := service.Register(ctx, "impersonated@example.test", "correct horse battery staple", "impersonation-register")
	if err != nil {
		t.Fatal(err)
	}

	adminID, adminSessionID := uuid.New(), uuid.New()
	rawAdmin := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	_, err = pool.Exec(ctx, `insert into pgbase_meta.admins(id,email,password_hash,active,created_at,updated_at) values($1,'actor@example.test','unused',true,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, adminID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `insert into pgbase_meta.admin_sessions(id,admin_id,token_hash,expires_at,last_seen_at,revoked_at,created_at) values($1,$2,$3,CURRENT_TIMESTAMP+interval '10 minutes',CURRENT_TIMESTAMP,null,CURRENT_TIMESTAMP)`, adminSessionID, adminID, adminauth.HashSessionToken(secret, rawAdmin))
	if err != nil {
		t.Fatal(err)
	}

	session, err := service.Impersonate(ctx, registered.User.ID, rawAdmin, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := service.Authenticate(ctx, session.RawToken)
	if err != nil {
		t.Fatal(err)
	}
	if !principal.Impersonated || principal.ImpersonatingAdministratorID != adminID || principal.User.ID != registered.User.ID {
		t.Fatalf("impersonated principal = %#v", principal)
	}
	var kind string
	var originAdmin, originSession uuid.UUID
	if err := pool.QueryRow(ctx, `select session_kind,impersonating_admin_id,impersonating_admin_session_id from pgbase_meta.user_sessions where token_hash=$1`, service.tokenHash(session.RawToken)).Scan(&kind, &originAdmin, &originSession); err != nil {
		t.Fatal(err)
	}
	if kind != "impersonation" || originAdmin != adminID || originSession != adminSessionID {
		t.Fatalf("stored origin = %q %s %s", kind, originAdmin, originSession)
	}

	if _, err := pool.Exec(ctx, `update pgbase_meta.admin_sessions set revoked_at=CURRENT_TIMESTAMP where id=$1`, adminSessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, session.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate after actor revoke = %v", err)
	}

	if _, err := service.Impersonate(ctx, registered.User.ID, rawAdmin, 5*time.Minute); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Impersonate with revoked actor = %v", err)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.admin_sessions set revoked_at=null where id=$1`, adminSessionID); err != nil {
		t.Fatal(err)
	}
	second, err := service.Impersonate(ctx, registered.User.ID, rawAdmin, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.admins set active=false where id=$1`, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, second.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate after actor deactivation = %v", err)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.admins set active=true where id=$1`, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.users set active=false where id=$1`, registered.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Impersonate(ctx, registered.User.ID, rawAdmin, 5*time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Impersonate inactive user = %v", err)
	}
}

func TestAdministratorImpersonationDurationBounds(t *testing.T) {
	service := &Service{}
	for _, duration := range []time.Duration{time.Second, 16 * time.Minute} {
		if _, err := service.Impersonate(context.Background(), uuid.New(), base64.RawURLEncoding.EncodeToString(make([]byte, 32)), duration); !errors.Is(err, ErrValidation) {
			t.Fatalf("duration %s = %v", duration, err)
		}
	}
}
