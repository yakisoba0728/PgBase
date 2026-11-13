//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// Relations do not block public access (docs/user-access.md once said so);
// instead every relation path is checked against the target's own view rule.
// Each assertion below would observe the hidden target if that check leaked.
func TestPublicRelationPathsNeverRevealHiddenTargets(t *testing.T) {
	f := newUserAccessFixture(t)
	ctx := context.Background()
	apply := func(input schema.CreateCollectionInput) {
		plan, err := f.schemas.Preview(ctx, schema.NewCreateChange(input))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(input), plan, f.admin.ID)); err != nil {
			t.Fatal(err)
		}
	}
	maximum := 4
	apply(schema.CreateCollectionInput{Name: "rel_targets", Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText, Indexed: true}}})
	apply(schema.CreateCollectionInput{Name: "rel_sources", Fields: []schema.FieldInput{
		{Name: "name", Type: schema.TypeText},
		{Name: "target", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "rel_targets", Cardinality: schema.RelationSingle, OnDelete: schema.RelationSetNull}},
		{Name: "tags", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "rel_targets", Cardinality: schema.RelationMultiple, MaxSelect: &maximum}},
	}})
	f.enable(t, "rel_targets")
	f.enable(t, "rel_sources")
	allow := accessrule.Rule{Op: "allow"}
	visibleTarget := accessrule.Rule{Op: "eq", Field: "name", Value: json.RawMessage(`"visible"`)}
	visibleSource := accessrule.Rule{Op: "ne", Field: "name", Value: json.RawMessage(`"hidden-source"`)}
	rules := recordaccess.NewRuleService(f.pool, f.schemas)
	if _, err := rules.Set(ctx, "rel_targets", accessrule.Rules{List: visibleTarget, View: visibleTarget, Create: allow, Update: allow, Delete: allow}); err != nil {
		t.Fatal(err)
	}
	if _, err := rules.Set(ctx, "rel_sources", accessrule.Rules{List: visibleSource, View: visibleSource, Create: allow, Update: allow, Delete: allow}); err != nil {
		t.Fatal(err)
	}
	create := func(principal Principal, collection, body string) (string, error) {
		var input map[string]json.RawMessage
		if err := json.Unmarshal([]byte(body), &input); err != nil {
			t.Fatal(err)
		}
		result, err := f.records.Create(ctx, CreateRequest{Principal: principal, RequestID: "relation-" + uuid.NewString(), Collection: collection, Input: input})
		if err != nil {
			return "", err
		}
		return result.Record.Data["id"].(string), nil
	}
	must := func(id string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	visible := must(create(f.admin, "rel_targets", `{"name":"visible"}`))
	hidden := must(create(f.admin, "rel_targets", `{"name":"secret"}`))
	must(create(f.admin, "rel_sources", fmt.Sprintf(`{"name":"s1","target":%q,"tags":[%q,%q]}`, hidden, visible, hidden)))
	s2 := must(create(f.admin, "rel_sources", fmt.Sprintf(`{"name":"s2","target":%q}`, visible)))
	must(create(f.admin, "rel_sources", fmt.Sprintf(`{"name":"hidden-source","target":%q}`, hidden)))

	for _, principal := range []Principal{{Kind: identity.Anonymous}, f.first} {
		list := func(collection, filter, sort, expand string) []map[string]any {
			t.Helper()
			page, err := f.records.List(ctx, ListRequest{Principal: principal, RequestID: "relation-list-" + uuid.NewString(), Collection: collection, Limit: 50, Filter: filter, Sort: sort, Expand: expand})
			if err != nil {
				t.Fatalf("%v list %s filter=%q sort=%q expand=%q: %v", principal.Kind, collection, filter, sort, expand, err)
			}
			return page.Items
		}
		names := func(items []map[string]any) []string {
			result := []string{}
			for _, item := range items {
				result = append(result, item["name"].(string))
			}
			return result
		}
		// Raw single and multiple relation IDs.
		for _, item := range list("rel_sources", "", "", "") {
			switch item["name"] {
			case "s1":
				if item["target"] != nil || fmt.Sprint(item["tags"]) != fmt.Sprint([]any{visible}) {
					t.Fatalf("%v s1 relation ids = %v / %v", principal.Kind, item["target"], item["tags"])
				}
			case "hidden-source":
				t.Fatalf("%v listed a source hidden by its own rule", principal.Kind)
			}
		}
		// Filters through a relation path must not act as an existence oracle.
		for _, filter := range []string{"target.name = 'secret'", "tags.name = 'secret'", "target.name ~ 'sec'"} {
			if got := names(list("rel_sources", filter, "", "")); len(got) != 0 {
				t.Fatalf("%v filter %q matched %v through a hidden target", principal.Kind, filter, got)
			}
		}
		if got := names(list("rel_sources", "target.name = 'visible'", "", "")); !slices.Equal(got, []string{"s2"}) {
			t.Fatalf("%v visible filter = %v", principal.Kind, got)
		}
		// A hidden target sorts as an absent value (nulls last), never by its name.
		if got := names(list("rel_sources", "", "target.name", "")); !slices.Equal(got, []string{"s2", "s1"}) {
			t.Fatalf("%v sort by hidden target = %v", principal.Kind, got)
		}
		for _, item := range list("rel_sources", "", "", "target,tags") {
			expanded, _ := json.Marshal(item["expand"])
			if strings.Contains(string(expanded), "secret") {
				t.Fatalf("%v expand revealed the hidden target: %s", principal.Kind, expanded)
			}
		}
		// Reverse paths apply the source's own rule.
		if got := names(list("rel_targets", "rel_sources_via_target.name = 'hidden-source'", "", "")); len(got) != 0 {
			t.Fatalf("%v reverse filter matched %v through a hidden source", principal.Kind, got)
		}
		// Writes cannot point at a hidden target.
		if _, err := create(principal, "rel_sources", fmt.Sprintf(`{"name":"probe","tags":[%q]}`, hidden)); !errors.Is(err, query.ErrRecordNotFound) {
			t.Fatalf("%v create with hidden multi target = %v", principal.Kind, err)
		}
		_, err := f.records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "relation-update-" + uuid.NewString(), Collection: "rel_sources", ID: uuid.MustParse(s2), Input: map[string]json.RawMessage{"target": json.RawMessage(fmt.Sprintf("%q", hidden))}})
		if !errors.Is(err, query.ErrRecordNotFound) {
			t.Fatalf("%v update to hidden target = %v", principal.Kind, err)
		}
	}
}
