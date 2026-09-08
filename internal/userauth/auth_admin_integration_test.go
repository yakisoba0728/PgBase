//go:build integration

package userauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/adminauth"
)

func TestAuthAdministrationPolicyTakesEffectAndRevokesSessions(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	realmID := uuid.New()
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: realmID, Name: "policy_" + realmID.String()[:8], RegistrationEnabled: true}); err != nil {
		t.Fatal(err)
	}
	session, err := service.RegisterInCollection(ctx, realmID, "policy@example.test", "correct horse battery staple", "policy-register")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := service.SetAuthPolicy(ctx, realmID, AuthPolicy{PasswordEnabled: true, MinimumPasswordBytes: 32, MFAEnabled: true})
	if err != nil || policy.MinimumPasswordBytes != 32 {
		t.Fatalf("set policy = %#v, %v", policy, err)
	}
	if _, err := service.RegisterInCollection(ctx, realmID, "short@example.test", "correct horse battery staple", "policy-short"); !errors.Is(err, ErrValidation) {
		t.Fatalf("short realm password = %v", err)
	}
	if _, err := service.LoginInCollection(ctx, realmID, "policy@example.test", "correct horse battery staple", "policy-old-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("existing short password after tightening = %v", err)
	}
	if _, err := service.Authenticate(ctx, session.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("session after password tightening = %v", err)
	}
	if _, err := service.SetAuthPolicy(ctx, realmID, AuthPolicy{PasswordEnabled: false, MinimumPasswordBytes: 32, MFAEnabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, session.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("session after tightened policy = %v", err)
	}
	if _, err := service.LoginInCollection(ctx, realmID, "policy@example.test", "correct horse battery staple", "policy-login"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("password login after disable = %v", err)
	}
	if _, err := service.BeginTOTPEnrollment(ctx, session.User.ID, "correct horse battery staple"); !errors.Is(err, ErrValidation) {
		t.Fatalf("TOTP enrollment after disable = %v", err)
	}
}

func TestAuthAdministrationNeverReturnsAndPreservesSecrets(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	realmID := uuid.New()
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: realmID, Name: "secret_" + realmID.String()[:8]}); err != nil {
		t.Fatal(err)
	}
	provider := OAuthProviderConfig{Name: "oidc", Enabled: true, AuthorizationURL: "https://id.example/authorize", TokenURL: "https://id.example/token", UserInfoURL: "https://id.example/userinfo", ClientID: "client", ClientSecret: "first-secret", Scopes: []string{"openid", "email"}, RedirectURIs: []string{"https://app.example/callback"}, SubjectField: "sub", EmailField: "email", EmailVerifiedField: "email_verified"}
	if _, err := service.PutOAuthProvider(ctx, realmID, provider); err != nil {
		t.Fatal(err)
	}
	var before []byte
	if err := service.pool.QueryRow(ctx, `select client_secret_ciphertext from pgbase_meta.auth_oauth_providers where auth_collection_id=$1 and name='oidc'`, realmID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	provider.ClientSecret = ""
	provider.ClientID = "updated"
	if _, err := service.PutOAuthProvider(ctx, realmID, provider); err != nil {
		t.Fatal(err)
	}
	var after []byte
	if err := service.pool.QueryRow(ctx, `select client_secret_ciphertext from pgbase_meta.auth_oauth_providers where auth_collection_id=$1 and name='oidc'`, realmID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("blank provider secret edit replaced stored ciphertext")
	}
	settings, err := service.GetAuthAdministration(ctx, realmID)
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.Providers) != 1 || !settings.Providers[0].SecretConfigured || settings.Providers[0].ClientSecret != "" {
		t.Fatalf("redacted provider = %#v", settings.Providers)
	}
}
