//go:build integration

package query

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func TestListExpandedHydratesNestedForwardPathsBreadthFirstWithoutNPlusOne(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	graph := createExpandGraph(t, ctx, pool, schemas, records, 3)
	records.now = func() time.Time { return time.Date(2026, 8, 31, 7, 0, 0, 0, time.UTC) }
	base, err := records.List(ctx, "expand_posts", "", 2, "", "")
	if err != nil {
		t.Fatal(err)
	}

	page, err := records.ListExpanded(ctx, ListRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_posts",
		Limit:      2,
		Expand:     "owner.labels",
	})
	if err != nil {
		t.Fatalf("ListExpanded(owner.labels): %v", err)
	}
	if len(page.Items) != 2 || page.querySteps != 5 {
		t.Fatalf("nested page items/steps = %d/%d, want 2/5", len(page.Items), page.querySteps)
	}
	if page.NextCursor == "" || page.NextCursor != base.NextCursor {
		t.Fatalf("nested expand changed root cursor: expanded=%q base=%q", page.NextCursor, base.NextCursor)
	}
	for _, item := range page.Items {
		if item.Root["owner"] != graph.ada.String() {
			t.Fatalf("root owner = %#v", item.Root["owner"])
		}
		owner, ok := item.Expand["owner"].(map[string]any)
		if !ok {
			t.Fatalf("expanded owner = %#v", item.Expand["owner"])
		}
		if !reflect.DeepEqual(owner["labels"], []string{graph.secondLabel.String(), graph.firstLabel.String()}) {
			t.Fatalf("owner raw labels = %#v", owner["labels"])
		}
		nested, ok := owner["expand"].(map[string]any)
		if !ok {
			t.Fatalf("owner sibling expand = %#v", owner["expand"])
		}
		labels, ok := nested["labels"].([]map[string]any)
		if !ok || len(labels) != 2 || labels[0]["id"] != graph.secondLabel.String() || labels[1]["id"] != graph.firstLabel.String() {
			t.Fatalf("ordered nested labels = %#v", nested["labels"])
		}
	}

	one, err := records.ListExpanded(ctx, ListRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_posts",
		Limit:      1,
		Expand:     "owner.labels",
	})
	if err != nil {
		t.Fatal(err)
	}
	if one.querySteps != page.querySteps {
		t.Fatalf("nested query steps changed with root count: one=%d two=%d", one.querySteps, page.querySteps)
	}

	// The leaf IDs are asserted against independently created fixture values,
	// not against the raw owner labels used by the implementation.
	if uuid.MustParse(page.Items[0].Expand["owner"].(map[string]any)["id"].(string)) != graph.ada {
		t.Fatal("nested target identity changed")
	}
}

func TestGetExpandedHydratesCatalogIncomingReversePathsInDeterministicOrder(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	graph := createExpandGraph(t, ctx, pool, schemas, records, 3)
	posts, err := records.List(ctx, "expand_posts", "", 10, "", "")
	if err != nil {
		t.Fatal(err)
	}
	wantPostIDs := recordIDs(posts.Items)

	result, err := records.GetExpanded(ctx, GetRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_people",
		ID:         graph.ada,
		Expand:     "expand_posts_via_owner,expand_posts_via_reviewers",
	})
	if err != nil {
		t.Fatalf("GetExpanded(reverse): %v", err)
	}
	if !reflect.DeepEqual(result.Root["labels"], []string{graph.secondLabel.String(), graph.firstLabel.String()}) {
		t.Fatalf("reverse root raw labels = %#v", result.Root["labels"])
	}
	if result.querySteps != 8 {
		t.Fatalf("reverse query steps = %d, want root 2 + two reverse nodes * 3", result.querySteps)
	}
	for _, path := range []string{"expand_posts_via_owner", "expand_posts_via_reviewers"} {
		expanded, ok := result.Expand[path].([]map[string]any)
		if !ok || !reflect.DeepEqual(recordIDs(expanded), wantPostIDs) {
			t.Fatalf("%s records = %#v, want ids %v", path, result.Expand[path], wantPostIDs)
		}
		for _, post := range expanded {
			if !reflect.DeepEqual(post["reviewers"], []string{graph.bob.String(), graph.ada.String()}) {
				t.Fatalf("%s raw reviewer order = %#v", path, post["reviewers"])
			}
		}
	}
	batched, err := records.ListExpanded(ctx, ListRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_people",
		Limit:      2,
		Expand:     "expand_posts_via_owner,expand_posts_via_reviewers",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(batched.Items) != 2 || batched.querySteps != result.querySteps {
		t.Fatalf("reverse batch items/steps = %d/%d, single-root steps=%d", len(batched.Items), batched.querySteps, result.querySteps)
	}

	unreferenced, err := records.Create(ctx, "expand_people", map[string]json.RawMessage{"name": json.RawMessage(`"Nobody"`)})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := records.GetExpanded(ctx, GetRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_people",
		ID:         uuid.MustParse(unreferenced["id"].(string)),
		Expand:     "expand_posts_via_owner,expand_posts_via_reviewers",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"expand_posts_via_owner", "expand_posts_via_reviewers"} {
		expanded, ok := empty.Expand[path].([]map[string]any)
		if !ok || len(expanded) != 0 {
			t.Fatalf("empty reverse %s = %#v, want []", path, empty.Expand[path])
		}
	}
}

func TestReverseExpandAppliesPolicyBeforeJobWideNodeBudget(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "reverse_parents",
		Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText},
		},
	})
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "reverse_children",
		Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText},
			{Name: "parent", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "reverse_parents", Cardinality: schema.RelationSingle,
				OnDelete: schema.RelationRestrict,
			}},
		},
	})
	parent, err := records.Create(ctx, "reverse_parents", map[string]json.RawMessage{"name": json.RawMessage(`"parent"`)})
	if err != nil {
		t.Fatal(err)
	}
	parentID := uuid.MustParse(parent["id"].(string))
	secondParent, err := records.Create(ctx, "reverse_parents", map[string]json.RawMessage{"name": json.RawMessage(`"second"`)})
	if err != nil {
		t.Fatal(err)
	}
	secondParentID := uuid.MustParse(secondParent["id"].(string))
	firstChildID := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	table := pgx.Identifier{"app", "reverse_children"}.Sanitize()
	if _, err := pool.Exec(ctx, `insert into `+table+` (id, created_at, updated_at, name, parent)
		select ('00000000-0000-4000-8000-' || lpad(value::text, 12, '0'))::uuid,
			current_timestamp, current_timestamp, 'child-' || value::text,
			case when value <= 501 then $1::uuid else $2::uuid end
		from generate_series(1, 1002) as generated(value)`, parentID, secondParentID); err != nil {
		t.Fatal(err)
	}
	request := ListRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "reverse_parents",
		Limit:      2,
		Expand:     "reverse_children_via_parent",
	}

	// Neither parent exceeds 1,000 matches alone. The combined breadth-first
	// node job does, proving the reverse cap is global to this batched node and
	// cannot be multiplied by the number of roots.
	_, err = records.ListExpanded(ctx, request)
	assertValidationDetail(t, err, "expand", "expand_budget_exceeded")

	records.policy = projectionPolicyFunc(func(_ context.Context, _ Principal, collection schema.Collection, _ ReadAction) (Predicate, FieldSet, error) {
		if collection.Name == "reverse_children" {
			return DenyAllRecords(), AllFields(), nil
		}
		return AllowAllRecords(), AllFields(), nil
	})
	hidden, err := records.ListExpanded(ctx, request)
	if err != nil {
		t.Fatalf("deny-all reverse expand: %v", err)
	}
	for _, item := range hidden.Items {
		children, ok := item.Expand["reverse_children_via_parent"].([]map[string]any)
		if !ok || len(children) != 0 {
			t.Fatalf("deny-all reverse children = %#v", item.Expand["reverse_children_via_parent"])
		}
	}
	if hidden.querySteps != 1 {
		t.Fatalf("deny-all reverse query steps = %d, want root batch only", hidden.querySteps)
	}

	allowed, err := AllowRecordIDs(firstChildID)
	if err != nil {
		t.Fatal(err)
	}
	records.policy = projectionPolicyFunc(func(_ context.Context, _ Principal, collection schema.Collection, _ ReadAction) (Predicate, FieldSet, error) {
		if collection.Name == "reverse_children" {
			return allowed, ProjectFields("name"), nil
		}
		return AllowAllRecords(), AllFields(), nil
	})
	visible, err := records.ListExpanded(ctx, request)
	if err != nil {
		t.Fatalf("policy-filtered reverse expand: %v", err)
	}
	if len(visible.Items) != 2 {
		t.Fatalf("policy-filtered roots = %d, want 2", len(visible.Items))
	}
	visibleChildren := make([]map[string]any, 0, 1)
	for _, item := range visible.Items {
		children, ok := item.Expand["reverse_children_via_parent"].([]map[string]any)
		if !ok {
			t.Fatalf("policy-filtered reverse children = %#v", item.Expand["reverse_children_via_parent"])
		}
		visibleChildren = append(visibleChildren, children...)
	}
	if len(visibleChildren) != 1 || visibleChildren[0]["id"] != firstChildID.String() || visibleChildren[0]["name"] != "child-1" {
		t.Fatalf("policy-filtered reverse children = %#v", visibleChildren)
	}
	if visibleChildren[0]["parent"] != nil {
		t.Fatalf("FieldSet leaked reverse relation: %#v", visibleChildren[0])
	}
	if visible.querySteps != 3 {
		t.Fatalf("policy-filtered reverse query steps = %d, want base + discovery + target batch", visible.querySteps)
	}
}

func TestNestedForwardExpandBoundsSelfCycleByRequestedDepth(t *testing.T) {
	_, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "nested_cycle_nodes",
		Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText},
			{Name: "parent", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "nested_cycle_nodes", Cardinality: schema.RelationSingle,
				OnDelete: schema.RelationRestrict,
			}},
		},
	})
	first, err := records.Create(ctx, "nested_cycle_nodes", map[string]json.RawMessage{"name": json.RawMessage(`"first"`)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := records.Create(ctx, "nested_cycle_nodes", map[string]json.RawMessage{
		"name":   json.RawMessage(`"second"`),
		"parent": json.RawMessage(`"` + first["id"].(string) + `"`),
	})
	if err != nil {
		t.Fatal(err)
	}
	firstID := uuid.MustParse(first["id"].(string))
	secondID := uuid.MustParse(second["id"].(string))
	if _, err := records.Update(ctx, "nested_cycle_nodes", firstID, map[string]json.RawMessage{
		"parent": json.RawMessage(`"` + secondID.String() + `"`),
	}); err != nil {
		t.Fatal(err)
	}

	result, err := records.GetExpanded(ctx, GetRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "nested_cycle_nodes",
		ID:         secondID,
		Expand:     "parent.parent.parent.parent.parent.parent",
	})
	if err != nil {
		t.Fatalf("six-hop cycle expand: %v", err)
	}
	if result.querySteps != 7 {
		t.Fatalf("cycle query steps = %d, want base + six path nodes", result.querySteps)
	}
	want := []uuid.UUID{firstID, secondID, firstID, secondID, firstID, secondID}
	current := result.Expand["parent"].(map[string]any)
	for depth, wantID := range want {
		if current["id"] != wantID.String() {
			t.Fatalf("cycle depth %d id = %#v, want %s", depth+1, current["id"], wantID)
		}
		if depth == len(want)-1 {
			if _, found := current["expand"]; found {
				t.Fatalf("cycle expanded beyond requested depth: %#v", current)
			}
			break
		}
		current = current["expand"].(map[string]any)["parent"].(map[string]any)
	}
}

func TestExpandedTargetProjectionHydratesOnlySelectedMultiRelations(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "projection_labels",
		Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText},
		},
	})
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "projection_targets",
		Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText},
			{Name: "small", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "projection_labels", Cardinality: schema.RelationMultiple,
				MinSelect: expandInt(0), MaxSelect: expandInt(4), OnDelete: schema.RelationRestrict,
			}},
			{Name: "large", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "projection_labels", Cardinality: schema.RelationMultiple,
				MinSelect: expandInt(0), MaxSelect: expandInt(4), OnDelete: schema.RelationRestrict,
			}},
		},
	})
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "projection_roots",
		Fields: []schema.FieldInput{
			{Name: "owner", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "projection_targets", Cardinality: schema.RelationSingle,
				OnDelete: schema.RelationRestrict,
			}},
		},
	})
	labels := make([]uuid.UUID, 4)
	for index := range labels {
		record, err := records.Create(ctx, "projection_labels", map[string]json.RawMessage{
			"name": json.RawMessage(`"label"`),
		})
		if err != nil {
			t.Fatal(err)
		}
		labels[index] = uuid.MustParse(record["id"].(string))
	}
	target, err := records.Create(ctx, "projection_targets", map[string]json.RawMessage{"name": json.RawMessage(`"visible"`)})
	if err != nil {
		t.Fatal(err)
	}
	targetID := uuid.MustParse(target["id"].(string))
	insertExpandEdges(t, ctx, pool, "projection_targets", "small", targetID, labels[0])
	insertExpandEdges(t, ctx, pool, "projection_targets", "large", targetID, labels...)
	root, err := records.Create(ctx, "projection_roots", map[string]json.RawMessage{
		"owner": json.RawMessage(`"` + targetID.String() + `"`),
	})
	if err != nil {
		t.Fatal(err)
	}
	records.policy = projectionPolicyFunc(func(_ context.Context, _ Principal, collection schema.Collection, _ ReadAction) (Predicate, FieldSet, error) {
		if collection.Name == "projection_targets" {
			return AllowAllRecords(), ProjectFields("name", "small"), nil
		}
		return AllowAllRecords(), AllFields(), nil
	})
	records.expandLimits.maxRows = 2

	expanded, err := records.GetExpanded(ctx, GetRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "projection_roots",
		ID:         uuid.MustParse(root["id"].(string)),
		Expand:     "owner",
	})
	if err != nil {
		t.Fatalf("projected target expand: %v", err)
	}
	owner := expanded.Expand["owner"].(map[string]any)
	if !reflect.DeepEqual(owner["small"], []string{labels[0].String()}) || owner["large"] != nil {
		t.Fatalf("projected target = %#v", owner)
	}
	if expanded.querySteps != 3 {
		t.Fatalf("projected target query steps = %d, want base + target + selected hydration", expanded.querySteps)
	}
}

func TestNestedExpandStopsBeforeHiddenIntermediateFieldAndFiltersVisibleLeaf(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	graph := createExpandGraph(t, ctx, pool, schemas, records, 1)
	posts, err := records.List(ctx, "expand_posts", "", 1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	request := GetRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_posts",
		ID:         uuid.MustParse(posts.Items[0]["id"].(string)),
		Expand:     "owner.labels",
	}

	t.Run("hidden intermediate relation does not call leaf policy or query leaf", func(t *testing.T) {
		labelPolicyCalls := 0
		records.policy = projectionPolicyFunc(func(_ context.Context, _ Principal, collection schema.Collection, _ ReadAction) (Predicate, FieldSet, error) {
			switch collection.Name {
			case "expand_people":
				return AllowAllRecords(), ProjectFields("name"), nil
			case "expand_labels":
				labelPolicyCalls++
			}
			return AllowAllRecords(), AllFields(), nil
		})
		result, err := records.GetExpanded(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		owner := result.Expand["owner"].(map[string]any)
		if owner["name"] != "Ada" || owner["labels"] != nil || owner["expand"] != nil {
			t.Fatalf("hidden intermediate projection = %#v", owner)
		}
		if labelPolicyCalls != 0 {
			t.Fatalf("hidden child invoked leaf policy %d times", labelPolicyCalls)
		}
		if result.querySteps != 3 {
			t.Fatalf("hidden child query steps = %d, want base/root hydration + owner batch", result.querySteps)
		}
	})

	t.Run("visible leaf predicate filters before attachment", func(t *testing.T) {
		allowed, err := AllowRecordIDs(graph.firstLabel)
		if err != nil {
			t.Fatal(err)
		}
		records.policy = projectionPolicyFunc(func(_ context.Context, _ Principal, collection schema.Collection, _ ReadAction) (Predicate, FieldSet, error) {
			if collection.Name == "expand_labels" {
				return allowed, ProjectFields("name"), nil
			}
			return AllowAllRecords(), AllFields(), nil
		})
		result, err := records.GetExpanded(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		owner := result.Expand["owner"].(map[string]any)
		if !reflect.DeepEqual(owner["labels"], []string{graph.secondLabel.String(), graph.firstLabel.String()}) {
			t.Fatalf("leaf policy changed raw relation ids: %#v", owner["labels"])
		}
		labels := owner["expand"].(map[string]any)["labels"].([]map[string]any)
		if len(labels) != 1 || labels[0]["id"] != graph.firstLabel.String() || labels[0]["name"] != "systems" {
			t.Fatalf("policy-filtered nested labels = %#v", labels)
		}
		if result.querySteps != 5 {
			t.Fatalf("visible nested leaf query steps = %d, want 5", result.querySteps)
		}
	})
}

func TestNestedReverseThenForwardExpansionBatchesEveryHop(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	graph := createExpandGraph(t, ctx, pool, schemas, records, 3)

	result, err := records.GetExpanded(ctx, GetRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_people",
		ID:         graph.ada,
		Expand:     "expand_posts_via_owner.reviewers.labels",
	})
	if err != nil {
		t.Fatalf("nested reverse expansion: %v", err)
	}
	posts := result.Expand["expand_posts_via_owner"].([]map[string]any)
	if len(posts) != 3 {
		t.Fatalf("reverse posts = %#v", posts)
	}
	for _, post := range posts {
		if !reflect.DeepEqual(post["reviewers"], []string{graph.bob.String(), graph.ada.String()}) {
			t.Fatalf("reverse target raw reviewers = %#v", post["reviewers"])
		}
		reviewers := post["expand"].(map[string]any)["reviewers"].([]map[string]any)
		if len(reviewers) != 2 || reviewers[0]["id"] != graph.bob.String() || reviewers[1]["id"] != graph.ada.String() {
			t.Fatalf("nested reviewers = %#v", reviewers)
		}
		for _, reviewer := range reviewers {
			labels := reviewer["expand"].(map[string]any)["labels"].([]map[string]any)
			if len(labels) == 0 {
				t.Fatalf("nested labels missing from reviewer: %#v", reviewer)
			}
		}
	}
	if result.querySteps != 8 {
		t.Fatalf("reverse nested query steps = %d, want root 2 + reverse 3 + reviewers 2 + labels 1", result.querySteps)
	}
}

func TestEmptyReverseExpandSucceedsAtExactZeroRemainingRowBudget(t *testing.T) {
	_, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "zero_budget_parents",
		Fields: []schema.FieldInput{{
			Name: "links", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "zero_budget_parents", Cardinality: schema.RelationMultiple,
				MinSelect: expandInt(0), MaxSelect: expandInt(2), OnDelete: schema.RelationRestrict,
			},
		}},
	})
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "zero_budget_children",
		Fields: []schema.FieldInput{{
			Name: "parent", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "zero_budget_parents", Cardinality: schema.RelationSingle,
				OnDelete: schema.RelationRestrict,
			},
		}},
	})
	root, err := records.Create(ctx, "zero_budget_parents", map[string]json.RawMessage{})
	if err != nil {
		t.Fatal(err)
	}
	records.expandLimits.maxRows = 0

	result, err := records.GetExpanded(ctx, GetRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "zero_budget_parents",
		ID:         uuid.MustParse(root["id"].(string)),
		Expand:     "zero_budget_children_via_parent",
	})
	if err != nil {
		t.Fatalf("empty reverse at exact row boundary: %v", err)
	}
	children, ok := result.Expand["zero_budget_children_via_parent"].([]map[string]any)
	if !ok || len(children) != 0 {
		t.Fatalf("empty reverse value = %#v", result.Expand["zero_budget_children_via_parent"])
	}
	if result.querySteps != 3 {
		t.Fatalf("empty reverse query steps = %d, want root select/hydration + discovery", result.querySteps)
	}
}

func TestBranchingSelfMultiExpansionHitsByteLimitBeforeExponentialFlatten(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "branching_nodes",
		Fields: []schema.FieldInput{{
			Name: "links", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "branching_nodes", Cardinality: schema.RelationMultiple,
				MinSelect: expandInt(0), MaxSelect: expandInt(16), OnDelete: schema.RelationRestrict,
			},
		}},
	})
	ids := make([]uuid.UUID, 16)
	for index := range ids {
		record, err := records.Create(ctx, "branching_nodes", map[string]json.RawMessage{})
		if err != nil {
			t.Fatal(err)
		}
		ids[index] = uuid.MustParse(record["id"].(string))
	}
	edgeTable := schema.NewRelationPhysicalNames("branching_nodes", "links").MultiTable
	if _, err := pool.Exec(ctx, `insert into `+pgx.Identifier{"pgbase_relation", edgeTable}.Sanitize()+`
		(source_id, target_id, position)
		select sources.source_id, targets.target_id, (targets.ordinality - 1)::integer
		from unnest($1::uuid[]) as sources(source_id)
		cross join unnest($1::uuid[]) with ordinality as targets(target_id, ordinality)`, ids); err != nil {
		t.Fatal(err)
	}

	_, err := records.GetExpanded(ctx, GetRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "branching_nodes",
		ID:         ids[0],
		Expand:     "links.links.links.links.links.links",
	})
	assertValidationDetail(t, err, "response", "response_too_large")
}
