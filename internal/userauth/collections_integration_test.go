//go:build integration

package userauth

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

type realmOAuthProvider struct{ identity OAuthIdentity }

func (p realmOAuthProvider) AuthorizationURL(state, challenge, redirectURI string) (string, error) {
	values := url.Values{"state": {state}, "code_challenge": {challenge}, "redirect_uri": {redirectURI}}
	return "https://provider.example/authorize?" + values.Encode(), nil
}
func (p realmOAuthProvider) Exchange(context.Context, string, string, string) (OAuthIdentity, error) {
	return p.identity, nil
}

func TestAuthCollectionsIsolateIdentitySessionsAndProfiles(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	first := uuid.New()
	second := uuid.New()
	for _, id := range []uuid.UUID{first, second} {
		if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: id, Name: "realm_" + id.String()[:8], RegistrationEnabled: true, ProfileSchema: map[string]string{"display_name": "text", "age": "integer"}}); err != nil {
			t.Fatal(err)
		}
	}
	a, err := service.RegisterInCollection(ctx, first, "same@example.com", "correct horse battery staple", "realm-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := service.RegisterInCollection(ctx, second, "same@example.com", "different horse battery staple", "realm-b")
	if err != nil {
		t.Fatal(err)
	}
	if a.User.ID == b.User.ID || a.User.AuthCollectionID != first || b.User.AuthCollectionID != second {
		t.Fatalf("users not isolated: %#v %#v", a.User, b.User)
	}
	if _, err := service.AuthenticateForCollection(ctx, second, a.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("cross realm token = %v", err)
	}
	profile, err := service.SetProfile(ctx, a.User.ID, map[string]any{"display_name": "Ada", "age": float64(37)})
	if err != nil || profile["display_name"] != "Ada" {
		t.Fatalf("profile = %#v, %v", profile, err)
	}
	if _, err := service.SetProfile(ctx, a.User.ID, map[string]any{"unknown": true}); !errors.Is(err, ErrValidation) {
		t.Fatalf("unknown profile field = %v", err)
	}
	if err := service.DeleteAuthCollection(ctx, first); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete populated realm = %v", err)
	}
}

func TestAuthCollectionRegistrationPolicies(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	id := uuid.New()
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: id, Name: "closed_" + id.String()[:8]}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterInCollection(ctx, id, "closed@example.com", "correct horse battery staple", "closed"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("closed registration = %v", err)
	}
	service.ConfigureDefaultRegistration(false)
	if _, err := service.Register(ctx, "default-closed@example.com", "correct horse battery staple", "default-closed"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("default runtime registration = %v", err)
	}
}

func TestAuthCollectionRulesSupportEmailVisibilityOnCreateAndUpdate(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	id := uuid.New()
	rules := AuthCollectionRules{View: "emailVisibility = true"}
	created, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: id, Name: "visibility_" + id.String()[:8], Rules: rules})
	if err != nil {
		t.Fatalf("create collection with emailVisibility rule: %v", err)
	}
	if created.Rules.View != rules.View {
		t.Fatalf("created view rule = %q, want %q", created.Rules.View, rules.View)
	}
	updatedRules := AuthCollectionRules{View: "emailVisibility = false"}
	updated, err := service.UpdateAuthCollection(ctx, id, UpdateAuthCollectionInput{Rules: updatedRules})
	if err != nil {
		t.Fatalf("update collection with emailVisibility rule: %v", err)
	}
	if updated.Rules.View != updatedRules.View {
		t.Fatalf("updated view rule = %q, want %q", updated.Rules.View, updatedRules.View)
	}
}

func TestOAuthCallbackPersistsNonDefaultRealm(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	realmID := uuid.New()
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: realmID, Name: "oauth_" + realmID.String()[:8], RegistrationEnabled: true}); err != nil {
		t.Fatal(err)
	}
	provider := realmOAuthProvider{identity: OAuthIdentity{Subject: "realm-subject", Email: "realm-oauth@example.test", EmailVerified: true}}
	redirect := "https://app.example/oauth/callback"
	if err := service.ConfigureOAuth(map[string]OAuthProvider{"local": provider}, []string{redirect}, time.Minute); err != nil {
		t.Fatal(err)
	}
	service.ConfigureOAuthRegistration(true)
	authorization, err := service.BeginOAuthInCollection(ctx, realmID, "local", redirect, "oauth-realm-network")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorization.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.CompleteOAuth(ctx, "local", parsed.Query().Get("state"), "local-code")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteOAuth(ctx, "local", parsed.Query().Get("state"), "local-code"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("replayed OAuth callback = %v", err)
	}
	if session.User.AuthCollectionID != realmID {
		t.Fatalf("OAuth user realm = %s, want %s", session.User.AuthCollectionID, realmID)
	}
	var defaultCount, realmCount int
	if err := service.pool.QueryRow(ctx, `select count(*) filter(where auth_collection_id=$1),count(*) filter(where auth_collection_id=$2) from pgbase_meta.users where email=$3`, DefaultCollectionID, realmID, provider.identity.Email).Scan(&defaultCount, &realmCount); err != nil {
		t.Fatal(err)
	}
	if defaultCount != 0 || realmCount != 1 {
		t.Fatalf("OAuth identity leaked realms: default=%d realm=%d", defaultCount, realmCount)
	}
	if _, err := service.UpdateAuthCollection(ctx, realmID, UpdateAuthCollectionInput{RegistrationEnabled: false}); err != nil {
		t.Fatal(err)
	}
	provider.identity = OAuthIdentity{Subject: "closed-subject", Email: "closed-oauth@example.test", EmailVerified: true}
	service.oauthProviders["local"] = provider
	closed, err := service.BeginOAuthInCollection(ctx, realmID, "local", redirect, "oauth-realm-network-2")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ = url.Parse(closed.AuthorizationURL)
	if _, err := service.CompleteOAuth(ctx, "local", parsed.Query().Get("state"), "local-code"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("closed realm OAuth registration = %v", err)
	}
	if err := service.pool.QueryRow(ctx, `select count(*) from pgbase_meta.users where email=$1`, provider.identity.Email).Scan(&realmCount); err != nil || realmCount != 0 {
		t.Fatalf("closed realm OAuth user count = %d, %v", realmCount, err)
	}
}
