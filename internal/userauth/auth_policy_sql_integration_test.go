//go:build integration

package userauth

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestAuthRulesUseTheProductionSQLBinding(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	realm := uuid.New()
	_, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: realm, Name: "sql_policy", RegistrationEnabled: true,
		Fields: []schema.FieldInput{{Name: "team", Type: schema.TypeText}, {Name: "score", Type: schema.TypeInteger}}, Rules: AuthCollectionRules{Create: "true"}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.RegisterInCollection(ctx, realm, "sql-policy@example.test", "correct horse battery staple", "sql-policy")
	if err != nil {
		t.Fatal(err)
	}
	profile := map[string]any{"team": "alpha", "score": json.Number("4")}
	if _, err := service.SetProfile(ctx, session.User.ID, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `update pgbase_meta.users set verified=true,email_visibility=true where id=$1`, session.User.ID); err != nil {
		t.Fatal(err)
	}
	current := AuthRecord{ID: session.User.ID, Email: session.User.Email, Verified: true, Active: true, EmailVisibility: true, Fields: profile}
	actor := identity.Subject{Kind: identity.User, ID: session.User.ID}
	for _, test := range []struct {
		rule      string
		principal identity.Subject
		request   map[string]any
		want      bool
	}{
		{"", actor, nil, true},
		{"true", actor, nil, true},
		{"false", actor, nil, false},
		{"emailVisibility = true", actor, nil, true},
		{"emailVisibility = false", actor, nil, false},
		{"score = 4", actor, nil, true},
		{"score = 3", actor, nil, false},
		{"@request.auth.id = id && verified = true && score >= 4 && @request.body.team:isset = true && @request.body.team:changed = true", actor, map[string]any{"team": "beta"}, true},
		{"@request.body.team:changed = true", actor, map[string]any{"team": "alpha"}, false},
		{"@request.body.score:isset = true", actor, map[string]any{"team": "beta"}, false},
		{"@request.body.score:changed = true", actor, map[string]any{"score": float64(4)}, false},
		{"@request.auth.id = id", identity.Subject{Kind: identity.Anonymous}, nil, false},
	} {
		t.Run(test.rule, func(t *testing.T) {
			var allowed bool
			err := service.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
				var err error
				allowed, err = EvaluateAuthRecordRuleInTx(ctx, tx, realm, test.rule, AuthRuleContext{Principal: test.principal, Current: current, Request: test.request})
				return err
			})
			if err != nil || allowed != test.want {
				t.Fatalf("SQL rule allowed=%v want=%v err=%v", allowed, test.want, err)
			}
		})
	}
}
