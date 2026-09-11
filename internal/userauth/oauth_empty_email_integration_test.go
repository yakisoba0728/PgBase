//go:build integration

package userauth

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

func TestOAuthDistinctSubjectsWithoutEmailRemainIndependent(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	service.ConfigureDefaultRegistration(true)
	provider := realmOAuthProvider{}
	redirect := "https://app.example/oauth/callback"
	if err := service.ConfigureOAuth(map[string]OAuthProvider{"local": provider}, []string{redirect}, time.Minute); err != nil {
		t.Fatal(err)
	}
	service.ConfigureOAuthRegistration(true)
	for _, subject := range []string{"no-email-one", "no-email-two"} {
		provider.identity = OAuthIdentity{Subject: subject}
		service.oauthProviders["local"] = provider
		start, err := service.BeginOAuth(context.Background(), "local", redirect, "192.0.2.10")
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := url.Parse(start.AuthorizationURL)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.CompleteOAuth(context.Background(), "local", parsed.Query().Get("state"), "code"); err != nil {
			t.Fatalf("subject %s: %v", subject, err)
		}
	}
	var users, links int
	if err := service.pool.QueryRow(context.Background(), `select count(*) from pgbase_meta.users where auth_collection_id=$1 and email=''`, DefaultCollectionID).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := service.pool.QueryRow(context.Background(), `select count(*) from pgbase_meta.user_oauth_identities where auth_collection_id=$1 and provider='local' and subject in ('no-email-one','no-email-two')`, DefaultCollectionID).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if users != 2 || links != 2 {
		t.Fatalf("empty-email users=%d OAuth links=%d", users, links)
	}
}
