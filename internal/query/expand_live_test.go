package query

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
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
	fixedNow := time.Date(2026, 8, 31, 5, 0, 0, 0, time.UTC)
	records.now = func() time.Time { return fixedNow }

	base, err := records.List(ctx, "expand_posts", "", 2, "", "")
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
	if expanded.NextCursor == "" || expanded.NextCursor != base.NextCursor {
		t.Fatalf("expanded cursor = %q, base cursor = %q", expanded.NextCursor, base.NextCursor)
	}
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

func TestExpandRecordsRejectsMissingRequestedRelationField(t *testing.T) {
	_, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name:   "expand_missing_targets",
		Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText, Required: true}},
	})
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "expand_missing_roots",
		Fields: []schema.FieldInput{
			{Name: "owner", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "expand_missing_targets", Cardinality: schema.RelationSingle,
				OnDelete: schema.RelationRestrict,
			}},
		},
	})

	err := records.inTxCatalog(ctx, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		_, err := records.ExpandRecords(ctx, tx, catalog, collection, []map[string]any{{
			"id":         uuid.New().String(),
			"created_at": time.Now().UTC(),
			"updated_at": time.Now().UTC(),
		}}, ExpandRequest{
			Principal: Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
			Expand:    "owner",
		})
		return err
	}, "expand_missing_roots")
	if !errors.Is(err, schema.ErrConflict) {
		t.Fatalf("ExpandRecords() error = %v, want opaque schema conflict", err)
	}
}

func TestGetExpandedBoundsHydratedEdgeRows(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	createExpandGraph(t, ctx, pool, schemas, records, 1)
	page, err := records.List(ctx, "expand_posts", "", 1, "", "")
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
	page, err := records.List(ctx, "expand_posts", "", 1, "", "")
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

func TestExpandedPolicyRestrictsTargetReadsAndProjection(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	graph := createExpandGraph(t, ctx, pool, schemas, records, 1)
	base, err := records.List(ctx, "expand_posts", "", 1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	rootID := uuid.MustParse(base.Items[0]["id"].(string))
	request := GetRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_posts", ID: rootID, Expand: "owner,reviewers",
	}

	t.Run("deny all performs no target hydration", func(t *testing.T) {
		records.policy = projectionPolicyFunc(func(context.Context, Principal, schema.Collection, ReadAction) (Predicate, FieldSet, error) {
			return DenyAllRecords(), AllFields(), nil
		})
		result, err := records.GetExpanded(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if _, found := result.Expand["owner"]; found {
			t.Fatalf("hidden single target leaked: %#v", result.Expand)
		}
		if reviewers, ok := result.Expand["reviewers"].([]map[string]any); !ok || len(reviewers) != 0 {
			t.Fatalf("hidden multi targets = %#v", result.Expand["reviewers"])
		}
		if result.querySteps != 2 {
			t.Fatalf("deny-all steps = %d, want only base + root hydration", result.querySteps)
		}
	})

	t.Run("id subset reads only visible target", func(t *testing.T) {
		allowed, err := AllowRecordIDs(graph.bob)
		if err != nil {
			t.Fatal(err)
		}
		records.policy = projectionPolicyFunc(func(context.Context, Principal, schema.Collection, ReadAction) (Predicate, FieldSet, error) {
			return allowed, AllFields(), nil
		})
		result, err := records.GetExpanded(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if _, found := result.Expand["owner"]; found {
			t.Fatalf("owner outside allowed subset leaked: %#v", result.Expand)
		}
		reviewers := result.Expand["reviewers"].([]map[string]any)
		if len(reviewers) != 1 || reviewers[0]["id"] != graph.bob.String() {
			t.Fatalf("visible reviewers = %#v", reviewers)
		}
		if result.querySteps != 4 {
			t.Fatalf("ID-subset steps = %d, want base/root plus one target/hydration batch", result.querySteps)
		}
	})

	t.Run("field subset skips unselected multi hydration", func(t *testing.T) {
		records.policy = projectionPolicyFunc(func(context.Context, Principal, schema.Collection, ReadAction) (Predicate, FieldSet, error) {
			return AllowAllRecords(), ProjectFields("name"), nil
		})
		result, err := records.GetExpanded(ctx, GetRequest{
			Principal: request.Principal, Collection: request.Collection, ID: request.ID, Expand: "owner",
		})
		if err != nil {
			t.Fatal(err)
		}
		owner := result.Expand["owner"].(map[string]any)
		if owner["name"] != "Ada" || owner["labels"] != nil {
			t.Fatalf("projected owner = %#v", owner)
		}
		if result.querySteps != 3 {
			t.Fatalf("field-subset steps = %d, want base/root plus target batch", result.querySteps)
		}
	})
}

func TestExpandBaseRecordsHydratesRootInOwnedTransactionWithoutMutatingInput(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	graph := createExpandGraph(t, ctx, pool, schemas, records, 1)
	page, err := records.List(ctx, "expand_posts", "", 1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	rootID := uuid.MustParse(page.Items[0]["id"].(string))

	err = records.inTxCatalog(ctx, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		statement := CompileSelectByID(collection, rootID)
		base, err := EncodeRecord(collection, tx.QueryRow(ctx, statement.SQL, statement.Args...))
		if err != nil {
			return err
		}
		if got := base["reviewers"].([]string); len(got) != 0 {
			t.Fatalf("base multi placeholder = %#v", got)
		}
		expanded, err := records.ExpandBaseRecords(ctx, tx, catalog, collection, []map[string]any{base}, ExpandRequest{
			Principal: Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
			Expand:    "owner",
		})
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(expanded[0].Root["reviewers"], []string{graph.bob.String(), graph.ada.String()}) {
			t.Fatalf("hydrated root reviewers = %#v", expanded[0].Root["reviewers"])
		}
		if got := base["reviewers"].([]string); len(got) != 0 {
			t.Fatalf("ExpandBaseRecords mutated input placeholder: %#v", got)
		}
		if expanded[0].querySteps != 4 {
			t.Fatalf("executor query steps = %d, want root select/hydrate + target select/hydrate", expanded[0].querySteps)
		}
		return nil
	}, "expand_posts")
	if err != nil {
		t.Fatal(err)
	}
}

func TestExpandRecordsClassifiesTargetCatalogDriftAsOpaqueConflict(t *testing.T) {
	_, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name:   "expand_drift_targets",
		Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText}},
	})
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "expand_drift_roots",
		Fields: []schema.FieldInput{{Name: "owner", Type: schema.TypeRelation, Relation: &schema.RelationInput{
			TargetCollection: "expand_drift_targets", Cardinality: schema.RelationSingle,
			OnDelete: schema.RelationRestrict,
		}}},
	})
	target, err := records.Create(ctx, "expand_drift_targets", map[string]json.RawMessage{"name": json.RawMessage(`"target"`)})
	if err != nil {
		t.Fatal(err)
	}
	root, err := records.Create(ctx, "expand_drift_roots", map[string]json.RawMessage{
		"owner": json.RawMessage(`"` + target["id"].(string) + `"`),
	})
	if err != nil {
		t.Fatal(err)
	}

	err = records.inTxCatalog(ctx, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		collections := make(map[string]schema.Collection, len(catalog.Collections))
		for name, item := range catalog.Collections {
			collections[name] = item
		}
		corrupt := collections["expand_drift_targets"]
		corrupt.TableName = "missing_expand_private_table"
		collections[corrupt.Name] = corrupt
		_, err := records.ExpandRecords(ctx, tx, schema.Catalog{
			Version: catalog.Version, Collections: collections, Incoming: catalog.Incoming,
		}, collection, []map[string]any{root}, ExpandRequest{
			Principal: Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
			Expand:    "owner",
		})
		return err
	}, "expand_drift_roots")
	if !errors.Is(err, schema.ErrConflict) {
		t.Fatalf("target drift error = %v, want schema conflict", err)
	}
	if strings.Contains(err.Error(), "missing_expand_private_table") {
		t.Fatalf("target drift error disclosed physical identifier: %q", err)
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
	plain, err := records.Get(ctx, "expand_collision_roots", rootID)
	if err != nil || plain["expand"] != "legacy" {
		t.Fatalf("plain Get legacy field = %#v, err=%v", plain["expand"], err)
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
	page, err := records.List(ctx, "expand_posts", "", 1, "", "")
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
	fixedNow := time.Date(2026, 8, 31, 6, 0, 0, 0, time.UTC)
	records.now = func() time.Time { return fixedNow }
	request := ListRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: expandTestAdministratorID},
		Collection: "expand_posts", Limit: 3, Expand: "owner,reviewers",
	}
	wide, err := records.ListExpanded(ctx, request)
	if err != nil || len(wide.Items) != 3 || wide.NextCursor == "" {
		t.Fatalf("wide page items/cursor = %d/%q err=%v", len(wide.Items), wide.NextCursor, err)
	}
	sortKeys := effectiveSortKeys(nil)
	anchor := wide.Items[1].Root
	values, err := recordSortValues(sortKeys, anchor)
	if err != nil {
		t.Fatal(err)
	}
	expectedCursor, err := records.cursors.Encode(PageCursor{
		Version: cursorVersion, CollectionID: mustCollectionID(t, schemas, "expand_posts"),
		Sort: sortKeys, Values: values, AnchorID: uuid.MustParse(anchor["id"].(string)),
		ExpiresAt: fixedNow.Add(cursorTTL),
	})
	if err != nil {
		t.Fatal(err)
	}
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
	all, err := records.List(ctx, "expand_posts", "", 10, "", "")
	if err != nil {
		t.Fatal(err)
	}
	gotIDs := expandedRootIDs(append(append([]ExpandedRecord{}, limited.Items...), next.Items...))
	wantIDs := recordIDs(all.Items)
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("paginated expanded ids = %v, want %v", gotIDs, wantIDs)
	}
}

func TestExpandedCursorIgnoresTargetUpdates(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	graph := createExpandGraph(t, ctx, pool, schemas, records, 3)
	records.now = func() time.Time { return time.Date(2026, 8, 31, 7, 0, 0, 0, time.UTC) }
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
	if before.NextCursor == "" || after.NextCursor != before.NextCursor {
		t.Fatalf("target update changed root cursor: before=%q after=%q", before.NextCursor, after.NextCursor)
	}
	if after.Items[0].Expand["owner"].(map[string]any)["name"] != "Ada changed" {
		t.Fatalf("target update was not visible in expansion: %#v", after.Items[0].Expand["owner"])
	}
}

type projectionPolicyFunc func(context.Context, Principal, schema.Collection, ReadAction) (Predicate, FieldSet, error)

func (f projectionPolicyFunc) ReadScope(
	ctx context.Context,
	principal Principal,
	collection schema.Collection,
	action ReadAction,
) (Predicate, FieldSet, error) {
	return f(ctx, principal, collection, action)
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
