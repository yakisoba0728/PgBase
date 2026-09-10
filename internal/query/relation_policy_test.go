package query

import (
	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"strings"
	"testing"
)

func TestRelationPageAppliesRootAndHopPoliciesBeforeLimit(t *testing.T) {
	target := schema.Collection{ID: uuid.New(), Name: "targets", TableName: "targets"}
	field := schema.Field{Name: "target", ColumnName: "target", Type: schema.TypeRelation, Relation: &schema.RelationSpec{TargetCollectionID: target.ID, Cardinality: schema.RelationSingle}}
	root := schema.Collection{ID: uuid.New(), Name: "roots", TableName: "roots", Fields: []schema.Field{field}}
	allow := accessrule.Rule{Op: "allow"}
	path := &plannedRelationPath{hops: []plannedRelationHop{{field: field, target: target, rule: &allow, ruleRevision: 7, principal: Principal{Kind: identity.User, ID: uuid.New()}}}, terminal: schema.Field{Name: "id", ColumnName: "id", Type: schema.TypeUUID}}
	plan := relationListPlan{filter: &plannedRelationFilter{comparison: &plannedRelationComparison{input: filterComparison{field: "target.id", operator: "=", value: filterLiteral{kind: filterLiteralString, text: uuid.NewString()}}, path: path}}, order: orderSpec{root: root, terms: []orderTerm{{key: SortKey{Field: "target.id"}, cursorField: path.terminal, relationPath: path}}}}
	deny := accessrule.Rule{Op: "deny"}
	statement, err := plan.pageStatementWithPolicy(nil, 10, nil, ruleBinding{rule: &deny, principal: Principal{Kind: identity.User, ID: uuid.New()}, collection: root})
	if err != nil {
		t.Fatal(err)
	}
	limit := strings.LastIndex(statement.SQL, " LIMIT ")
	if limit < 0 || !strings.Contains(statement.SQL[:limit], "FALSE") || strings.Count(statement.SQL[:limit], "TRUE") < 2 {
		t.Fatalf("missing pre-limit policies: %s", statement.SQL)
	}
	if got := relationPolicyRevisionString(plan); !strings.Contains(got, target.ID.String()+":7") {
		t.Fatalf("revision binding=%q", got)
	}
}

func TestViewPolicyUsesSourceFieldOutsideProjection(t *testing.T) {
	sourceID := uuid.New()
	source := schema.Collection{ID: sourceID, Name: "source", TableName: "source", Fields: []schema.Field{{Name: "private", ColumnName: "private", Type: schema.TypeBoolean}}}
	view := schema.Collection{ID: uuid.New(), Name: "public_view", TableName: "public_view", View: &schema.ViewSpec{SourceCollectionID: sourceID, Source: source.Name}}
	rule := accessrule.Rule{Op: "eq", Field: "private", Value: []byte("true")}
	predicate, args, err := compilePolicyPredicate(rule, view, source, view.TableName, Principal{Kind: identity.Anonymous}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(predicate, `FROM "app"."source"`) || !strings.Contains(predicate, `"public_view_policy"."private"`) || !strings.Contains(predicate, `"public_view_policy"."id"="public_view"."id"`) {
		t.Fatalf("view predicate does not correlate source policy: %s", predicate)
	}
	if len(args) != 1 || args[0] != true {
		t.Fatalf("view predicate args = %#v", args)
	}
}
