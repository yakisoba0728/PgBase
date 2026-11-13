//go:build integration

package userauth

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestAuthManyOperandScopes(t *testing.T) {
	ctx := context.Background()
	db, err := pgx.Connect(ctx, testutil.NewDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	realm := uuid.New()
	_, err = db.Exec(ctx, `CREATE SCHEMA pgbase_meta;
 CREATE TABLE pgbase_meta.users(id uuid PRIMARY KEY,auth_collection_id uuid,email text);
 CREATE TABLE pgbase_meta.user_auth_profiles(user_id uuid PRIMARY KEY,attributes jsonb);`)
	if err != nil {
		t.Fatal(err)
	}
	alice, bob, root := uuid.New(), uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO pgbase_meta.users VALUES ($1,$4,'alice'),($2,$4,'bob'),($3,$4,'root')`, alice, bob, root, realm)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO pgbase_meta.user_auth_profiles VALUES ($1,jsonb_build_object('authors',jsonb_build_array($2::text),'reviewers',jsonb_build_array($3::text),'tags',jsonb_build_array('alice'),'labels',jsonb_build_array('bob')))`, root, alice, bob)
	if err != nil {
		t.Fatal(err)
	}
	collection := authRuleCollection{ID: realm, Name: "people", Fields: []schema.FieldInput{{Name: "tags", Type: schema.TypeJSONB}, {Name: "labels", Type: schema.TypeJSONB}}}
	for _, name := range []string{"authors", "reviewers"} {
		collection.Fields = append(collection.Fields, schema.FieldInput{Name: name, Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "people", Cardinality: schema.RelationMultiple}})
	}
	resolver := authRuleResolver{root: collection, collections: map[string]authRuleCollection{"people": collection}}
	for _, test := range []struct {
		expression string
		want       bool
	}{
		{`authors.email ?= reviewers.email`, false}, {`authors.email = reviewers.email`, false},
		{`authors.email ?= authors.email`, true}, {`tags ?= labels`, false}, {`tags ?= tags`, true},
		{`@collection.people.email ?= @collection.people.email`, true},
	} {
		t.Run(test.expression, func(t *testing.T) {
			rule, e := query.ParseAccessRule(test.expression)
			if e != nil {
				t.Fatal(e)
			}
			sql, args, e := accessrule.CompileExpression(rule, accessrule.ExpressionContext{Resolver: resolver, ArgOffset: 1})
			if e != nil {
				t.Fatal(e)
			}
			var got bool
			e = db.QueryRow(ctx, `SELECT (`+sql+`) FROM pgbase_meta.users u LEFT JOIN pgbase_meta.user_auth_profiles p ON p.user_id=u.id WHERE u.id=$1`, append([]any{root}, args...)...).Scan(&got)
			if e != nil || got != test.want {
				t.Fatalf("allowed=%v want=%v err=%v SQL=%s", got, test.want, e, sql)
			}
		})
	}
}
