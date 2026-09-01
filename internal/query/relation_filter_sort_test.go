package query

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

type relationFilterSortPolicyFunc func(context.Context, Principal, schema.Collection, ReadAction) (Predicate, FieldSet, error)

func (f relationFilterSortPolicyFunc) ReadScope(
	ctx context.Context,
	principal Principal,
	collection schema.Collection,
	action ReadAction,
) (Predicate, FieldSet, error) {
	return f(ctx, principal, collection, action)
}

func TestCompileRelationListSingleRelationEqualityUsesVisibleTargetExists(t *testing.T) {
	t.Parallel()

	catalog, posts := relationFilterSortCatalog()
	targetID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	filter := mustParseFilter(t, "owner = '"+targetID.String()+"'")
	plan, err := compileRelationListPlan(
		context.Background(), catalog, posts,
		Principal{Kind: PrincipalAdministrator, ID: uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")},
		AdminAllowAll{}, filter, effectiveSortKeys(nil),
	)
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

func TestCompileRelationListMultipleMembershipAndBoundedPathPolicy(t *testing.T) {
	t.Parallel()

	catalog, posts := relationFilterSortCatalog()
	targetID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	var actions []ReadAction
	policy := relationFilterSortPolicyFunc(func(_ context.Context, _ Principal, _ schema.Collection, action ReadAction) (Predicate, FieldSet, error) {
		actions = append(actions, action)
		return AllowAllRecords(), ProjectFields("name", "manager"), nil
	})
	filter := mustParseFilter(t, "reviewers = '"+targetID.String()+"' && owner.manager.name = 'Ada'")
	plan, err := compileRelationListPlan(context.Background(), catalog, posts, Principal{}, policy, filter, effectiveSortKeys(nil))
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
	if len(actions) != 3 {
		t.Fatalf("policy actions = %v, want one typed filter action per hop", actions)
	}
	for _, action := range actions {
		if action != ReadActionRelationFilter {
			t.Fatalf("policy action = %q, want %q", action, ReadActionRelationFilter)
		}
	}
}

func TestCompileRelationListRejectsPathProjectionAndStaticBudgetsBeforePolicy(t *testing.T) {
	t.Parallel()

	catalog, posts := relationFilterSortCatalog()
	missingProjection := relationFilterSortPolicyFunc(func(context.Context, Principal, schema.Collection, ReadAction) (Predicate, FieldSet, error) {
		return AllowAllRecords(), ProjectFields("manager"), nil
	})
	_, err := compileRelationListPlan(context.Background(), catalog, posts, Principal{}, missingProjection,
		mustParseFilter(t, "owner.name = 'Ada'"), effectiveSortKeys(nil))
	if !errors.Is(err, ErrProjectionDenied) {
		t.Fatalf("missing terminal projection error = %v, want projection denied", err)
	}

	policyCalls := 0
	policy := relationFilterSortPolicyFunc(func(context.Context, Principal, schema.Collection, ReadAction) (Predicate, FieldSet, error) {
		policyCalls++
		return AllowAllRecords(), AllFields(), nil
	})
	_, err = compileRelationListPlan(context.Background(), catalog, posts, Principal{}, policy,
		mustParseFilter(t, "owner.manager.manager.manager.name = 'Ada'"), effectiveSortKeys(nil))
	assertValidationDetail(t, err, "filter", "relation_filter_unsupported")
	if policyCalls != 0 {
		t.Fatalf("over-depth request called policy %d times before rejection", policyCalls)
	}

	parts := make([]string, 9)
	for index := range parts {
		parts[index] = "owner.name = 'Ada'"
	}
	_, err = compileRelationListPlan(context.Background(), catalog, posts, Principal{}, policy,
		mustParseFilter(t, strings.Join(parts, " && ")), effectiveSortKeys(nil))
	assertValidationDetail(t, err, "filter", "relation_filter_unsupported")
	if policyCalls != 0 {
		t.Fatalf("over-node request called policy %d times before rejection", policyCalls)
	}
}

func TestCompileRelationListSinglePathSortIsPrivateAndMultiSortIsStableError(t *testing.T) {
	t.Parallel()

	catalog, posts := relationFilterSortCatalog()
	var actions []ReadAction
	policy := relationFilterSortPolicyFunc(func(_ context.Context, _ Principal, _ schema.Collection, action ReadAction) (Predicate, FieldSet, error) {
		actions = append(actions, action)
		return AllowAllRecords(), ProjectFields("name"), nil
	})
	requested, err := parseSort("owner.name")
	if err != nil {
		t.Fatalf("parseSort(owner.name) error = %v", err)
	}
	plan, err := compileRelationListPlan(context.Background(), catalog, posts, Principal{}, policy, nil, effectiveSortKeys(requested))
	if err != nil {
		t.Fatalf("compileRelationListPlan() error = %v", err)
	}
	if !plan.privateCursor {
		t.Fatal("relation sort did not require a private cursor")
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
	after := PageCursor{
		Version: cursorVersion, CollectionID: posts.ID, Sort: relationPlanSortKeys(plan),
		Values:   []json.RawMessage{json.RawMessage(`"Ada"`), json.RawMessage(`"99999999-9999-4999-8999-999999999999"`)},
		AnchorID: uuid.MustParse("99999999-9999-4999-8999-999999999999"),
	}
	continued, err := plan.statement(&after, 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(continued.SQL, `(SELECT`) || strings.Count(continued.SQL, `LEFT JOIN "app"."relation_people"`) != 1 ||
		strings.Count(continued.SQL, `"rq_sort_1"."name"`) < 4 {
		t.Fatalf("relation sort continuation did not reuse one joined value: %q", continued.SQL)
	}
	anchor, err := plan.anchorStatement(after.AnchorID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(anchor.SQL, `(SELECT`) || strings.Count(anchor.SQL, `LEFT JOIN "app"."relation_people"`) != 1 {
		t.Fatalf("relation sort anchor retained a correlated or duplicate target lookup: %q", anchor.SQL)
	}
	if len(actions) != 1 || actions[0] != ReadActionRelationSort {
		t.Fatalf("sort policy actions = %v", actions)
	}

	multi, err := parseSort("reviewers.name")
	if err != nil {
		t.Fatalf("parseSort(reviewers.name) error = %v", err)
	}
	_, err = compileRelationListPlan(context.Background(), catalog, posts, Principal{}, policy, nil, effectiveSortKeys(multi))
	assertValidationDetail(t, err, "sort", "relation_sort_unsupported")
}

func TestBareRelationSortCurrentlyJoinsTargetAndRequiresPrivateCursor(t *testing.T) {
	t.Parallel()

	catalog, posts := relationFilterSortCatalog()
	requested, err := parseSort("owner")
	if err != nil {
		t.Fatalf("parseSort(owner) error = %v", err)
	}
	plan, err := compileRelationListPlan(
		context.Background(), catalog, posts,
		Principal{Kind: PrincipalAdministrator, ID: uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")},
		AdminAllowAll{}, nil, effectiveSortKeys(requested),
	)
	if err != nil {
		t.Fatalf("compileRelationListPlan(owner) error = %v", err)
	}
	if !plan.privateCursor || !relationListPlanUsesRelation(plan) {
		t.Fatalf("bare relation plan private/uses-relation = %t/%t, want true/true", plan.privateCursor, relationListPlanUsesRelation(plan))
	}
	statement, err := plan.statement(nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`LEFT JOIN "app"."relation_people" AS "rq_sort_1" ON "rq_sort_1"."id" = "rq_root"."owner"`,
		`SELECT "rq_root"."id", "rq_root"."created_at", "rq_root"."updated_at", "rq_root"."owner", "rq_sort_1"."id"`,
		`ORDER BY "rq_sort_1"."id" ASC NULLS LAST`,
	} {
		if !strings.Contains(statement.SQL, fragment) {
			t.Fatalf("bare relation sort SQL = %q, want current join fragment %q", statement.SQL, fragment)
		}
	}
	if maximumRelationSortRootRows != 100_000 {
		t.Fatalf("bare relation sort root budget = %d, want current 100000", maximumRelationSortRootRows)
	}
}

func TestRelationSortCursorIsAnchorOnlyAndKeyed(t *testing.T) {
	t.Parallel()

	collectionID := uuid.MustParse("88888888-8888-4888-8888-888888888888")
	anchorID := uuid.MustParse("99999999-9999-4999-8999-999999999999")
	values := []json.RawMessage{json.RawMessage(`"Ada"`), json.RawMessage(`"` + anchorID.String() + `"`)}
	cursor := PageCursor{
		Version: cursorVersion, CollectionID: collectionID,
		Sort:   []SortKey{{Field: "owner.name"}, {Field: systemFieldID}},
		Values: values, AnchorID: anchorID, PrivateValues: true,
		ExpiresAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
	codec := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	token, err := codec.Encode(cursor)
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
	wantHash, err := codec.privateCursorValuesHash(values)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.AnchorHash, wantHash) {
		t.Fatalf("private anchor hash = %x, want %x", decoded.AnchorHash, wantHash)
	}
	otherHash, err := NewCursorCodec([]byte("abcdef0123456789abcdef0123456789")).privateCursorValuesHash(values)
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
	}}, posts
}
