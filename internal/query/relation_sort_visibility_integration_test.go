//go:build integration

package query

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// A relation target the caller cannot view must contribute a NULL sort key,
// exactly like a missing relation, both for a bare relation key and for a
// relation path, on the first page and through anchor-only cursors. The
// hidden targets are given ids and titles whose order would be observable if
// they still ordered rows.
func TestRelationSortTreatsHiddenTargetsAsMissing(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: "relvis_targets", Fields: []schema.FieldInput{
		{Name: "title", Type: schema.TypeText, Required: true, Indexed: true},
		{Name: "visible", Type: schema.TypeText},
	}})
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: "relvis_posts", Fields: []schema.FieldInput{
		{Name: "target", Type: schema.TypeRelation, Relation: &schema.RelationInput{
			TargetCollection: "relvis_targets", Cardinality: schema.RelationSingle, OnDelete: schema.RelationRestrict,
		}},
	}})

	type target struct {
		id      string
		title   string
		visible bool
	}
	targets := []target{}
	for _, spec := range []struct {
		title   string
		visible bool
	}{{"a", false}, {"b", true}, {"c", false}, {"d", true}, {"e", false}} {
		visible := "no"
		if spec.visible {
			visible = "yes"
		}
		created, err := records.Create(ctx, "relvis_targets", map[string]json.RawMessage{
			"title": json.RawMessage(`"` + spec.title + `"`), "visible": json.RawMessage(`"` + visible + `"`),
		})
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, target{id: created["id"].(string), title: spec.title, visible: spec.visible})
	}
	type post struct {
		id     string
		target *target
	}
	posts := []post{}
	references := []*target{nil, nil}
	for index := range targets {
		references = append(references, &targets[index], &targets[index])
	}
	for _, reference := range references {
		input := map[string]json.RawMessage{}
		if reference != nil {
			input["target"] = json.RawMessage(`"` + reference.id + `"`)
		}
		created, err := records.Create(ctx, "relvis_posts", input)
		if err != nil {
			t.Fatal(err)
		}
		posts = append(posts, post{id: created["id"].(string), target: reference})
	}

	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	allow := accessrule.Rule{Op: "allow"}
	for _, name := range []string{"relvis_targets", "relvis_posts"} {
		if _, err := recordaccess.NewService(pool).Set(ctx, name, true); err != nil {
			t.Fatal(err)
		}
		view := allow
		if name == "relvis_targets" {
			view = accessrule.Rule{Op: "eq", Field: "visible", Value: json.RawMessage(`"yes"`)}
		}
		raw, _ := json.Marshal(accessrule.Rules{List: allow, View: view, Create: allow, Update: allow, Delete: allow})
		if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.collection_rules(collection_id,rules) VALUES($1,$2)`, catalog.Collections[name].ID, raw); err != nil {
			t.Fatal(err)
		}
	}

	// expected orders posts the way the database must for a caller that sees
	// only the visible targets: the key of a hidden or missing target is NULL,
	// NULLs sort last in either direction, and id breaks ties in the direction
	// of the last requested key.
	expected := func(desc, administrator bool, key func(target) string) []string {
		ordered := append([]post(nil), posts...)
		value := func(p post) *string {
			if p.target == nil || (!p.target.visible && !administrator) {
				return nil
			}
			text := key(*p.target)
			return &text
		}
		sort.SliceStable(ordered, func(i, j int) bool {
			left, right := value(ordered[i]), value(ordered[j])
			switch {
			case left == nil && right == nil:
			case left == nil:
				return false
			case right == nil:
				return true
			case *left != *right:
				return (*left < *right) != desc
			}
			return (ordered[i].id < ordered[j].id) != desc
		})
		ids := make([]string, len(ordered))
		for index := range ordered {
			ids[index] = ordered[index].id
		}
		return ids
	}
	byID := func(target target) string { return target.id }
	byTitle := func(target target) string { return target.title }
	administrator := Principal{Kind: identity.Administrator, ID: uuid.New()}
	anonymous := Principal{Kind: identity.Anonymous}

	for _, test := range []struct {
		sort string
		desc bool
		key  func(target) string
	}{
		{"target", false, byID}, {"-target", true, byID},
		{"target.title", false, byTitle}, {"-target.title", true, byTitle},
	} {
		all, err := records.List(ctx, ListRequest{Principal: administrator, Collection: "relvis_posts", Sort: test.sort, Limit: 100})
		if err != nil {
			t.Fatalf("%s administrator: %v", test.sort, err)
		}
		if got, want := recordIDs(all.Items), expected(test.desc, true, test.key); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s administrator order = %v, want %v", test.sort, got, want)
		}

		want := expected(test.desc, false, test.key)
		full, err := records.List(ctx, ListRequest{Principal: anonymous, Collection: "relvis_posts", Sort: test.sort, Limit: 100})
		if err != nil {
			t.Fatalf("%s anonymous: %v", test.sort, err)
		}
		if got := recordIDs(full.Items); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s anonymous order = %v, want hidden targets ordered as missing %v", test.sort, got, want)
		}

		got := []string{}
		request := ListRequest{Principal: anonymous, Collection: "relvis_posts", Sort: test.sort, Limit: 2}
		for pages := 0; ; pages++ {
			if pages > len(posts) {
				t.Fatalf("%s pagination did not terminate", test.sort)
			}
			page, err := records.List(ctx, request)
			if err != nil {
				t.Fatalf("%s anonymous page %d: %v", test.sort, pages, err)
			}
			got = append(got, recordIDs(page.Items)...)
			if page.NextCursor == "" {
				break
			}
			request.Cursor = page.NextCursor
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s anonymous pages = %v, want %v", test.sort, got, want)
		}
	}

	// Once a target becomes visible, a cursor anchored on a row whose key it
	// changed is refused instead of continuing from a stale position.
	request := ListRequest{Principal: anonymous, Collection: "relvis_posts", Sort: "target", Limit: 4}
	first, err := records.List(ctx, request)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page err=%v cursor=%q", err, first.NextCursor)
	}
	anchor := first.Items[len(first.Items)-1]["id"].(string)
	var anchorTarget *target
	for _, p := range posts {
		if p.id == anchor {
			anchorTarget = p.target
		}
	}
	if anchorTarget == nil || !anchorTarget.visible {
		t.Fatalf("page anchor %s does not reference a visible target", anchor)
	}
	targetCollection := catalog.Collections["relvis_targets"]
	visibleField, _ := filterField(targetCollection, "visible")
	if _, err := pool.Exec(ctx, `UPDATE `+quotedTable(targetCollection)+` SET `+quoted(visibleField.ColumnName)+`='no' WHERE id=$1`, anchorTarget.id); err != nil {
		t.Fatal(err)
	}
	request.Cursor = first.NextCursor
	if _, err := records.List(ctx, request); !IsInvalidCursor(err) {
		t.Fatalf("continuation after the anchor's target became hidden err=%v, want invalid cursor", err)
	}
}
