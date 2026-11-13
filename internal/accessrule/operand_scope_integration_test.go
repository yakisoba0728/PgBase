//go:build integration

package accessrule_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestPolicyOperandScopesAndRequestValidation(t *testing.T) {
	ctx := context.Background()
	db, err := pgx.Connect(ctx, testutil.NewDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	_, err = db.Exec(ctx, `CREATE SCHEMA app;
 CREATE TABLE app.people(id uuid PRIMARY KEY,email text);
 CREATE TABLE app.posts(id uuid PRIMARY KEY,author uuid,reviewer uuid,payload jsonb);
 INSERT INTO app.people VALUES ('00000000-0000-0000-0000-000000000001','alice'),('00000000-0000-0000-0000-000000000002','bob');
 INSERT INTO app.posts VALUES ('00000000-0000-0000-0000-000000000003','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000002','["alice"]');`)
	if err != nil {
		t.Fatal(err)
	}
	people := schema.Collection{ID: uuid.New(), Name: "people", TableName: "people", Fields: []schema.Field{{Name: "email", ColumnName: "email", Type: schema.TypeText}}}
	posts := schema.Collection{ID: uuid.New(), Name: "posts", TableName: "posts", Fields: []schema.Field{{Name: "payload", ColumnName: "payload", Type: schema.TypeJSONB}}}
	for _, name := range []string{"author", "reviewer"} {
		posts.Fields = append(posts.Fields, schema.Field{Name: name, ColumnName: name, Type: schema.TypeRelation, Relation: &schema.RelationSpec{TargetCollectionID: people.ID, Cardinality: schema.RelationSingle}})
	}
	catalog := schema.Catalog{Collections: map[string]schema.Collection{"people": people, "posts": posts}}
	request := &accessrule.RequestContext{Method: "GET", Query: map[string][]string{"a": {"alice"}, "b": {"bob"}}, Auth: map[string]any{"email": "alice"}}
	for _, test := range []struct {
		expression string
		want       bool
	}{
		{`@request.auth.email ?< reviewer.email`, true},
		{`@request.auth.email ?<= reviewer.email`, true},
		{`@request.auth.email ?> reviewer.email`, false},
		{`@request.auth.email ?>= reviewer.email`, false},
		{`author.email ?= reviewer.email`, false}, {`author.email = reviewer.email`, false},
		{`author.email ?= author.email`, true}, {`author.email = author.email`, true},
		{`@collection.posts.author.email ?= 'alice'`, true},
		{`@collection.posts.payload:each ?= 'alice'`, true},
		{`@request.query.q:length = 0`, true},
		{`@request.headers.accept:length = 0`, true},
		{`@request.query.a:length = 5`, true},
		{`@request.query.a:each ?= @request.query.b:each`, false},
		{`@request.query.a:each ?= @request.query.a:each`, true},
		{`@request.method = 'GET' && @request.auth.email = 'alice'`, true},
	} {
		t.Run(test.expression, func(t *testing.T) {
			rule, e := query.ParseAccessRule(test.expression)
			if e != nil {
				t.Fatal(e)
			}
			rules := accessrule.Rules{List: rule, View: rule, Create: rule, Update: rule, Delete: rule}
			if e = recordaccess.ValidateRules(catalog, posts, rules); e != nil {
				t.Fatalf("save validation: %v", e)
			}
			sql, args, e := accessrule.CompilePolicy(rule, accessrule.PolicyContext{Catalog: catalog, Collection: posts, Alias: "posts", Request: request})
			if e != nil {
				t.Fatal(e)
			}
			var got bool
			if e = db.QueryRow(ctx, `SELECT (`+sql+`) FROM app.posts posts`, args...).Scan(&got); e != nil {
				t.Fatalf("runtime: %v; SQL %s", e, sql)
			}
			if got != test.want {
				t.Fatalf("allowed=%v want=%v; SQL %s", got, test.want, sql)
			}
		})
	}
	for _, binding := range []string{"", accessrule.BindingScalar, accessrule.BindingCatalog} {
		for _, op := range []string{"contains", "not_contains", "any_contains", "any_not_contains"} {
			t.Run("contains_"+binding+"_"+op, func(t *testing.T) {
				rule := accessrule.Rule{Binding: binding, Op: op, Field: "email", Value: json.RawMessage(`"li"`)}
				sql, args, err := accessrule.CompilePolicy(rule, accessrule.PolicyContext{Catalog: catalog, Collection: people, Alias: "people"})
				if err != nil {
					t.Fatal(err)
				}
				var got bool
				if err := db.QueryRow(ctx, "SELECT ("+sql+") FROM app.people people WHERE email='alice'", args...).Scan(&got); err != nil {
					t.Fatalf("runtime: %v; SQL %s", err, sql)
				}
				if want := op == "contains" || op == "any_contains"; got != want {
					t.Fatalf("allowed=%v want=%v", got, want)
				}
			})
		}
	}
	for _, name := range []string{"pb_operand_left", "pgbase_operand_left", "pgbase_operand_right"} {
		t.Run(name, func(t *testing.T) {
			table := pgx.Identifier{"app", name}.Sanitize()
			if _, err := db.Exec(ctx, "CREATE TABLE "+table+" AS TABLE app.posts"); err != nil {
				t.Fatal(err)
			}
			collection := posts
			collection.Name, collection.TableName = name, name
			for _, expression := range []string{"author.email ?= reviewer.email", "author.email = reviewer.email", "author.email ?= author.email"} {
				rule, err := query.ParseAccessRule(expression)
				if err != nil {
					t.Fatal(err)
				}
				sql, args, err := accessrule.CompilePolicy(rule, accessrule.PolicyContext{Catalog: catalog, Collection: collection, Alias: name, EventAlias: "pgbase_operand_left"})
				if err != nil {
					t.Fatal(err)
				}
				var got bool
				if err = db.QueryRow(ctx, "SELECT ("+sql+") FROM "+table+" "+pgx.Identifier{name}.Sanitize(), args...).Scan(&got); err != nil {
					t.Fatalf("%s: %v; SQL %s", expression, err, sql)
				}
				if want := expression == "author.email ?= author.email"; got != want {
					t.Fatalf("%s allowed=%v want=%v", expression, got, want)
				}
			}
		})
	}

}
