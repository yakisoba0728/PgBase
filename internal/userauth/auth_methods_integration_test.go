//go:build integration

package userauth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

func TestPublicAuthMethodsExposeOnlyEnabledProviderNames(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	realmID := uuid.New()
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: realmID, Name: "methods_" + realmID.String()[:8]}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetAuthPolicy(ctx, realmID, AuthPolicy{PasswordEnabled: true, MinimumPasswordBytes: 12, MFAEnabled: true, OTPEnabled: true, OTPDurationSeconds: 300, OTPMaxAttempts: 5}); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []OAuthProviderConfig{
		{Name: "enabled", Enabled: true, AuthorizationURL: "https://id.example/authorize", TokenURL: "https://id.example/token", UserInfoURL: "https://id.example/userinfo", ClientID: "client", ClientSecret: "never-public", Scopes: []string{"openid"}, RedirectURIs: []string{"https://app.example/callback"}, SubjectField: "sub", EmailField: "email", EmailVerifiedField: "email_verified"},
		{Name: "disabled", Enabled: false, AuthorizationURL: "https://id.example/authorize", TokenURL: "https://id.example/token", UserInfoURL: "https://id.example/userinfo", ClientID: "client", ClientSecret: "also-private", Scopes: []string{"openid"}, RedirectURIs: []string{"https://app.example/callback"}, SubjectField: "sub", EmailField: "email", EmailVerifiedField: "email_verified"},
	} {
		if _, err := service.PutOAuthProvider(ctx, realmID, provider); err != nil {
			t.Fatal(err)
		}
	}
	methods, err := service.PublicAuthMethods(ctx, realmID)
	if err != nil {
		t.Fatal(err)
	}
	if !methods.PasswordEnabled || !methods.OTPEnabled || !methods.MFAEnabled || len(methods.OAuthProviders) != 1 || methods.OAuthProviders[0] != "enabled" {
		t.Fatalf("methods=%#v", methods)
	}
	if _, err := service.PublicAuthMethods(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown methods=%v", err)
	}
}

func TestRefreshSessionRotatesInPlaceAndRejectsInvalidStates(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	realmID := uuid.New()
	otherRealmID := uuid.New()
	for _, collection := range []AuthCollection{{ID: realmID, Name: "refresh_" + realmID.String()[:8]}, {ID: otherRealmID, Name: "other_" + otherRealmID.String()[:8]}} {
		if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: collection.ID, Name: collection.Name, RegistrationEnabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := service.RegisterInCollection(ctx, realmID, "refresh@example.test", "correct horse battery staple", "refresh-register")
	if err != nil {
		t.Fatal(err)
	}
	var sessionID uuid.UUID
	if err := service.pool.QueryRow(ctx, `select id from pgbase_meta.user_sessions where token_hash=$1`, service.tokenHash(first.RawToken)).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RefreshSessionForCollection(ctx, otherRealmID, first.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("cross-realm refresh=%v", err)
	}
	if _, err := service.Authenticate(ctx, first.RawToken); err != nil {
		t.Fatalf("wrong realm refresh altered valid token: %v", err)
	}
	refreshed, err := service.RefreshSessionForCollection(ctx, realmID, first.RawToken)
	if err != nil || refreshed.RawToken == first.RawToken || !refreshed.ExpiresAt.After(first.ExpiresAt) {
		t.Fatalf("refresh=%#v err=%v first=%#v", refreshed, err, first)
	}
	if _, err := service.Authenticate(ctx, first.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old token after refresh=%v", err)
	}
	if _, err := service.AuthenticateForCollection(ctx, realmID, refreshed.RawToken); err != nil {
		t.Fatalf("replacement authentication=%v", err)
	}
	var refreshedID uuid.UUID
	if err := service.pool.QueryRow(ctx, `select id from pgbase_meta.user_sessions where token_hash=$1`, service.tokenHash(refreshed.RawToken)).Scan(&refreshedID); err != nil || refreshedID != sessionID {
		t.Fatalf("session continuity id=%s err=%v want=%s", refreshedID, err, sessionID)
	}

	concurrent, err := service.RegisterInCollection(ctx, realmID, "parallel@example.test", "correct horse battery staple", "parallel-register")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.RefreshSessionForCollection(ctx, realmID, concurrent.RawToken)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("concurrent refresh=%v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent refresh successes=%d", successes)
	}

	expired, err := service.RegisterInCollection(ctx, realmID, "expired@example.test", "correct horse battery staple", "expired-register")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `update pgbase_meta.user_sessions set expires_at=CURRENT_TIMESTAMP-interval '1 second' where token_hash=$1`, service.tokenHash(expired.RawToken)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RefreshSessionForCollection(ctx, realmID, expired.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expired refresh=%v", err)
	}

	revoked, err := service.RegisterInCollection(ctx, realmID, "revoked@example.test", "correct horse battery staple", "revoked-register")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Logout(ctx, revoked.RawToken); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RefreshSessionForCollection(ctx, realmID, revoked.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("revoked refresh=%v", err)
	}

	blocked, err := service.RegisterInCollection(ctx, realmID, "blocked@example.test", "correct horse battery staple", "blocked-register")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetUserActive(ctx, blocked.User.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RefreshSessionForCollection(ctx, realmID, blocked.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("inactive refresh=%v", err)
	}
}
