//go:build integration

package query

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestListRelationAwareFiltersAndSortsWithPrivateStableCursor(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	fixture := createRelationFilterSortFixture(t, ctx, pool, schemas, records)
	principal := Principal{Kind: PrincipalAdministrator, ID: uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")}

	assertIDs := func(name, filter, sortText string, want ...uuid.UUID) {
		t.Helper()
		page, err := records.ListRelationAware(ctx, RelationListRequest{
			Principal: principal, Collection: "relation_query_posts", Limit: 20, Filter: filter, Sort: sortText,
		})
		if err != nil {
			t.Fatalf("%s: ListRelationAware(): %v", name, err)
		}
		got := recordIDs(page.Items)
		wantStrings := make([]string, len(want))
		for index := range want {
			wantStrings[index] = want[index].String()
		}
		if !reflect.DeepEqual(got, wantStrings) {
			t.Fatalf("%s ids = %v, want %v", name, got, wantStrings)
		}
	}

	assertIDs("single equality", "owner = '"+fixture.ada.String()+"'", "", fixture.postAda)
	assertIDs("root lower", "title:lower = 'post-ada'", "", fixture.postAda)
	assertIDs("relation lower", "owner.name:lower = 'ada'", "", fixture.postAda)
	assertIDs("multiple membership", "reviewers = '"+fixture.ada.String()+"'", "", fixture.postBob)
	assertIDs("bounded nested path", "owner.manager.name = 'Ada'", "", fixture.postBob)
	assertIDs("filter and relation sort", "reviewers = '"+fixture.ada.String()+"'", "owner.name", fixture.postBob)
	assertIDs("depth-two relation sort", "", "owner.manager.name", fixture.postBob, fixture.postAda)

	first, err := records.ListRelationAware(ctx, RelationListRequest{
		Principal: principal, Collection: "relation_query_posts", Limit: 1, Sort: "owner.name,amount",
	})
	if err != nil {
		t.Fatalf("first relation sort page: %v", err)
	}
	if got := recordIDs(first.Items); !reflect.DeepEqual(got, []string{fixture.postAda.String()}) || first.NextCursor == "" {
		t.Fatalf("first relation sort page = ids:%v cursor:%q", got, first.NextCursor)
	}
	raw, err := base64.RawURLEncoding.DecodeString(first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("Ada")) {
		t.Fatalf("relation cursor disclosed target sort value: %q", raw)
	}

	second, err := records.ListRelationAware(ctx, RelationListRequest{
		Principal: principal, Collection: "relation_query_posts", Cursor: first.NextCursor, Limit: 1, Sort: "owner.name,amount",
	})
	if err != nil {
		t.Fatalf("second relation sort page: %v", err)
	}
	if got := recordIDs(second.Items); !reflect.DeepEqual(got, []string{fixture.postBob.String()}) {
		t.Fatalf("second relation sort page ids = %v", got)
	}

	if _, err := pool.Exec(ctx, `update app.relation_query_people set name = 'Zed' where id = $1`, fixture.ada); err != nil {
		t.Fatal(err)
	}
	_, err = records.ListRelationAware(ctx, RelationListRequest{
		Principal: principal, Collection: "relation_query_posts", Cursor: first.NextCursor, Limit: 1, Sort: "owner.name,amount",
	})
	if !IsInvalidCursor(err) {
		t.Fatalf("cursor after target sort mutation error = %v, want invalid cursor", err)
	}
}

func TestListContainsFilterMatchesLiteralTextAtRootAndRelationTerminal(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	fixture := createRelationFilterSortFixture(t, ctx, pool, schemas, records)
	principal := Principal{Kind: PrincipalAdministrator, ID: uuid.New()}

	root, err := records.ListRelationAware(ctx, RelationListRequest{
		Principal: principal, Collection: "relation_query_posts", Limit: 20, Filter: `title ~ 'bob'`, Sort: "title",
	})
	if err != nil || !reflect.DeepEqual(recordIDs(root.Items), []string{fixture.postBob.String()}) {
		t.Fatalf("root contains items=%v err=%v", recordIDs(root.Items), err)
	}
	relation, err := records.ListRelationAware(ctx, RelationListRequest{
		Principal: principal, Collection: "relation_query_posts", Limit: 20, Filter: `owner.name ~ 'Ad'`, Sort: "title",
	})
	if err != nil || !reflect.DeepEqual(recordIDs(relation.Items), []string{fixture.postAda.String()}) {
		t.Fatalf("relation contains items=%v err=%v", recordIDs(relation.Items), err)
	}
}

func TestListRelationAwareKeepsScalarListCursorExactlyCompatible(t *testing.T) {
	_, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name:   "relation_scalar_compat",
		Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText, Required: true, Indexed: true}},
	})
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if _, err := records.Create(ctx, "relation_scalar_compat", map[string]json.RawMessage{"name": json.RawMessage(`"` + name + `"`)}); err != nil {
			t.Fatal(err)
		}
	}
	legacy, err := records.listScalar(ctx, "relation_scalar_compat", "", 2, "name >= 'alpha'", "name")
	if err != nil {
		t.Fatal(err)
	}
	relationAware, err := records.ListRelationAware(ctx, RelationListRequest{
		Collection: "relation_scalar_compat", Limit: 2, Filter: "name >= 'alpha'", Sort: "name",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(legacy.Items, relationAware.Items) {
		t.Fatalf("scalar relation-aware items = %#v, legacy = %#v", relationAware.Items, legacy.Items)
	}
	assertEquivalentCursorIgnoringExpiry(t, records,
		mustCollectionID(t, schemas, "relation_scalar_compat"), relationAware.NextCursor, legacy.NextCursor)
}

func TestListExpandedCombinesRelationFilterSortWithNestedAndReverseExpansion(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	fixture := createRelationFilterSortFixture(t, ctx, pool, schemas, records)
	principal := Principal{Kind: PrincipalAdministrator, ID: uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")}

	filtered, err := records.ListExpanded(ctx, ListRequest{
		Principal: principal, Collection: "relation_query_posts", Limit: 10,
		Filter: "reviewers = '" + fixture.ada.String() + "'", Sort: "owner.name", Expand: "owner.manager",
	})
	if err != nil {
		t.Fatalf("relation-aware nested ListExpanded(): %v", err)
	}
	if got := expandedRootIDs(filtered.Items); !reflect.DeepEqual(got, []string{fixture.postBob.String()}) {
		t.Fatalf("relation-aware nested roots = %v", got)
	}
	owner, ok := filtered.Items[0].Expand["owner"].(map[string]any)
	if !ok || owner["id"] != fixture.bob.String() {
		t.Fatalf("expanded owner = %#v", filtered.Items[0].Expand["owner"])
	}
	nested, ok := owner["expand"].(map[string]any)
	if !ok {
		t.Fatalf("owner nested expand = %#v", owner["expand"])
	}
	manager, ok := nested["manager"].(map[string]any)
	if !ok || manager["id"] != fixture.ada.String() || manager["name"] != "Ada" {
		t.Fatalf("expanded owner.manager = %#v", nested["manager"])
	}

	reverse, err := records.ListExpanded(ctx, ListRequest{
		Principal: principal, Collection: "relation_query_people", Limit: 10,
		Filter: "manager = '" + fixture.ada.String() + "'", Sort: "name", Expand: "relation_query_posts_via_owner",
	})
	if err != nil {
		t.Fatalf("relation-aware reverse ListExpanded(): %v", err)
	}
	if got := expandedRootIDs(reverse.Items); !reflect.DeepEqual(got, []string{fixture.bob.String()}) {
		t.Fatalf("relation-aware reverse roots = %v", got)
	}
	posts, ok := reverse.Items[0].Expand["relation_query_posts_via_owner"].([]map[string]any)
	if !ok || !reflect.DeepEqual(recordIDs(posts), []string{fixture.postBob.String()}) {
		t.Fatalf("reverse posts = %#v", reverse.Items[0].Expand["relation_query_posts_via_owner"])
	}

	first, err := records.ListExpanded(ctx, ListRequest{
		Principal: principal, Collection: "relation_query_posts", Limit: 1,
		Sort: "owner.name", Expand: "owner.manager",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := expandedRootIDs(first.Items); !reflect.DeepEqual(got, []string{fixture.postAda.String()}) || first.NextCursor == "" {
		t.Fatalf("first expanded relation-sort page = ids:%v cursor:%q", got, first.NextCursor)
	}
	second, err := records.ListExpanded(ctx, ListRequest{
		Principal: principal, Collection: "relation_query_posts", Cursor: first.NextCursor, Limit: 1,
		Sort: "owner.name", Expand: "owner.manager",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := expandedRootIDs(second.Items); !reflect.DeepEqual(got, []string{fixture.postBob.String()}) {
		t.Fatalf("second expanded relation-sort page = %v", got)
	}
}

func TestRelationSortBudgetCoversDottedPathsOnlyAt100001(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "relation_sort_budget_targets",
		Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText, Required: true, Indexed: true},
		},
	})
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "relation_sort_budget_roots",
		Fields: []schema.FieldInput{
			{Name: "owner", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "relation_sort_budget_targets", Cardinality: schema.RelationSingle,
				OnDelete: schema.RelationRestrict,
			}},
		},
	})
	target, err := records.Create(ctx, "relation_sort_budget_targets", map[string]json.RawMessage{
		"name": json.RawMessage(`"same-value"`),
	})
	if err != nil {
		t.Fatal(err)
	}
	targetID := uuid.MustParse(target["id"].(string))
	table := pgx.Identifier{"app", "relation_sort_budget_roots"}.Sanitize()
	insertRange := func(first, last int) {
		t.Helper()
		_, err := pool.Exec(ctx, `INSERT INTO `+table+` (id, created_at, updated_at, owner)
			SELECT md5('relation-sort-root-' || value::text)::uuid, statement_timestamp(), statement_timestamp(), $1
			FROM generate_series($2::integer, $3::integer) AS generated(value)`, targetID, first, last)
		if err != nil {
			t.Fatal(err)
		}
		syncMembershipOrdinals(t, ctx, pool)
	}
	insertRange(1, maximumRelationSortRootRows)
	principal := Principal{Kind: PrincipalAdministrator, ID: uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")}
	for _, sortText := range []string{"owner.name", "owner"} {
		page, err := records.ListRelationAware(ctx, RelationListRequest{
			Principal: principal, Collection: "relation_sort_budget_roots", Limit: 1, Sort: sortText,
		})
		if err != nil {
			t.Fatalf("relation sort %q at budget: %v", sortText, err)
		}
		if len(page.Items) != 1 || page.NextCursor == "" {
			t.Fatalf("relation sort %q at budget page = %#v", sortText, page)
		}
	}

	// Past the budget only the dotted path is refused. The bare sort reads the
	// root's own indexed foreign-key column, so no collection size makes it
	// join or sort a target.
	insertRange(maximumRelationSortRootRows+1, maximumRelationSortRootRows+1)
	_, err = records.ListRelationAware(ctx, RelationListRequest{
		Principal: principal, Collection: "relation_sort_budget_roots", Limit: 1, Sort: "owner.name",
	})
	assertValidationDetail(t, err, "sort", "relation_sort_budget_exceeded")

	page, err := records.ListRelationAware(ctx, RelationListRequest{
		Principal: principal, Collection: "relation_sort_budget_roots", Limit: 1, Sort: "owner",
	})
	if err != nil {
		t.Fatalf("bare relation sort past the dotted budget: %v", err)
	}
	if len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("bare relation sort past the dotted budget page = %#v", page)
	}
}

type relationFilterSortFixture struct {
	ada, bob         uuid.UUID
	postAda, postBob uuid.UUID
}

func createRelationFilterSortFixture(
	t *testing.T,
	ctx context.Context,
	pool *database.Pool,
	schemas *schema.Service,
	records *Service,
) relationFilterSortFixture {
	t.Helper()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "relation_query_people",
		Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText, Required: true, Indexed: true},
			{Name: "manager", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "relation_query_people", Cardinality: schema.RelationSingle, OnDelete: schema.RelationRestrict,
			}},
		},
	})
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "relation_query_posts",
		Fields: []schema.FieldInput{
			{Name: "title", Type: schema.TypeText, Required: true, Indexed: true},
			{Name: "amount", Type: schema.TypeNumeric, Indexed: true},
			{Name: "owner", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "relation_query_people", Cardinality: schema.RelationSingle, OnDelete: schema.RelationRestrict,
			}},
			{Name: "reviewers", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "relation_query_people", Cardinality: schema.RelationMultiple,
				MinSelect: relationFilterInt(0), MaxSelect: relationFilterInt(4), OnDelete: schema.RelationRestrict,
			}},
		},
	})
	adaRecord, err := records.Create(ctx, "relation_query_people", map[string]json.RawMessage{"name": json.RawMessage(`"Ada"`)})
	if err != nil {
		t.Fatal(err)
	}
	ada := uuid.MustParse(adaRecord["id"].(string))
	bobRecord, err := records.Create(ctx, "relation_query_people", map[string]json.RawMessage{
		"name": json.RawMessage(`"Bob"`), "manager": relationFilterUUIDJSON(ada),
	})
	if err != nil {
		t.Fatal(err)
	}
	bob := uuid.MustParse(bobRecord["id"].(string))
	postAdaRecord, err := records.Create(ctx, "relation_query_posts", map[string]json.RawMessage{
		"title": json.RawMessage(`"post-ada"`), "amount": json.RawMessage(`"0.00"`), "owner": relationFilterUUIDJSON(ada),
	})
	if err != nil {
		t.Fatal(err)
	}
	postBobRecord, err := records.Create(ctx, "relation_query_posts", map[string]json.RawMessage{
		"title": json.RawMessage(`"post-bob"`), "amount": json.RawMessage(`"0.0"`), "owner": relationFilterUUIDJSON(bob),
	})
	if err != nil {
		t.Fatal(err)
	}
	postAda := uuid.MustParse(postAdaRecord["id"].(string))
	postBob := uuid.MustParse(postBobRecord["id"].(string))

	edge := schema.NewRelationPhysicalNames("relation_query_posts", "reviewers").MultiTable
	statement := `insert into ` + pgx.Identifier{"pgbase_relation", edge}.Sanitize() +
		` (source_id, target_id, position) values ($1, $2, 0), ($3, $4, 0)`
	if _, err := pool.Exec(ctx, statement, postAda, bob, postBob, ada); err != nil {
		t.Fatal(err)
	}
	return relationFilterSortFixture{ada: ada, bob: bob, postAda: postAda, postBob: postBob}
}

func relationFilterUUIDJSON(id uuid.UUID) json.RawMessage {
	encoded, _ := json.Marshal(id.String())
	return encoded
}

func relationFilterInt(value int) *int { return &value }
