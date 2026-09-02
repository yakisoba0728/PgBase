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
	"github.com/webisnoteasy/PgBase/internal/cursor"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/membership"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

var expandTestAdministratorID = uuid.MustParse("019551d0-e9c0-7000-8000-000000000001")

func TestGetExpandedReturnsSingleTargetApartFromRoot(t *testing.T) {
	_, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "expand_people",
		Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText, Required: true},
		},
	})
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "expand_posts",
		Fields: []schema.FieldInput{
			{Name: "title", Type: schema.TypeText, Required: true},
			{Name: "owner", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "expand_people", Cardinality: schema.RelationSingle,
				OnDelete: schema.RelationRestrict,
			}},
		},
	})
	target, err := records.Create(ctx, "expand_people", map[string]json.RawMessage{"name": json.RawMessage(`"Ada"`)})
	if err != nil {
		t.Fatal(err)
	}
	targetID := uuid.MustParse(target["id"].(string))
	root, err := records.Create(ctx, "expand_posts", map[string]json.RawMessage{
		"title": json.RawMessage(`"Compiler"`),
		"owner": json.RawMessage(`"` + targetID.String() + `"`),
	})
	if err != nil {
		t.Fatal(err)
	}

	expanded, err := records.GetExpanded(ctx, GetRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_posts", ID: uuid.MustParse(root["id"].(string)), Expand: "owner",
	})
	if err != nil {
		t.Fatalf("GetExpanded(): %v", err)
	}
	if expanded.Root["owner"] != targetID.String() {
		t.Fatalf("root owner = %#v, want raw id %s", expanded.Root["owner"], targetID)
	}
	owner, ok := expanded.Expand["owner"].(map[string]any)
	if !ok || owner["name"] != "Ada" || owner["id"] != targetID.String() {
		t.Fatalf("expanded owner = %#v", expanded.Expand["owner"])
	}
	if _, found := expanded.Root["expand"]; found {
		t.Fatal("expand data mutated the root record")
	}
	encoded, err := json.Marshal(expanded)
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(encoded, &response); err != nil {
		t.Fatal(err)
	}
	if response["owner"] != targetID.String() || response["expand"] == nil {
		t.Fatalf("expanded JSON = %s", encoded)
	}
}

func TestListExpandedBatchesSingleAndMultipleTargetsWithRawRelationsAndStableCursor(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	graph := createExpandGraph(t, ctx, pool, schemas, records, 3)

	// A v5 cursor binds the principal scope digest, so the plain baseline has
	// to read as the same principal for "both readers issue the same root
	// cursor" to still be the property under test.
	base, err := records.List(ctx, ListRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_posts", Limit: 2,
	})
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	expanded, err := records.ListExpanded(ctx, ListRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_posts", Limit: 2, Expand: "owner,reviewers",
	})
	if err != nil {
		t.Fatalf("ListExpanded(): %v", err)
	}
	if expanded.NextCursor == "" {
		t.Fatal("expanded page did not issue a root cursor")
	}
	assertEquivalentCursorIgnoringExpiry(t, records,
		mustCollectionID(t, schemas, "expand_posts"), expanded.NextCursor, base.NextCursor)
	if len(expanded.Items) != 2 || expanded.querySteps != 6 {
		t.Fatalf("expanded page items/steps = %d/%d, want 2/6", len(expanded.Items), expanded.querySteps)
	}
	for _, item := range expanded.Items {
		if !reflect.DeepEqual(item.Root["reviewers"], []string{graph.bob.String(), graph.ada.String()}) {
			t.Fatalf("raw reviewers = %#v", item.Root["reviewers"])
		}
		owner := item.Expand["owner"].(map[string]any)
		if !reflect.DeepEqual(owner["labels"], []string{graph.secondLabel.String(), graph.firstLabel.String()}) {
			t.Fatalf("expanded owner raw labels = %#v", owner["labels"])
		}
		reviewers := item.Expand["reviewers"].([]map[string]any)
		if len(reviewers) != 2 || reviewers[0]["id"] != graph.bob.String() || reviewers[1]["id"] != graph.ada.String() {
			t.Fatalf("ordered reviewers = %#v", reviewers)
		}
		if !reflect.DeepEqual(reviewers[0]["labels"], []string{graph.firstLabel.String()}) {
			t.Fatalf("expanded reviewer raw labels = %#v", reviewers[0]["labels"])
		}
	}

	one, err := records.ListExpanded(ctx, ListRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_posts", Limit: 1, Expand: "owner,reviewers",
	})
	if err != nil {
		t.Fatal(err)
	}
	if one.querySteps != expanded.querySteps {
		t.Fatalf("query steps changed with root count: one=%d two=%d", one.querySteps, expanded.querySteps)
	}
}

func TestGetExpandedBoundsHydratedEdgeRows(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	createExpandGraph(t, ctx, pool, schemas, records, 1)
	page, err := records.listScalar(ctx, "expand_posts", "", 1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	records.expandLimits.maxRows = 1

	_, err = records.GetExpanded(ctx, GetRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_posts",
		ID:         uuid.MustParse(page.Items[0]["id"].(string)),
		Expand:     "owner",
	})
	assertValidationDetail(t, err, "expand", "expand_budget_exceeded")
}

func TestGetExpandedPrechargesTargetQueryStepsBeforeHydration(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	createExpandGraph(t, ctx, pool, schemas, records, 1)
	page, err := records.listScalar(ctx, "expand_posts", "", 1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	// Base SELECT + root reviewers hydration consume two steps. Expanding owner
	// requires the target SELECT and its labels hydration atomically, so a
	// three-step request must fail before either target query starts.
	records.expandLimits.maxQuerySteps = 3
	_, err = records.GetExpanded(ctx, GetRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_posts", ID: uuid.MustParse(page.Items[0]["id"].(string)), Expand: "owner",
	})
	assertValidationDetail(t, err, "expand", "expand_budget_exceeded")
}

func TestGetExpandedPreservesNullAndEmptyRelationSemanticsWithoutTargetReads(t *testing.T) {
	_, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name:   "expand_empty_people",
		Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText}},
	})
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "expand_empty_posts",
		Fields: []schema.FieldInput{
			{Name: "owner", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "expand_empty_people", Cardinality: schema.RelationSingle,
				OnDelete: schema.RelationRestrict,
			}},
			{Name: "reviewers", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "expand_empty_people", Cardinality: schema.RelationMultiple,
				MinSelect: expandInt(0), MaxSelect: expandInt(4), OnDelete: schema.RelationRestrict,
			}},
		},
	})
	root, err := records.Create(ctx, "expand_empty_posts", map[string]json.RawMessage{
		"owner": json.RawMessage(`null`),
	})
	if err != nil {
		t.Fatal(err)
	}

	expanded, err := records.GetExpanded(ctx, GetRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_empty_posts", ID: uuid.MustParse(root["id"].(string)),
		Expand: "owner,reviewers",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, found := expanded.Expand["owner"]; found {
		t.Fatalf("null single relation emitted expand key: %#v", expanded.Expand)
	}
	reviewers, ok := expanded.Expand["reviewers"].([]map[string]any)
	if !ok || len(reviewers) != 0 {
		t.Fatalf("empty multi expansion = %#v, want non-null empty array", expanded.Expand["reviewers"])
	}
	if expanded.querySteps != 2 {
		t.Fatalf("empty-reference query steps = %d, want base + root hydration only", expanded.querySteps)
	}
}

func TestExpandedReadRejectsLegacyExpandFieldCollisionWithoutBreakingPlainGet(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name:   "expand_collision_targets",
		Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText}},
	})
	// "expand" is a reserved field name, so a collection carrying one can no
	// longer be created through the schema API. Databases written before the
	// reservation can still hold one, and the expand planner must keep
	// rejecting those rather than silently mis-resolving the response key, so
	// this test reproduces that legacy shape by renaming the catalogued field
	// after apply. Only the logical name moves; the physical column keeps its
	// original identifier, exactly as a pre-reservation catalog would.
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "expand_collision_roots",
		Fields: []schema.FieldInput{
			{Name: "legacy_expand", Type: schema.TypeText},
			{Name: "owner", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "expand_collision_targets", Cardinality: schema.RelationSingle,
				OnDelete: schema.RelationRestrict,
			}},
		},
	})
	if _, err := schemas.PlanCreate(ctx, schema.CreateCollectionInput{
		Name:   "expand_collision_rejected",
		Fields: []schema.FieldInput{{Name: "expand", Type: schema.TypeText}},
	}); err == nil {
		t.Fatal("PlanCreate with a field named expand = nil error, want reserved name rejection")
	}
	if _, err := pool.Exec(ctx, `alter table app.expand_collision_roots
		rename column legacy_expand to expand`); err != nil {
		t.Fatal(err)
	}
	tag, err := pool.Exec(ctx, `update pgbase_meta.fields as fields
		set name = 'expand', column_name = 'expand'
		from pgbase_meta.collections as collections
		where fields.collection_id = collections.id
			and collections.name = 'expand_collision_roots'
			and fields.name = 'legacy_expand'`)
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("legacy field rename affected %d rows, want 1", tag.RowsAffected())
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.schema_state
		set version = version + 1, updated_at = current_timestamp where singleton`); err != nil {
		t.Fatal(err)
	}
	target, err := records.Create(ctx, "expand_collision_targets", map[string]json.RawMessage{"name": json.RawMessage(`"target"`)})
	if err != nil {
		t.Fatal(err)
	}
	root, err := records.Create(ctx, "expand_collision_roots", map[string]json.RawMessage{
		"expand": json.RawMessage(`"legacy"`),
		"owner":  json.RawMessage(`"` + target["id"].(string) + `"`),
	})
	if err != nil {
		t.Fatal(err)
	}
	rootID := uuid.MustParse(root["id"].(string))
	plain, err := records.Get(ctx, GetRequest{
		Principal: testReadPrincipal, Collection: "expand_collision_roots", ID: rootID,
	})
	if err != nil || plain.Data["expand"] != "legacy" {
		t.Fatalf("plain Get legacy field = %#v, err=%v", plain.Data["expand"], err)
	}
	_, err = records.GetExpanded(ctx, GetRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_collision_roots", ID: rootID, Expand: "owner",
	})
	assertValidationDetail(t, err, "expand", "expand_invalid_path")
}

func TestGetExpandedSupportsOneHopSelfRelationCycle(t *testing.T) {
	_, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "expand_nodes",
		Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText},
			{Name: "parent", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "expand_nodes", Cardinality: schema.RelationSingle,
				OnDelete: schema.RelationRestrict,
			}},
		},
	})
	first, err := records.Create(ctx, "expand_nodes", map[string]json.RawMessage{"name": json.RawMessage(`"first"`)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := records.Create(ctx, "expand_nodes", map[string]json.RawMessage{
		"name":   json.RawMessage(`"second"`),
		"parent": json.RawMessage(`"` + first["id"].(string) + `"`),
	})
	if err != nil {
		t.Fatal(err)
	}
	firstID := uuid.MustParse(first["id"].(string))
	secondID := uuid.MustParse(second["id"].(string))
	if _, err := records.Update(ctx, "expand_nodes", firstID, map[string]json.RawMessage{
		"parent": json.RawMessage(`"` + secondID.String() + `"`),
	}); err != nil {
		t.Fatal(err)
	}

	expanded, err := records.GetExpanded(ctx, GetRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_nodes", ID: secondID, Expand: "parent",
	})
	if err != nil {
		t.Fatal(err)
	}
	parent := expanded.Expand["parent"].(map[string]any)
	if parent["id"] != firstID.String() || parent["parent"] != secondID.String() {
		t.Fatalf("one-hop self target = %#v", parent)
	}
	if _, recursivelyExpanded := parent["expand"]; recursivelyExpanded {
		t.Fatalf("one-hop self relation recursively expanded: %#v", parent)
	}
}

func TestGetExpandedMeasuresExactFlatJSONWithTrailingNewline(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	createExpandGraph(t, ctx, pool, schemas, records, 1)
	page, err := records.listScalar(ctx, "expand_posts", "", 1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	request := GetRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_posts", ID: uuid.MustParse(page.Items[0]["id"].(string)), Expand: "owner",
	}
	result, err := records.GetExpanded(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	records.expandLimits.maxBytes = len(encoded) + 1
	if _, err := records.GetExpanded(ctx, request); err != nil {
		t.Fatalf("exact-size Get rejected: %v", err)
	}
	records.expandLimits.maxBytes = len(encoded)
	_, err = records.GetExpanded(ctx, request)
	assertValidationDetail(t, err, "response", "response_too_large")
}

func TestListExpandedTruncatesAtExactFlatByteLimitAndReanchorsCursor(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	createExpandGraph(t, ctx, pool, schemas, records, 4)
	// Only the transaction clock is stubbed; the durable traversal underneath is
	// the resolver's own, because a v5 descriptor can only be minted from a ready
	// boundary and that readiness is not something a literal can fake.
	fixedTransactionTime := time.Date(2026, 8, 31, 6, 0, 0, 0, time.UTC)
	resolved := records.resolveTraversal
	records.resolveTraversal = func(ctx context.Context, tx pgx.Tx, id uuid.UUID) (membership.Traversal, error) {
		traversal, err := resolved(ctx, tx, id)
		if err != nil {
			return traversal, err
		}
		traversal.TransactionTime = fixedTransactionTime
		return traversal, nil
	}
	request := ListRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_posts", Limit: 3, Expand: "owner,reviewers",
	}
	wide, err := records.ListExpanded(ctx, request)
	if err != nil || len(wide.Items) != 3 || wide.NextCursor == "" {
		t.Fatalf("wide page items/cursor = %d/%q err=%v", len(wide.Items), wide.NextCursor, err)
	}
	// The cursor a two-item page issues on its own is what the byte-limited
	// three-item page has to re-anchor to, byte for byte.
	narrowRequest := request
	narrowRequest.Limit = 2
	narrow, err := records.ListExpanded(ctx, narrowRequest)
	if err != nil || len(narrow.Items) != 2 || narrow.NextCursor == "" {
		t.Fatalf("narrow page items/cursor = %d/%q err=%v", len(narrow.Items), narrow.NextCursor, err)
	}
	expectedCursor := narrow.NextCursor
	exactPage := ExpandedRecordPage{Items: wide.Items[:2], NextCursor: expectedCursor}
	exactJSON, err := json.Marshal(exactPage)
	if err != nil {
		t.Fatal(err)
	}
	records.expandLimits.maxBytes = len(exactJSON) + 1

	limited, err := records.ListExpanded(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	emitted, err := json.Marshal(limited)
	if err != nil {
		t.Fatal(err)
	}
	if len(emitted)+1 != records.expandLimits.maxBytes {
		t.Fatalf("emitted page bytes = %d, want exact %d", len(emitted)+1, records.expandLimits.maxBytes)
	}
	if len(limited.Items) != 2 || limited.NextCursor != expectedCursor {
		t.Fatalf("limited page items/cursor = %d/%q, want 2/reanchored", len(limited.Items), limited.NextCursor)
	}

	request.Cursor = limited.NextCursor
	next, err := records.ListExpanded(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	all, err := records.listScalar(ctx, "expand_posts", "", 10, "", "")
	if err != nil {
		t.Fatal(err)
	}
	gotIDs := expandedRootIDs(append(append([]ExpandedRecord{}, limited.Items...), next.Items...))
	wantIDs := recordIDs(all.Items)
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("paginated expanded ids = %v, want %v", gotIDs, wantIDs)
	}
}

// The byte-limit truncate/re-anchor path above stubs a v4 traversal, so nothing
// proved that an expanded page mints a v5 descriptor, carries the same binding
// through a re-anchor, and keeps a later creation outside the traversal the way
// the scalar and relation roots do.
func TestListExpandedReanchorsInsideOneV5MembershipBoundary(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	createExpandGraph(t, ctx, pool, schemas, records, 4)
	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	posts := catalog.Collections["expand_posts"]
	baseline, err := records.listScalar(ctx, "expand_posts", "", 10, "", "")
	if err != nil {
		t.Fatal(err)
	}
	postIDs := make([]uuid.UUID, 0, len(baseline.Items))
	for _, id := range recordIDs(baseline.Items) {
		postIDs = append(postIDs, uuid.MustParse(id))
	}
	syncMembershipOrdinals(t, ctx, pool)
	controller := membership.New(pool)
	runtimeB, err := membership.RegisterWriter(ctx, controller, membership.WriterB)
	if err != nil {
		t.Fatalf("RegisterWriter(B): %v", err)
	}
	if _, err := controller.EnableV5(ctx, membership.FleetProof{
		Members: []membership.FleetMember{runtimeB.FleetMember()},
	}); err != nil {
		t.Fatalf("EnableV5: %v", err)
	}
	// Only the transaction time is pinned, so the expected cursor is byte
	// comparable while the resolver's own generation, high-water and durable
	// ready state still decide whether a descriptor may be minted at all.
	fixedTransactionTime := time.Date(2026, 8, 31, 6, 0, 0, 0, time.UTC)
	resolved := records.resolveTraversal
	records.resolveTraversal = func(ctx context.Context, tx pgx.Tx, id uuid.UUID) (membership.Traversal, error) {
		traversal, err := resolved(ctx, tx, id)
		traversal.TransactionTime = fixedTransactionTime
		return traversal, err
	}

	request := ListRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_posts", Limit: 3, Expand: "owner,reviewers",
	}
	wide, err := records.ListExpanded(ctx, request)
	if err != nil || len(wide.Items) != 3 || wide.NextCursor == "" {
		t.Fatalf("wide page items/cursor = %d/%q err=%v", len(wide.Items), wide.NextCursor, err)
	}
	descriptor := &cursor.V5Descriptor{
		CatalogVersion: catalog.Version, MembershipGeneration: 1, HighWater: int64(len(postIDs)),
		QueryHash: records.cursors.QueryFingerprint([]byte(`{"filter":null}`)),
		ScopeHash: records.cursors.ScopeFingerprint([]byte(
			`{"kind":"administrator","id":"` + expandTestAdministratorID.String() + `","policy":"administrator-all-v1"}`,
		)),
		Order: []cursor.V5OrderTerm{
			{Path: []string{"$created_at"}, Type: string(schema.TypeTimestampTZ), Nulls: cursor.NullsLast},
			{Path: []string{"$id"}, Type: string(schema.TypeUUID), Nulls: cursor.NullsLast},
		},
	}
	wideCursor, err := records.cursors.Decode(wide.NextCursor, posts.ID, time.Time{})
	if err != nil || wideCursor.Version != cursor.Version5 || !reflect.DeepEqual(wideCursor.V5, descriptor) {
		t.Fatalf("expanded page v5 binding = %#v (err %v), want %#v", wideCursor.V5, err, descriptor)
	}

	anchor := wide.Items[1].Root
	values, err := recordSortValues(effectiveSortKeys(nil), anchor)
	if err != nil {
		t.Fatal(err)
	}
	expectedCursor, err := records.cursors.Encode(cursor.Page{
		Version: cursor.Version5, CollectionID: posts.ID, Values: values,
		AnchorID:  uuid.MustParse(anchor["id"].(string)),
		ExpiresAt: fixedTransactionTime.Add(cursorTTL), V5: descriptor,
	})
	if err != nil {
		t.Fatal(err)
	}
	exactJSON, err := json.Marshal(ExpandedRecordPage{Items: wide.Items[:2], NextCursor: expectedCursor})
	if err != nil {
		t.Fatal(err)
	}
	records.expandLimits.maxBytes = len(exactJSON) + 1
	limited, err := records.ListExpanded(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Items) != 2 || limited.NextCursor != expectedCursor {
		t.Fatalf("truncated page items/cursor = %d/%q, want 2 re-anchored under the same v5 binding",
			len(limited.Items), limited.NextCursor)
	}
	records.expandLimits = defaultExpandLimits

	late, err := records.Create(ctx, "expand_posts", map[string]json.RawMessage{
		"title": json.RawMessage(`"post-late"`),
	})
	if err != nil {
		t.Fatalf("Create(late post): %v", err)
	}
	lateID := uuid.MustParse(late["id"].(string))
	if _, err := pool.Exec(ctx, `with bumped as (
		update pgbase_meta.collection_creation_counters
		set last_ordinal = $3, updated_at = transaction_timestamp()
		where collection_id = $1 and generation = 1 and last_ordinal = $3 - 1
		returning collection_id
	)
	insert into pgbase_meta.record_creation_ordinals (collection_id, record_id, ordinal)
	select collection_id, $2, $3 from bumped`, posts.ID, lateID, len(postIDs)+1); err != nil {
		t.Fatalf("commit late membership: %v", err)
	}

	request.Cursor = limited.NextCursor
	continued, err := records.ListExpanded(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	want := recordIDs(baseline.Items)[2:]
	if got := expandedRootIDs(continued.Items); !reflect.DeepEqual(got, want) || continued.NextCursor != "" {
		t.Fatalf("continued page = %v cursor:%q, want %v and a terminal page", got, continued.NextCursor, want)
	}

	request.Cursor, request.Limit = "", 1
	fresh, err := records.ListExpanded(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	freshCursor, err := records.cursors.Decode(fresh.NextCursor, posts.ID, time.Time{})
	if err != nil || freshCursor.V5 == nil || freshCursor.V5.HighWater != int64(len(postIDs)+1) {
		t.Fatalf("fresh traversal binding = %#v (err %v), want high-water %d",
			freshCursor.V5, err, len(postIDs)+1)
	}
	request.Limit = 10
	all, err := records.ListExpanded(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if got := expandedRootIDs(all.Items); !reflect.DeepEqual(got, append(recordIDs(baseline.Items), lateID.String())) {
		t.Fatalf("fresh traversal ids = %v, want every committed post", got)
	}
}

func TestExpandedCursorIgnoresTargetUpdates(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	graph := createExpandGraph(t, ctx, pool, schemas, records, 3)
	request := ListRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_posts", Limit: 2, Expand: "owner",
	}
	before, err := records.ListExpanded(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := records.Update(ctx, "expand_people", graph.ada, map[string]json.RawMessage{
		"name": json.RawMessage(`"Ada changed"`),
	}); err != nil {
		t.Fatal(err)
	}
	after, err := records.ListExpanded(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if before.NextCursor == "" {
		t.Fatal("expanded page did not issue a root cursor")
	}
	assertEquivalentCursorIgnoringExpiry(t, records,
		mustCollectionID(t, schemas, "expand_posts"), after.NextCursor, before.NextCursor)
	if after.Items[0].Expand["owner"].(map[string]any)["name"] != "Ada changed" {
		t.Fatalf("target update was not visible in expansion: %#v", after.Items[0].Expand["owner"])
	}
}

type expandGraph struct {
	firstLabel  uuid.UUID
	secondLabel uuid.UUID
	ada         uuid.UUID
	bob         uuid.UUID
}

func createExpandGraph(
	t *testing.T,
	ctx context.Context,
	pool *database.Pool,
	schemas *schema.Service,
	records *Service,
	postCount int,
) expandGraph {
	t.Helper()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name:   "expand_labels",
		Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText, Required: true}},
	})
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "expand_people",
		Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText, Required: true},
			{Name: "labels", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "expand_labels", Cardinality: schema.RelationMultiple,
				MinSelect: expandInt(0), MaxSelect: expandInt(4), OnDelete: schema.RelationRestrict,
			}},
		},
	})
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "expand_posts",
		Fields: []schema.FieldInput{
			{Name: "title", Type: schema.TypeText, Required: true},
			{Name: "owner", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "expand_people", Cardinality: schema.RelationSingle, OnDelete: schema.RelationRestrict,
			}},
			{Name: "reviewers", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "expand_people", Cardinality: schema.RelationMultiple,
				MinSelect: expandInt(0), MaxSelect: expandInt(4), OnDelete: schema.RelationRestrict,
			}},
		},
	})
	create := func(collection, name string) uuid.UUID {
		record, err := records.Create(ctx, collection, map[string]json.RawMessage{"name": json.RawMessage(`"` + name + `"`)})
		if err != nil {
			t.Fatal(err)
		}
		return uuid.MustParse(record["id"].(string))
	}
	graph := expandGraph{
		firstLabel:  create("expand_labels", "systems"),
		secondLabel: create("expand_labels", "compilers"),
		ada:         create("expand_people", "Ada"),
		bob:         create("expand_people", "Bob"),
	}
	insertExpandEdges(t, ctx, pool, "expand_people", "labels", graph.ada, graph.secondLabel, graph.firstLabel)
	insertExpandEdges(t, ctx, pool, "expand_people", "labels", graph.bob, graph.firstLabel)
	for index := 0; index < postCount; index++ {
		post, err := records.Create(ctx, "expand_posts", map[string]json.RawMessage{
			"title": json.RawMessage(`"post-` + string(rune('a'+index)) + `"`),
			"owner": json.RawMessage(`"` + graph.ada.String() + `"`),
		})
		if err != nil {
			t.Fatal(err)
		}
		insertExpandEdges(t, ctx, pool, "expand_posts", "reviewers", uuid.MustParse(post["id"].(string)), graph.bob, graph.ada)
	}
	return graph
}

func insertExpandEdges(t *testing.T, ctx context.Context, pool *database.Pool, collection, field string, source uuid.UUID, targets ...uuid.UUID) {
	t.Helper()
	table := schema.NewRelationPhysicalNames(collection, field).MultiTable
	for position, target := range targets {
		if _, err := pool.Exec(ctx, "insert into "+pgx.Identifier{"pgbase_relation", table}.Sanitize()+
			" (source_id, target_id, position) values ($1, $2, $3)", source, target, position); err != nil {
			t.Fatal(err)
		}
	}
}

func mustCollectionID(t *testing.T, schemas *schema.Service, name string) uuid.UUID {
	t.Helper()
	catalog, err := schemas.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	collection, ok := catalog.Collections[name]
	if !ok {
		t.Fatalf("collection %q not found", name)
	}
	return collection.ID
}

func expandedRootIDs(records []ExpandedRecord) []string {
	ids := make([]string, len(records))
	for index, record := range records {
		ids[index] = record.Root["id"].(string)
	}
	return ids
}

func recordIDs(records []map[string]any) []string {
	ids := make([]string, len(records))
	for index, record := range records {
		ids[index] = record["id"].(string)
	}
	return ids
}

func expandInt(value int) *int { return &value }
