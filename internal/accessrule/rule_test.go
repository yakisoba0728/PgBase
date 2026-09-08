package accessrule

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/identity"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func TestTypedRuleBindsValuesAndRejectsAmbiguousAST(t *testing.T) {
	collection := schema.Collection{ID: uuid.New(), TableName: "notes", Fields: []schema.Field{{Name: "title", ColumnName: "title", Type: schema.TypeText}, {Name: "public", ColumnName: "public", Type: schema.TypeBoolean}, {Name: "author", ColumnName: "author", Type: schema.TypeUUID}}}
	principal := identity.Subject{Kind: identity.User, ID: uuid.New()}
	rule := Rule{Op: "any", Args: []Rule{{Op: "owner"}, {Op: "eq", Field: "title", Value: json.RawMessage(`"'); DROP TABLE notes; --"`)}}}
	sql, args, err := Compile(rule, collection, "notes", principal, 3, false)
	if err != nil || len(args) != 3 || strings.Contains(sql, "DROP") || !strings.Contains(sql, "$6") {
		t.Fatalf("bound rule sql=%s args=%d err=%v", sql, len(args), err)
	}
	invalid := []Rule{
		{}, {Op: "not", Args: []Rule{{Op: "allow"}}}, {Op: "all"}, {Op: "allow", Field: "title"},
		{Op: "eq", Field: "missing", Value: json.RawMessage(`true`)}, {Op: "eq", Field: "public", Value: json.RawMessage(`"true"`)},
		{Op: "lt", Field: "public", Value: json.RawMessage(`true`)}, {Op: "eq", Field: "author", Value: json.RawMessage(`"not-a-uuid"`)},
		{Op: "eq", Field: "title", Value: json.RawMessage(`"x"`), Auth: "id"}, {Op: "eq", Field: "title", Auth: "id"},
	}
	for _, rule := range invalid {
		if _, _, err := Compile(rule, collection, "notes", principal, 0, false); err == nil {
			t.Errorf("accepted invalid rule=%+v", rule)
		}
	}
	deep := Rule{Op: "allow"}
	for range 10 {
		deep = Rule{Op: "all", Args: []Rule{deep}}
	}
	if _, _, err := Compile(deep, collection, "notes", principal, 0, false); err == nil {
		t.Fatal("accepted unbounded AST depth")
	}
}

func TestRuleJSONAndOwnerCompatibility(t *testing.T) {
	raw, _ := json.Marshal(OwnerRules())
	parsed, err := Parse(raw)
	if err != nil || parsed.View.Op != "owner" {
		t.Fatalf("owner default %v", err)
	}
	for _, raw := range []string{`{}`, `{"list":{"op":"allow","sql":"SELECT 1"}}`, `null`, `{} {}`} {
		rules, err := Parse([]byte(raw))
		if err == nil {
			_, _, err = Compile(rules.List, schema.Collection{}, "x", identity.Subject{}, 0, false)
		}
		if err == nil {
			t.Errorf("accepted invalid JSON %s", raw)
		}
	}
}
