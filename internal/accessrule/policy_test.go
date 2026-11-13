package accessrule

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestPolicyMeaningDoesNotDependOnSourceMetadata(t *testing.T) {
	collection := schema.Collection{ID: uuid.New(), Name: "notes", TableName: "notes", Fields: []schema.Field{{Name: "title", ColumnName: "title", Type: schema.TypeText}}}
	context := PolicyContext{Collection: collection, Catalog: schema.Catalog{Collections: map[string]schema.Collection{"notes": collection}}, Alias: "notes", Principal: identity.Subject{Kind: identity.User, ID: uuid.New()}, ArgOffset: 2}
	for _, binding := range []string{"", BindingScalar, BindingCatalog} {
		rule := Rule{Op: "contains", Field: "title", Binding: binding, Value: json.RawMessage(`"needle"`)}
		beforeSQL, beforeArgs, err := CompilePolicy(rule, context)
		if err != nil {
			t.Fatal(err)
		}
		source := "unrelated display text"
		rule.Expression = &source
		afterSQL, afterArgs, err := CompilePolicy(rule, context)
		if err != nil || beforeSQL != afterSQL || !reflect.DeepEqual(beforeArgs, afterArgs) {
			t.Fatalf("binding %q source changed policy: %s/%s %v", binding, beforeSQL, afterSQL, err)
		}
		raw, err := json.Marshal(rule)
		if err != nil {
			t.Fatal(err)
		}
		var restored Rule
		if err := json.Unmarshal(raw, &restored); err != nil {
			t.Fatal(err)
		}
		restored.Expression = nil
		afterSQL, afterArgs, err = CompilePolicy(restored, context)
		if err != nil || beforeSQL != afterSQL || !reflect.DeepEqual(beforeArgs, afterArgs) {
			t.Fatalf("binding %q roundtrip changed policy: %s/%s %v", binding, beforeSQL, afterSQL, err)
		}
	}
}

func TestPolicyComposesCatalogAndOwnershipWithEventAndProspectiveBindings(t *testing.T) {
	collection := schema.Collection{ID: uuid.New(), Name: "notes", TableName: "notes", Fields: []schema.Field{{Name: "title", ColumnName: "title", Type: schema.TypeText}}}
	context := PolicyContext{Collection: collection, Catalog: schema.Catalog{Collections: map[string]schema.Collection{"notes": collection}}, Alias: "notes", Principal: identity.Subject{Kind: identity.User, ID: uuid.New()}, ArgOffset: 1, EventAlias: "event"}
	rule := Rule{Op: "all", Args: []Rule{{Op: "owner"}, {Op: "eq", Binding: BindingCatalog, Field: "title", Value: json.RawMessage(`"published"`)}}}
	sql, args, err := CompilePolicy(rule, context)
	if err != nil || !strings.Contains(sql, `"event".owner_user_id=$2`) || !strings.Contains(sql, "$3") || len(args) != 2 {
		t.Fatalf("event mixed policy: %s %v %v", sql, args, err)
	}
	context.EventAlias, context.ProspectiveOwner = "", true
	sql, args, err = CompilePolicy(rule, context)
	if err != nil || strings.Contains(sql, "record_owners") || len(args) != 1 || !strings.Contains(sql, "TRUE") {
		t.Fatalf("prospective mixed policy: %s %v %v", sql, args, err)
	}
}

func TestPolicyRejectsUnknownBindingAndMigratesHistoricalSource(t *testing.T) {
	for _, raw := range []string{
		`{"op":"allow","binding":"unknown"}`,
		`{"op":"all","args":[{"op":"allow","binding":"unknown"}]}`,
	} {
		var rule Rule
		if json.Unmarshal([]byte(raw), &rule) == nil {
			t.Fatalf("accepted unknown binding: %s", raw)
		}
	}
	var historical Rule
	if err := json.Unmarshal([]byte(`{"op":"eq","field":"title","value":"published","pocketbase":"title = 'published'"}`), &historical); err != nil || historical.Binding != BindingCatalog {
		t.Fatalf("historical source did not retain binding: %#v %v", historical, err)
	}
	var native Rule
	if err := json.Unmarshal([]byte(`{"op":"eq","field":"title","value":"published","expression":"display source"}`), &native); err != nil || native.Binding != "" {
		t.Fatalf("native source changed default semantics: %#v %v", native, err)
	}
	_, _, err := CompilePolicy(Rule{Op: "allow", Binding: "unknown"}, PolicyContext{Alias: "notes"})
	if err == nil {
		t.Fatal("unknown programmatic binding accepted")
	}
}
