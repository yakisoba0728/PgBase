//go:build integration

package recordops

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestTextRecordIdentityCRUDReplayAndExactCase(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := recordOperationsTestContext(t)
	definition := schema.CreateCollectionInput{
		Name:     "text_identity_records",
		RecordID: &schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 32, Pattern: `^[A-Za-z0-9]+$`},
		Fields:   []schema.FieldInput{{Name: "title", Type: schema.TypeText, Required: true}},
	}
	plan, err := schemas.Preview(ctx, schema.NewCreateChange(definition))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(definition), plan, principal.ID)); err != nil {
		t.Fatal(err)
	}

	created, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "text-id-create", Collection: definition.Name, IdempotencyKey: "text-id-create-key-0001", Input: map[string]json.RawMessage{"id": json.RawMessage(`"CaseSensitive01"`), "title": json.RawMessage(`"first"`)}})
	if err != nil || created.Record == nil || created.Record.Data["id"] != "CaseSensitive01" || created.InternalID == uuid.Nil {
		t.Fatalf("create=%#v err=%v", created, err)
	}
	replayed, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "text-id-replay", Collection: definition.Name, IdempotencyKey: "text-id-create-key-0001", Input: map[string]json.RawMessage{"id": json.RawMessage(`"CaseSensitive01"`), "title": json.RawMessage(`"first"`)}})
	if err != nil || !replayed.Replayed || replayed.Record.Data["id"] != "CaseSensitive01" || replayed.InternalID != created.InternalID {
		t.Fatalf("replay=%#v err=%v", replayed, err)
	}

	got, err := records.Get(ctx, GetRequest{Principal: principal, RequestID: "text-id-get", Collection: definition.Name, PublicID: "CaseSensitive01"})
	if err != nil || got.Data["id"] != "CaseSensitive01" {
		t.Fatalf("get=%#v err=%v", got, err)
	}
	updated, err := records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "text-id-update", Collection: definition.Name, PublicID: "CaseSensitive01", Input: map[string]json.RawMessage{"title": json.RawMessage(`"updated"`)}})
	if err != nil || updated.Record.Data["id"] != "CaseSensitive01" || updated.Record.Data["title"] != "updated" {
		t.Fatalf("update=%#v err=%v", updated, err)
	}

	_, err = records.Create(ctx, CreateRequest{Principal: principal, RequestID: "text-id-collision", Collection: definition.Name, Input: map[string]json.RawMessage{"id": json.RawMessage(`"CaseSensitive01"`), "title": json.RawMessage(`"collision"`)}})
	var operationError *Error
	if !errors.As(err, &operationError) || operationError.Code != CodeRecordIDConflict {
		t.Fatalf("collision error=%v", err)
	}
	caseDistinct, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "text-id-case", Collection: definition.Name, Input: map[string]json.RawMessage{"id": json.RawMessage(`"casesensitive01"`), "title": json.RawMessage(`"distinct"`)}})
	if err != nil || caseDistinct.Record.Data["id"] != "casesensitive01" {
		t.Fatalf("case-distinct create=%#v err=%v", caseDistinct, err)
	}
	listed, err := records.ListPocketBase(ctx, PocketBaseListRequest{ListRequest: ListRequest{
		Principal: principal, RequestID: "text-id-list", Collection: definition.Name,
		Filter: `id = "CaseSensitive01"`, Sort: "id",
	}, Page: 1, PerPage: 10})
	if err != nil || len(listed.Items) != 1 || listed.Items[0]["id"] != "CaseSensitive01" {
		t.Fatalf("PocketBase text ID list=%#v err=%v", listed, err)
	}

	if _, err := records.Get(ctx, GetRequest{Principal: principal, RequestID: "text-id-no-fallback", Collection: definition.Name, ID: created.InternalID}); !errors.As(err, &operationError) || operationError.Code != CodeInvalidRecordID {
		t.Fatalf("internal UUID fallback error=%v", err)
	}
	if _, err := records.Delete(ctx, DeleteRequest{Principal: principal, RequestID: "text-id-delete", Collection: definition.Name, PublicID: "CaseSensitive01"}); err != nil {
		t.Fatal(err)
	}
	if _, err := records.Get(ctx, GetRequest{Principal: principal, RequestID: "text-id-gone", Collection: definition.Name, PublicID: "CaseSensitive01"}); !errors.Is(err, query.ErrRecordNotFound) {
		t.Fatalf("deleted get error=%v", err)
	}
	var aliases int
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.record_identities where collection_id=(select id from pgbase_meta.collections where name=$1) and external_id=$2`, definition.Name, "CaseSensitive01").Scan(&aliases); err != nil || aliases != 0 {
		t.Fatalf("deleted aliases=%d err=%v", aliases, err)
	}
}

func TestTextRecordIdentityRelationsAndOrderedBatchResolution(t *testing.T) {
	_, schemas, records, principal := newRecordOperationsTest(t)
	ctx := recordOperationsTestContext(t)
	apply := func(definition schema.CreateCollectionInput) {
		t.Helper()
		plan, err := schemas.Preview(ctx, schema.NewCreateChange(definition))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(definition), plan, principal.ID)); err != nil {
			t.Fatal(err)
		}
	}
	policy := &schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 32, Pattern: `^[A-Za-z0-9]+$`}
	maximumTargets := 10
	apply(schema.CreateCollectionInput{Name: "text_identity_targets", RecordID: policy, Fields: []schema.FieldInput{{Name: "label", Type: schema.TypeText, Required: true}}})
	apply(schema.CreateCollectionInput{Name: "text_identity_sources", RecordID: policy, Fields: []schema.FieldInput{
		{Name: "label", Type: schema.TypeText, Required: true},
		{Name: "primary_target", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "text_identity_targets", Cardinality: schema.RelationSingle}},
		{Name: "other_targets", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "text_identity_targets", Cardinality: schema.RelationMultiple, MaxSelect: &maximumTargets}},
	}})

	results, err := records.Batch(ctx, BatchRequest{Principal: principal, RequestID: "text-id-relation-batch", Operations: []BatchOperation{
		{Create: &CreateRequest{Collection: "text_identity_targets", Input: map[string]json.RawMessage{"id": json.RawMessage(`"TargetOne"`), "label": json.RawMessage(`"one"`)}}},
		{Create: &CreateRequest{Collection: "text_identity_sources", Input: map[string]json.RawMessage{"id": json.RawMessage(`"SourceOne"`), "label": json.RawMessage(`"source"`), "primary_target": json.RawMessage(`"TargetOne"`), "other_targets": json.RawMessage(`["TargetOne"]`)}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := results[1].Record.Data; got["id"] != "SourceOne" || got["primary_target"] != "TargetOne" {
		t.Fatalf("batch relation projection = %#v", got)
	}
	values, ok := results[1].Record.Data["other_targets"].([]string)
	if !ok || len(values) != 1 || values[0] != "TargetOne" {
		t.Fatalf("batch multiple relation projection = %#v", results[1].Record.Data["other_targets"])
	}

	got, err := records.Get(ctx, GetRequest{Principal: principal, RequestID: "text-id-relation-get", Collection: "text_identity_sources", PublicID: "SourceOne"})
	if err != nil || got.Data["primary_target"] != "TargetOne" {
		t.Fatalf("get relation = %#v, %v", got, err)
	}
	list, err := records.List(ctx, ListRequest{Principal: principal, RequestID: "text-id-relation-list", Collection: "text_identity_sources",
		Expand: "primary_target", Fields: "id,primary_target,expand.primary_target.id"})
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("expanded list = %#v, %v", list, err)
	}
	encoded, err := json.Marshal(list.Items[0])
	if err != nil || string(encoded) != `{"expand":{"primary_target":{"id":"TargetOne"}},"id":"SourceOne","primary_target":"TargetOne"}` {
		t.Fatalf("expanded projection = %s, %v", encoded, err)
	}
	updated, err := records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "text-id-relation-update", Collection: "text_identity_sources", PublicID: "SourceOne", Input: map[string]json.RawMessage{"primary_target": json.RawMessage(`"TargetOne"`)}})
	if err != nil || updated.Record.Data["primary_target"] != "TargetOne" {
		t.Fatalf("update relation = %#v, %v", updated, err)
	}
}

func TestNativePageUsesPublicRelationIDWireBudget(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := t.Context()
	const (
		targetCount     = 1000
		rootCount       = 36
		maximumPageSize = 48<<20 + 4<<10
	)
	apply := func(definition schema.CreateCollectionInput) {
		t.Helper()
		change := schema.NewCreateChange(definition)
		plan, err := schemas.Preview(ctx, change)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := schemas.Apply(ctx, schema.LegacyApply(change, plan, principal.ID)); err != nil {
			t.Fatal(err)
		}
	}
	apply(schema.CreateCollectionInput{
		Name:     "wire_targets",
		RecordID: &schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 255, Pattern: `^[&0-9]+$`},
		Fields:   []schema.FieldInput{{Name: "label", Type: schema.TypeText}},
	})
	maximum := targetCount
	apply(schema.CreateCollectionInput{Name: "wire_roots", Fields: []schema.FieldInput{
		{Name: "links", Type: schema.TypeRelation, Relation: &schema.RelationInput{
			TargetCollection: "wire_targets", Cardinality: schema.RelationMultiple, MaxSelect: &maximum,
		}},
	}})
	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, root := catalog.Collections["wire_targets"], catalog.Collections["wire_roots"]
	targetTable := pgx.Identifier{"app", target.TableName}.Sanitize()
	rootTable := pgx.Identifier{"app", root.TableName}.Sanitize()
	edgeTable := pgx.Identifier{"pgbase_relation", schema.NewRelationPhysicalNames(root.TableName, "links").MultiTable}.Sanitize()
	// The JSON encoder escapes ampersands. The projected IDs cross the page
	// cap even though their internal UUIDs occupy well under two MiB.
	if _, err := pool.Exec(ctx, `WITH targets AS (
			INSERT INTO `+targetTable+` (id, created_at, updated_at, label)
			SELECT md5('wire-target-' || n::text)::uuid, now(), now(), n::text FROM generate_series(1, $1::integer) AS t(n)
			RETURNING id, label
		)
		INSERT INTO pgbase_meta.record_identities(collection_id, collection_kind, scope_id, internal_id, external_id)
		SELECT $2, 'base', $2, id, repeat('&', 250) || label FROM targets`, targetCount, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO `+rootTable+` (id, created_at, updated_at)
		SELECT md5('wire-root-' || n::text)::uuid, now() + n * interval '1 microsecond', now()
		FROM generate_series(1, $1::integer) AS t(n)`, rootCount); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO `+edgeTable+` (source_id, target_id, position)
		SELECT roots.id, targets.id, targets.label::integer - 1
		FROM `+rootTable+` roots CROSS JOIN `+targetTable+` targets`); err != nil {
		t.Fatal(err)
	}
	testutil.RetireRecordMembershipV4(t, ctx, pool)

	request := ListRequest{Principal: principal, RequestID: "wire-page", Collection: root.Name, Limit: rootCount, Fields: "id,links", Total: true}
	seen := make(map[string]bool, rootCount)
	for {
		page, err := records.List(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if page.TotalItems == nil || *page.TotalItems != rootCount || len(page.Items) == 0 {
			t.Fatalf("page count=%d total=%v", len(page.Items), page.TotalItems)
		}
		if request.Cursor == "" && page.NextCursor == "" {
			t.Fatal("first page missing continuation")
		}
		encoded, err := json.Marshal(page)
		if err != nil || len(encoded)+1 > maximumPageSize {
			t.Fatalf("page encoded bytes = %d, err=%v", len(encoded)+1, err)
		}
		for _, item := range page.Items {
			id, _ := item["id"].(string)
			links, _ := item["links"].([]string)
			if id == "" || seen[id] || len(links) != targetCount || links[0] != strings.Repeat("&", 250)+"1" || len(item) != 2 {
				t.Fatalf("invalid projected item: id=%q duplicate=%t link count=%d field count=%d", id, seen[id], len(links), len(item))
			}
			seen[id] = true
		}
		if page.NextCursor == "" {
			break
		}
		request.Cursor = page.NextCursor
	}
	if len(seen) != rootCount {
		t.Fatalf("unique paginated rows = %d, want %d", len(seen), rootCount)
	}

	request.Cursor = ""
	if _, err := records.ListPocketBase(ctx, PocketBaseListRequest{ListRequest: request, Page: 1, PerPage: rootCount, SkipTotal: true}); err == nil {
		t.Fatal("over-budget offset page must reject skipped rows")
	} else {
		var validation *query.ValidationError
		if !errors.As(err, &validation) || len(validation.Details) != 1 || validation.Details[0].Code != "response_too_large" {
			t.Fatalf("offset page: %v", err)
		}
	}

	request.Fields = "id"
	page, err := records.List(ctx, request)
	if err != nil || len(page.Items) != rootCount || page.NextCursor != "" {
		t.Fatalf("id-only page: records=%d cursor=%t err=%v", len(page.Items), page.NextCursor != "", err)
	}
}
