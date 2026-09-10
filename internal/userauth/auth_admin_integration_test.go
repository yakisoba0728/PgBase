//go:build integration

package userauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
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
	policy, err := service.SetAuthPolicy(ctx, realmID, AuthPolicy{PasswordEnabled: true, MinimumPasswordBytes: 32, MFAEnabled: true, OTPEnabled: true, OTPDurationSeconds: 120, OTPMaxAttempts: 4})
	if err != nil || policy.MinimumPasswordBytes != 32 || !policy.OTPEnabled || policy.OTPDurationSeconds != 120 || policy.OTPMaxAttempts != 4 {
		t.Fatalf("set policy = %#v, %v", policy, err)
	}
	if _, err := service.PutMailTemplate(ctx, realmID, MailTemplate{Purpose: "otp", Subject: "Realm code", Body: "Code {{otp}} / {{otp_id}}", HTMLBody: "<p>Code <strong>{{otp}}</strong></p>"}); err != nil {
		t.Fatal(err)
	}
	administration, err := service.GetAuthAdministration(ctx, realmID)
	if err != nil || !administration.Policy.OTPEnabled {
		t.Fatalf("OTP administration=%#v %v", administration.Policy, err)
	}
	foundOTP := false
	for _, template := range administration.Templates {
		if template.Purpose == "otp" {
			foundOTP = true
		}
	}
	if !foundOTP {
		t.Fatal("OTP template missing from administration")
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

func TestAuthPolicyEnforcesMaximumPatternAndDeclaredProfileIdentity(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	realm := uuid.New()
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: realm, Name: "identity_" + realm.String()[:8], RegistrationEnabled: true, ProfileSchema: map[string]string{"username": "text"}}); err != nil {
		t.Fatal(err)
	}
	policy := AuthPolicy{PasswordEnabled: true, MinimumPasswordBytes: 12, MaximumPasswordBytes: 30, PasswordPattern: "[A-Z]", IdentityFields: []string{"username"}, MFAEnabled: true, MFADurationSeconds: 120, OTPEnabled: false, OTPDurationSeconds: 300, OTPMaxAttempts: 5}
	if _, err := service.SetAuthPolicy(ctx, realm, policy); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterInCollection(ctx, realm, "identity@example.test", "correct horse battery", "identity-register"); !errors.Is(err, ErrValidation) {
		t.Fatalf("pattern registration = %v", err)
	}
	session, err := service.RegisterInCollection(ctx, realm, "identity@example.test", "Correct horse battery", "identity-register")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetProfile(ctx, session.User.ID, map[string]any{"username": "alexa"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LoginInCollection(ctx, realm, "alexa", "Correct horse battery", "identity-login"); err != nil {
		t.Fatalf("profile identity login = %v", err)
	}
	if _, err := service.LoginInCollection(ctx, realm, "identity@example.test", "Correct horse battery", "identity-email"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled email identity = %v", err)
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
