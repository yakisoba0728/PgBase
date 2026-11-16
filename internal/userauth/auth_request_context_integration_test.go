//go:build integration

package userauth

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/identity"
)

func TestAuthRuleTransportContextDoesNotSupplyActorOrBody(t *testing.T) {
	s := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	session, err := s.Register(t.Context(), "request-context@example.test", "correct horse battery staple", "request-context-test")
	if err != nil {
		t.Fatal(err)
	}
	ruleContext := AuthRuleContext{Principal: identity.Subject{Kind: identity.User, ID: session.User.ID}, Current: AuthRecord{ID: session.User.ID}, Request: map[string]any{"email": "new@example.test"}}
	metadata := &accessrule.RequestContext{Method: "PATCH", Headers: map[string][]string{"x_scope": {"blocked"}}, Query: map[string][]string{"scope": {"blocked"}}, Auth: map[string]any{"verified": true}}
	for _, test := range []struct {
		name, rule string
		metadata   *accessrule.RequestContext
		want       bool
	}{
		{"no_request_method_negation", "@request.method != 'GET'", nil, false},
		{"no_request_header_negation", "@request.headers.x_scope != 'blocked'", nil, false},
		{"no_request_query_negation", "@request.query.scope != 'blocked'", nil, false},
		{"no_request_nested", "active = true || @request.method != 'GET'", nil, false},
		{"no_request_rhs", "email != @request.method", nil, false},
		{"empty_metadata", "@request.method != 'GET'", &accessrule.RequestContext{}, false},
		{"method_and_service_body", "@request.method = 'PATCH' && @request.body.email = 'new@example.test'", metadata, true},
		{"blocked_header", "@request.headers.x_scope != 'blocked'", metadata, false},
		{"blocked_query", "@request.query.scope != 'blocked'", metadata, false},
		{"actor_from_database", "@request.method = 'PATCH' && @request.auth.verified = false", metadata, true},
		{"spoofed_actor_ignored", "@request.method = 'PATCH' && @request.auth.verified = true", metadata, false},
		{"embedded_body_still_supported", "@request.body.email = 'new@example.test'", nil, true},
		{"embedded_actor_still_supported", "@request.auth.id = id", nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			if test.metadata != nil {
				ctx = accessrule.WithRequestContext(ctx, test.metadata)
			}
			var allowed bool
			err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
				var err error
				allowed, err = EvaluateAuthRecordRuleInTx(ctx, tx, DefaultCollectionID, test.rule, ruleContext)
				return err
			})
			if err != nil || allowed != test.want {
				t.Fatalf("allowed=%v want=%v err=%v", allowed, test.want, err)
			}
		})
	}
}
