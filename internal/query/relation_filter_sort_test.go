package query

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/cursor"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestCompileRelationListSingleRelationEqualityUsesVisibleTargetExists(t *testing.T) {
	t.Parallel()

	catalog, posts := relationFilterSortCatalog()
	targetID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	filter := mustParseFilter(t, "owner = '"+targetID.String()+"'")
	plan, err := compileRelationListPlan(catalog, posts, filter, effectiveSortKeys(nil))
	if err != nil {
		t.Fatalf("compileRelationListPlan() error = %v", err)
	}
	statement, err := plan.statement(nil, 10)
	if err != nil {
		t.Fatalf("statement() error = %v", err)
	}
	for _, fragment := range []string{
		`EXISTS (SELECT 1 FROM "app"."relation_people" AS`,
		`"id" = "rq_root"."owner"`,
		`"id" = $1`,
		`ORDER BY "rq_root"."created_at" ASC NULLS LAST, "rq_root"."id" ASC NULLS LAST`,
	} {
		if !strings.Contains(statement.SQL, fragment) {
			t.Fatalf("relation filter SQL = %q, want fragment %q", statement.SQL, fragment)
		}
	}
	if len(statement.Args) != 2 || statement.Args[0] != targetID || statement.Args[1] != 11 {
		t.Fatalf("relation filter args = %#v, want UUID and bounded fetch limit", statement.Args)
	}
	if strings.Contains(statement.SQL, targetID.String()) {
		t.Fatalf("relation filter SQL embedded caller UUID: %q", statement.SQL)
	}
}

func TestCompileRelationListMultipleMembershipAndBoundedPath(t *testing.T) {
	t.Parallel()

	catalog, posts := relationFilterSortCatalog()
	targetID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	filter := mustParseFilter(t, "reviewers = '"+targetID.String()+"' && owner.manager.name = 'Ada'")
	plan, err := compileRelationListPlan(catalog, posts, filter, effectiveSortKeys(nil))
	if err != nil {
		t.Fatalf("compileRelationListPlan() error = %v", err)
	}
	statement, err := plan.statement(nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	edge := schema.NewRelationPhysicalNames(posts.TableName, "reviewers").MultiTable
	for _, fragment := range []string{
		`FROM "pgbase_relation"."` + edge + `" AS`,
		`JOIN "app"."relation_people" AS`,
		`"target_id"`,
		`"name" = $2`,
	} {
		if !strings.Contains(statement.SQL, fragment) {
			t.Fatalf("relation path SQL = %q, want fragment %q", statement.SQL, fragment)
		}
	}
}

func TestCompileRelationListReverseRelationsUseIncomingCorrelation(t *testing.T) {
	t.Parallel()
	catalog, _ := relationFilterSortCatalog()
	people := catalog.Collections["relation_people"]
	for _, testCase := range []struct {
		filter    string
		fragments []string
	}{
		{filter: "relation_posts_via_owner.owner = '11111111-1111-4111-8111-111111111111'", fragments: []string{`FROM "app"."relation_posts" AS`, `"owner" = "rq_root"."id"`}},
		{filter: "relation_posts_via_reviewers.owner = '11111111-1111-4111-8111-111111111111'", fragments: []string{`FROM "pgbase_relation".`, `"source_id"`, `"target_id" = "rq_root"."id"`}},
	} {
		filter := mustParseFilter(t, testCase.filter)
		plan, err := compileRelationListPlan(catalog, people, filter, effectiveSortKeys(nil))
		if err != nil {
			t.Fatalf("compile %q: %v", testCase.filter, err)
		}
		statement, err := plan.statement(nil, 10)
		if err != nil {
			t.Fatalf("render %q: %v", testCase.filter, err)
		}
		for _, fragment := range testCase.fragments {
			if !strings.Contains(statement.SQL, fragment) {
				t.Fatalf("reverse SQL %q, want %q", statement.SQL, fragment)
			}
		}
	}
}

func TestCompileRelationListRejectsStaticBudgets(t *testing.T) {
	t.Parallel()

	catalog, posts := relationFilterSortCatalog()
	_, err := compileRelationListPlan(catalog, posts,
		mustParseFilter(t, "owner.manager.manager.manager.name = 'Ada'"), effectiveSortKeys(nil))
	assertValidationDetail(t, err, "filter", "relation_filter_unsupported")

	parts := make([]string, 9)
	for index := range parts {
		parts[index] = "owner.name = 'Ada'"
	}
	_, err = compileRelationListPlan(catalog, posts,
		mustParseFilter(t, strings.Join(parts, " && ")), effectiveSortKeys(nil))
	assertValidationDetail(t, err, "filter", "relation_filter_unsupported")
}

func TestCompileRelationListSinglePathSortIsPrivateAndMultiSortIsStableError(t *testing.T) {
	t.Parallel()

	catalog, posts := relationFilterSortCatalog()
	requested, err := parseSort("owner.name")
	if err != nil {
		t.Fatalf("parseSort(owner.name) error = %v", err)
	}
	plan, err := compileRelationListPlan(catalog, posts, nil, effectiveSortKeys(requested))
	if err != nil {
		t.Fatalf("compileRelationListPlan() error = %v", err)
	}
	if !plan.order.private() {
		t.Fatal("relation sort did not require a private cursor")
	}
	if plan.order.terms[0].indexHint != orderIndexRelationTarget {
		t.Fatalf("relation sort index hint = %v, want target index", plan.order.terms[0].indexHint)
	}
	statement, err := plan.statement(nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`LEFT JOIN "app"."relation_people" AS "rq_sort_1" ON "rq_sort_1"."id" = "rq_root"."owner"`,
		`SELECT "rq_root"."id", "rq_root"."created_at", "rq_root"."updated_at", "rq_root"."owner", "rq_sort_1"."name"`,
		`ORDER BY "rq_sort_1"."name" ASC NULLS LAST`,
		`"rq_root"."id" ASC NULLS LAST`,
	} {
		if !strings.Contains(statement.SQL, fragment) {
			t.Fatalf("relation sort SQL = %q, want fragment %q", statement.SQL, fragment)
		}
	}
	if strings.Contains(statement.SQL, `(SELECT`) || strings.Count(statement.SQL, `LEFT JOIN "app"."relation_people"`) != 1 {
		t.Fatalf("relation sort retained a correlated or duplicate target lookup: %q", statement.SQL)
	}
	after := pageBoundary{Values: []json.RawMessage{
		json.RawMessage(`"Ada"`), json.RawMessage(`"99999999-9999-4999-8999-999999999999"`),
	}}
	continued, err := plan.statement(&after, 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(continued.SQL, `(SELECT`) || strings.Count(continued.SQL, `LEFT JOIN "app"."relation_people"`) != 1 ||
		strings.Count(continued.SQL, `"rq_sort_1"."name"`) < 4 {
		t.Fatalf("relation sort continuation did not reuse one joined value: %q", continued.SQL)
	}
	anchor, err := plan.order.anchorStatement(uuid.MustParse("99999999-9999-4999-8999-999999999999"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(anchor.SQL, `(SELECT`) || strings.Count(anchor.SQL, `LEFT JOIN "app"."relation_people"`) != 1 {
		t.Fatalf("relation sort anchor retained a correlated or duplicate target lookup: %q", anchor.SQL)
	}
	multi, err := parseSort("reviewers.name")
	if err != nil {
		t.Fatalf("parseSort(reviewers.name) error = %v", err)
	}
	multiPlan, err := compileRelationListPlan(catalog, posts, nil, effectiveSortKeys(multi))
	if err != nil {
		t.Fatal(err)
	}
	multiStatement, err := multiPlan.statement(nil, 2)
	if err != nil || !strings.Contains(multiStatement.SQL, "LEFT JOIN LATERAL") || !strings.Contains(multiStatement.SQL, `"pgbase_relation"`) || !strings.Contains(multiStatement.SQL, `"sort_value"`) {
		t.Fatalf("multi relation sort SQL=%q err=%v", multiStatement.SQL, err)
	}
}

func TestOrderSpecV5DescriptorUsesStableFieldIDsAndExplicitSemantics(t *testing.T) {
	t.Parallel()

	catalog, posts := relationFilterSortCatalog()
	requested, err := parseSort("owner.name")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := compileRelationListPlan(catalog, posts, nil, effectiveSortKeys(requested))
	if err != nil {
		t.Fatal(err)
	}
	got, err := plan.order.cursorV5Order()
	if err != nil {
		t.Fatalf("cursorV5Order() error = %v", err)
	}
	want := []cursor.V5OrderTerm{
		{
			Path: []string{
				"55555555-5555-4555-8555-555555555555",
				"44444444-4444-4444-8444-444444444444",
			},
			Type: "text", Nulls: cursor.NullsLast,
		},
		{Path: []string{"$id"}, Type: "uuid", Nulls: cursor.NullsLast},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("v5 order descriptor = %#v, want %#v", got, want)
	}
}

func TestOrderSpecV5DescriptorPreservesAcceptedExplicitIDBeforeFinalTiebreaker(t *testing.T) {
	t.Parallel()

	catalog, posts := relationFilterSortCatalog()
	requested, err := parseSort("id,owner.name")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := compileRelationListPlan(catalog, posts, nil, effectiveSortKeys(requested))
	if err != nil {
		t.Fatal(err)
	}
	got, err := plan.order.cursorV5Order()
	if err != nil {
		t.Fatalf("cursorV5Order(explicit id) error = %v", err)
	}
	wantPaths := [][]string{
		{"$id"},
		{"55555555-5555-4555-8555-555555555555", "44444444-4444-4444-8444-444444444444"},
		{"$id"},
	}
	for index, want := range wantPaths {
		if !reflect.DeepEqual(got[index].Path, want) {
			t.Fatalf("v5 order path %d = %v, want %v", index, got[index].Path, want)
		}
	}
}

// A single relation column stores the target's id under a deferred foreign key,
// so ordering by the joined target.id is ordering by the root column itself.
// The plan therefore stays on the root table: no target join, no private
// cursor, and none of the dotted path's target-side budget.
func TestBareRelationSortOrdersOnTheRootForeignKeyWithoutJoiningTheTarget(t *testing.T) {
	t.Parallel()

	catalog, posts := relationFilterSortCatalog()
	requested, err := parseSort("owner")
	if err != nil {
		t.Fatalf("parseSort(owner) error = %v", err)
	}
	plan, err := compileRelationListPlan(catalog, posts, nil, effectiveSortKeys(requested))
	if err != nil {
		t.Fatalf("compileRelationListPlan(owner) error = %v", err)
	}
	if plan.order.private() || relationListPlanUsesRelation(plan) {
		t.Fatalf("bare relation plan private/uses-relation = %t/%t, want false/false",
			plan.order.private(), relationListPlanUsesRelation(plan))
	}
	if hint := plan.order.terms[0].indexHint; hint != orderIndexRoot {
		t.Fatalf("bare relation sort index hint = %v, want the root index", hint)
	}

	// The route production takes: no relation SQL is required, so the scalar
	// page compiler renders the unaliased root statement.
	statement, err := plannedRootPlan(nil, plan).pageStatement(nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	wantSQL := `SELECT "id", "created_at", "updated_at", "owner" FROM "app"."relation_posts" ` +
		`ORDER BY "owner" ASC NULLS LAST, "id" ASC NULLS LAST LIMIT $1`
	if statement.SQL != wantSQL {
		t.Fatalf("bare relation sort SQL =\n%q\nwant\n%q", statement.SQL, wantSQL)
	}
	for _, absent := range []string{"LEFT JOIN", relationRootAlias, "rq_sort"} {
		if strings.Contains(statement.SQL, absent) {
			t.Fatalf("bare relation sort SQL = %q, want no %q", statement.SQL, absent)
		}
	}

	// The term keeps the relation field's identity but describes the uuid the
	// column physically is, so the cursor decodes a uuid and the v5 descriptor
	// encodes "uuid" rather than "relation".
	cursorField := plan.order.terms[0].cursorField
	ownerField, _ := filterField(posts, "owner")
	if cursorField.ID != ownerField.ID || cursorField.ColumnName != "owner" || cursorField.Type != schema.TypeUUID {
		t.Fatalf("bare relation cursor field = %+v, want owner's id/column with uuid type", cursorField)
	}
	descriptor, err := plan.order.cursorV5Order()
	if err != nil {
		t.Fatalf("cursorV5Order(owner) error = %v", err)
	}
	if !reflect.DeepEqual(descriptor[0].Path, []string{ownerField.ID.String()}) ||
		descriptor[0].Type != string(schema.TypeUUID) {
		t.Fatalf("bare relation v5 term = %+v, want path [%s] typed uuid", descriptor[0], ownerField.ID)
	}

	// Only the dotted path reads a target, so only it carries the root budget.
	dotted, err := parseSort("owner.name")
	if err != nil {
		t.Fatalf("parseSort(owner.name) error = %v", err)
	}
	dottedPlan, err := compileRelationListPlan(catalog, posts, nil, effectiveSortKeys(dotted))
	if err != nil {
		t.Fatal(err)
	}
	if !dottedPlan.order.private() || maximumRelationSortRootRows != 100_000 {
		t.Fatalf("dotted sort private = %t, budget = %d, want true/100000",
			dottedPlan.order.private(), maximumRelationSortRootRows)
	}

	// Multi cardinality has no single ordering value and keeps its stable error.
	multi, err := parseSort("reviewers")
	if err != nil {
		t.Fatalf("parseSort(reviewers) error = %v", err)
	}
	_, err = compileRelationListPlan(catalog, posts, nil, effectiveSortKeys(multi))
	assertValidationDetail(t, err, "sort", "relation_sort_unsupported")
}

func TestRelationSortCursorIsAnchorOnlyAndKeyed(t *testing.T) {
	t.Parallel()

	collectionID := uuid.MustParse("88888888-8888-4888-8888-888888888888")
	anchorID := uuid.MustParse("99999999-9999-4999-8999-999999999999")
	values := []json.RawMessage{json.RawMessage(`"Ada"`), json.RawMessage(`"` + anchorID.String() + `"`)}
	page := cursor.Page{
		Version: cursor.Version5, CollectionID: collectionID,
		Values: values, AnchorID: anchorID, PrivateValues: true,
		ExpiresAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		V5: &cursor.V5Descriptor{
			CatalogVersion: 12, MembershipGeneration: 1,
			QueryHash: bytes.Repeat([]byte{0x11}, sha256.Size),
			ScopeHash: bytes.Repeat([]byte{0x22}, sha256.Size), HighWater: 9,
			Order: []cursor.V5OrderTerm{
				{
					Path:  []string{"77777777-7777-4777-8777-777777777777", "66666666-6666-4666-8666-666666666666"},
					Type:  "text",
					Nulls: cursor.NullsLast,
				},
				{Path: []string{"$id"}, Type: "uuid", Nulls: cursor.NullsLast},
			},
		},
	}
	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	token, err := codec.Encode(page)
	if err != nil {
		t.Fatalf("Encode(private cursor) error = %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("Ada")) {
		t.Fatalf("private cursor disclosed relation sort value: %q", raw)
	}
	decoded, err := codec.Decode(token, collectionID, time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Decode(private cursor) error = %v", err)
	}
	if !decoded.PrivateValues || len(decoded.Values) != 0 || decoded.AnchorID != anchorID {
		t.Fatalf("decoded private cursor = %#v", decoded)
	}
	wantHash, err := codec.ValuesHash(values)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.AnchorHash, wantHash) {
		t.Fatalf("private anchor hash = %x, want %x", decoded.AnchorHash, wantHash)
	}
	otherHash, err := cursor.NewCodec([]byte("abcdef0123456789abcdef0123456789")).ValuesHash(values)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(wantHash, otherHash) {
		t.Fatal("private anchor hash was not keyed by the cursor secret")
	}
}

func relationFilterSortCatalog() (schema.Catalog, schema.Collection) {
	peopleID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	postsID := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	people := schema.Collection{
		ID: peopleID, Name: "relation_people", TableName: "relation_people",
		Fields: []schema.Field{
			{ID: uuid.MustParse("44444444-4444-4444-8444-444444444444"), Name: "name", ColumnName: "name", Type: schema.TypeText, Indexed: true, Ordinal: 0},
			{ID: uuid.MustParse("66666666-6666-4666-8666-666666666666"), Name: "manager", ColumnName: "manager", Type: schema.TypeRelation, Indexed: true, Ordinal: 1, Relation: &schema.RelationSpec{
				TargetCollectionID: peopleID, Cardinality: schema.RelationSingle,
				MinSelect: 0, MaxSelect: 1, OnDelete: schema.RelationRestrict,
				ValueCodecVersion: schema.RelationValueCodecVersion,
				Storage:           schema.RelationStorage{Kind: schema.RelationStorageInlineFKV1, Name: "manager"},
			}},
		},
	}
	posts := schema.Collection{
		ID: postsID, Name: "relation_posts", TableName: "relation_posts",
		Fields: []schema.Field{
			{ID: uuid.MustParse("55555555-5555-4555-8555-555555555555"), Name: "owner", ColumnName: "owner", Type: schema.TypeRelation, Indexed: true, Ordinal: 0, Relation: &schema.RelationSpec{
				TargetCollectionID: peopleID, Cardinality: schema.RelationSingle,
				MinSelect: 0, MaxSelect: 1, OnDelete: schema.RelationRestrict,
				ValueCodecVersion: schema.RelationValueCodecVersion,
				Storage:           schema.RelationStorage{Kind: schema.RelationStorageInlineFKV1, Name: "owner"},
			}},
			{ID: uuid.MustParse("77777777-7777-4777-8777-777777777777"), Name: "reviewers", ColumnName: "reviewers", Type: schema.TypeRelation, Indexed: true, Ordinal: 1, Relation: &schema.RelationSpec{
				TargetCollectionID: peopleID, Cardinality: schema.RelationMultiple,
				MinSelect: 0, MaxSelect: 10, OnDelete: schema.RelationRestrict,
				ValueCodecVersion: schema.RelationValueCodecVersion,
				Storage: schema.RelationStorage{
					Kind: schema.RelationStorageEdgeTableV1,
					Name: schema.NewRelationPhysicalNames("relation_posts", "reviewers").MultiTable,
				},
			}},
		},
	}
	return schema.Catalog{Collections: map[string]schema.Collection{
		people.Name: people,
		posts.Name:  posts,
	}, Incoming: map[uuid.UUID][]schema.RelationRef{peopleID: {
		{SourceCollectionID: postsID, SourceFieldID: posts.Fields[0].ID},
		{SourceCollectionID: postsID, SourceFieldID: posts.Fields[1].ID},
	}}}, posts
}
