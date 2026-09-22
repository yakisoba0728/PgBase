//go:build integration

package userauth

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestPublicRegistrationEnforcesCollectionCreatePolicy(t *testing.T) {
	for _, method := range []string{"oauth", "password"} {
		for _, policy := range []string{"denied-create-rule", "required-profile-field"} {
			t.Run(method+"/"+policy, func(t *testing.T) {
				service, _ := newEmailActionService(t)
				ctx := context.Background()
				collectionID := uuid.New()
				rules := AuthCollectionRules{Create: "false"}
				var fields []schema.FieldInput
				wantErr := ErrInvalidCredentials
				if policy == "required-profile-field" {
					rules.Create = "true"
					fields = []schema.FieldInput{{Name: "display_name", Type: schema.TypeText, Required: true}}
					wantErr = ErrValidation
				}
				_, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: collectionID, Name: "signup_" + collectionID.String()[:8], RegistrationEnabled: true, Fields: fields, Rules: rules})
				if err != nil {
					t.Fatal(err)
				}
				register := func() (Session, error) {
					return service.RegisterInCollection(ctx, collectionID, "signup@example.test", "correct horse battery staple", "signup-policy-password")
				}
				if method == "oauth" {
					provider := realmOAuthProvider{identity: OAuthIdentity{Subject: "signup-subject", Email: "signup@example.test", EmailVerified: true}}
					redirect := "https://app.example/oauth/callback"
					if err := service.ConfigureOAuth(map[string]OAuthProvider{"local": provider}, []string{redirect}, time.Minute); err != nil {
						t.Fatal(err)
					}
					service.ConfigureOAuthRegistration(true)
					authorization, err := service.BeginOAuthInCollection(ctx, collectionID, "local", redirect, "signup-policy-oauth")
					if err != nil {
						t.Fatal(err)
					}
					parsed, err := url.Parse(authorization.AuthorizationURL)
					if err != nil {
						t.Fatal(err)
					}
					state := parsed.Query().Get("state")
					register = func() (Session, error) { return service.CompleteOAuth(ctx, "local", state, "code") }
				}
				if _, err := register(); !errors.Is(err, wantErr) {
					t.Fatalf("collection must reject registration without satisfying %s: %v", policy, err)
				}
				records, err := service.ListAuthRecords(ctx, collectionID, 10)
				if err != nil || len(records) != 0 {
					t.Fatalf("denied registration persisted a user: %#v %v", records, err)
				}
				rules.Create = "@request.body.email = 'signup@example.test'"
				for i := range fields {
					fields[i].Required = false
				}
				if _, err := service.UpdateAuthCollection(ctx, collectionID, UpdateAuthCollectionInput{RegistrationEnabled: true, Fields: fields, Rules: rules}); err != nil {
					t.Fatal(err)
				}
				session, err := register()
				if err != nil || session.User.Email != "signup@example.test" {
					t.Fatalf("registration retry after allowing policy must succeed: %#v %v", session.User, err)
				}
			})
		}
	}
}

type waitingRegistrationProvider struct {
	realmOAuthProvider
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *waitingRegistrationProvider) Exchange(ctx context.Context, code, verifier, redirect string) (OAuthIdentity, error) {
	p.once.Do(func() { close(p.entered) })
	select {
	case <-ctx.Done():
		return OAuthIdentity{}, ctx.Err()
	case <-p.release:
		return p.realmOAuthProvider.Exchange(ctx, code, verifier, redirect)
	}
}

func TestOAuthRegistrationUsesPolicyCommittedDuringProviderExchange(t *testing.T) {
	service, _ := newEmailActionService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	collectionID := uuid.New()
	rules := AuthCollectionRules{Create: "true"}
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: collectionID, Name: "signup_race_" + collectionID.String()[:8], RegistrationEnabled: true, Rules: rules}); err != nil {
		t.Fatal(err)
	}
	provider := &waitingRegistrationProvider{realmOAuthProvider: realmOAuthProvider{identity: OAuthIdentity{Subject: "signup-race-subject", Email: "race@example.test", EmailVerified: true}}, entered: make(chan struct{}), release: make(chan struct{})}
	redirect := "https://app.example/oauth/callback"
	if err := service.ConfigureOAuth(map[string]OAuthProvider{"local": provider}, []string{redirect}, time.Minute); err != nil {
		t.Fatal(err)
	}
	service.ConfigureOAuthRegistration(true)
	authorization, err := service.BeginOAuthInCollection(ctx, collectionID, "local", redirect, "signup-race")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorization.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	state := parsed.Query().Get("state")
	completed := make(chan error, 1)
	go func() {
		_, err := service.CompleteOAuth(ctx, "local", state, "code")
		completed <- err
	}()
	select {
	case <-provider.entered:
	case <-ctx.Done():
		t.Fatal("OAuth provider exchange never started")
	}
	rules.Create = "false"
	_, err = service.UpdateAuthCollection(ctx, collectionID, UpdateAuthCollectionInput{RegistrationEnabled: true, Rules: rules})
	close(provider.release)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-completed; !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("OAuth signup ignored committed create policy: %v", err)
	}
	records, err := service.ListAuthRecords(ctx, collectionID, 10)
	if err != nil || len(records) != 0 {
		t.Fatalf("policy race left a registered account: %#v %v", records, err)
	}
}

func TestPublicRegistrationPersistsGeneratedRequiredProfileFields(t *testing.T) {
	service, _ := newEmailActionService(t)
	ctx := context.Background()
	collectionID := uuid.New()
	fields := []schema.FieldInput{{Name: "joined_at", Type: schema.TypeAutoDate, Required: true, Options: &schema.FieldOptions{OnCreate: true}}}
	rules := AuthCollectionRules{Create: "email = 'generated@example.test' && joined_at != null"}
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: collectionID, Name: "signup_generated_" + collectionID.String()[:8], RegistrationEnabled: true, Fields: fields, Rules: rules}); err != nil {
		t.Fatal(err)
	}
	session, err := service.RegisterInCollection(ctx, collectionID, "generated@example.test", "correct horse battery staple", "generated-profile-register")
	if err != nil {
		t.Fatalf("generated required fields should satisfy signup policy: %v", err)
	}
	record, err := service.GetAuthRecord(ctx, collectionID, session.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := record.Fields["joined_at"].(string)
	if _, err := time.Parse(time.RFC3339Nano, value); !ok || err != nil {
		t.Fatalf("generated required profile field was not persisted: %#v", record.Fields)
	}
}
