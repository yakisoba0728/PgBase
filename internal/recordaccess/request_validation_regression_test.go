package recordaccess_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestValidateRequestBindingsWithoutExecutionValues(t *testing.T) {
	collection := schema.Collection{ID: uuid.New(), Name: "notes", TableName: "notes", Fields: []schema.Field{{Name: "title", ColumnName: "title", Type: schema.TypeText}, {Name: "payload", ColumnName: "payload", Type: schema.TypeJSONB}}}
	catalog := schema.Catalog{Collections: map[string]schema.Collection{"notes": collection}}
	for _, expression := range []string{
		`@request.query.q:length = 5`, `@request.headers.accept:length = 5`,
		`@request.method = 'GET'`, `@request.query.q = 'value'`, `@request.headers.accept:isset = true`,
		`@request.context.mode = 'test'`, `@request.auth.email = 'alice'`, `@request.auth.verified = true`,
		`title = @request.auth.email`, `@request.body.title:lower = 'hello'`,
		`@request.body.title:length = 5`, `@request.body.payload:each ?= 'hello'`,
	} {
		t.Run(expression, func(t *testing.T) {
			rule, err := query.ParseAccessRule(expression)
			if err != nil {
				t.Fatal(err)
			}
			rules := accessrule.Rules{List: rule, View: rule, Create: rule, Update: rule, Delete: rule}
			if err = recordaccess.ValidateRules(catalog, collection, rules); err != nil {
				t.Fatal(err)
			}
			if _, _, err = accessrule.CompilePolicy(rule, accessrule.PolicyContext{Catalog: catalog, Collection: collection, Alias: "notes"}); err == nil {
				t.Fatal("execution accepted a missing request")
			}
		})
	}
}

func TestRequestLookupRejectsUnknownModifierWhenAbsent(t *testing.T) {
	collection := schema.Collection{Name: "notes", TableName: "notes"}
	context := accessrule.PolicyContext{Collection: collection, Alias: "notes", Request: &accessrule.RequestContext{}}
	for _, field := range []string{"__pgbase_request_query_q:unknown", "__pgbase_request_header_q:unknown"} {
		rule := accessrule.Rule{Binding: accessrule.BindingCatalog, Op: "eq", Field: field, Value: []byte(`""`)}
		if _, _, err := accessrule.CompilePolicy(rule, context); err == nil {
			t.Fatalf("unknown modifier accepted: %s", field)
		}
		if err := accessrule.ValidatePolicy(rule, context); err == nil {
			t.Fatalf("unknown modifier validated: %s", field)
		}
	}
}
