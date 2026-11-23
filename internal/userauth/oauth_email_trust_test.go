package userauth

import (
	"encoding/json"
	"testing"
)

func TestCustomProviderHonorsOnlyExplicitEmailVerification(t *testing.T) {
	provider, err := NewHTTPOAuthProvider(HTTPOAuthConfig{AuthorizationURL: "https://id.example/authorize", TokenURL: "https://id.example/token", UserInfoURL: "https://id.example/userinfo", ClientID: "client"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name         string
		claims       string
		wantEmail    string
		wantVerified bool
		wantErr      bool
	}{
		{name: "boolean true", claims: `{"sub":"s","email":"a@example.test","email_verified":true}`, wantEmail: "a@example.test", wantVerified: true},
		{name: "boolean false keeps the email", claims: `{"sub":"s","email":"a@example.test","email_verified":false}`, wantEmail: "a@example.test"},
		{name: "missing claim keeps the email", claims: `{"sub":"s","email":"a@example.test"}`, wantEmail: "a@example.test"},
		{name: "null claim", claims: `{"sub":"s","email":"a@example.test","email_verified":null}`, wantEmail: "a@example.test"},
		{name: "string true", claims: `{"sub":"s","email":"a@example.test","email_verified":"true"}`, wantEmail: "a@example.test", wantVerified: true},
		{name: "string false", claims: `{"sub":"s","email":"a@example.test","email_verified":"False"}`, wantEmail: "a@example.test"},
		{name: "claim without an email", claims: `{"sub":"s","email":null,"email_verified":true}`},
		{name: "number", claims: `{"sub":"s","email":"a@example.test","email_verified":1}`, wantErr: true},
		{name: "other string", claims: `{"sub":"s","email":"a@example.test","email_verified":"yes"}`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(test.claims), &fields); err != nil {
				t.Fatal(err)
			}
			identity, err := provider.identityFromFields(fields)
			if test.wantErr {
				if err == nil {
					t.Fatalf("identity = %#v, want an error", identity)
				}
				return
			}
			if err != nil || identity.Email != test.wantEmail || identity.EmailVerified != test.wantVerified {
				t.Fatalf("identity email=%q verified=%t err=%v, want %q %t", identity.Email, identity.EmailVerified, err, test.wantEmail, test.wantVerified)
			}
		})
	}
}
