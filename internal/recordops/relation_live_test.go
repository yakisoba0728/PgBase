//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/observability"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestRecordOperationsRelationCreateReplayReadAndRelationOnlyPatch(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	createRelationCollections(t, ctx, schemas, principal.ID)

	firstTarget := createRelationTarget(t, ctx, records, principal, "first")
	secondTarget := createRelationTarget(t, ctx, records, principal, "second")
	request := CreateRequest{
		Principal: principal, RequestID: "relation-create-001", Collection: "articles",
		IdempotencyKey: "relation-create-key-0001",
		Input: map[string]json.RawMessage{
			"title": json.RawMessage(`"hello"`),
			"owner": relationUUIDJSON(firstTarget),
			"tags":  relationUUIDArrayJSON(firstTarget, secondTarget),
		},
	}
	created, err := records.Create(ctx, request)
	if err != nil {
		t.Fatalf("create relation record: %v", err)
	}
	assertRelationRecord(t, created.Record.Data, firstTarget, []uuid.UUID{firstTarget, secondTarget})

	replayRequest := request
	replayRequest.RequestID = "relation-create-002"
	replayed, err := records.Create(ctx, replayRequest)
	if err != nil {
		t.Fatalf("replay relation record: %v", err)
	}
	createdJSON, _ := json.Marshal(created.Record.Data)
	replayedJSON, _ := json.Marshal(replayed.Record.Data)
	if !replayed.Replayed || replayed.EventID != created.EventID || !reflect.DeepEqual(replayedJSON, createdJSON) {
		t.Fatalf("relation replay = %#v, want original %#v", replayed, created)
	}

	recordID := uuid.MustParse(created.Record.Data["id"].(string))
	got, err := records.Get(ctx, GetRequest{
		Principal: principal, RequestID: "relation-get-0001", Collection: "articles", ID: recordID,
	})
	if err != nil {
		t.Fatalf("get relation record: %v", err)
	}
	assertRelationRecord(t, got.Data, firstTarget, []uuid.UUID{firstTarget, secondTarget})

	testutil.RetireRecordMembershipV4(t, ctx, pool)
	page, err := records.List(ctx, ListRequest{
		Principal: principal, RequestID: "relation-list-001", Collection: "articles", Limit: 10,
	})
	if err != nil {
		t.Fatalf("list relation records: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("relation list items = %d, want 1", len(page.Items))
	}
	assertRelationRecord(t, page.Items[0], firstTarget, []uuid.UUID{firstTarget, secondTarget})

	beforeUpdatedAt := got.Data["updated_at"]
	updated, err := records.Update(ctx, UpdateRequest{
		Principal: principal, RequestID: "relation-update-01", Collection: "articles", ID: recordID,
		IfMatch: EntityTagSet{Present: true, Values: []string{got.ETag}},
		Input:   map[string]json.RawMessage{"tags": relationUUIDArrayJSON(secondTarget, firstTarget)},
	})
	if err != nil {
		t.Fatalf("relation-only update: %v", err)
	}
	if updated.Record.Version != got.Version+1 || updated.Record.ETag == got.ETag {
		t.Fatalf("relation-only revision = %#v, previous %#v", updated.Record, got)
	}
	if updated.Record.Data["updated_at"] == beforeUpdatedAt {
		t.Fatal("relation-only patch did not change updated_at")
	}
	assertRelationRecord(t, updated.Record.Data, firstTarget, []uuid.UUID{secondTarget, firstTarget})

	var changed []string
	if err := pool.QueryRow(ctx, `select changed_fields from pgbase_meta.record_audit_events where event_id = $1`, updated.EventID).Scan(&changed); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(changed, []string{"tags"}) {
		t.Fatalf("relation audit changed fields = %#v", changed)
	}
	var envelope []byte
	if err := pool.QueryRow(ctx, `select envelope from pgbase_meta.record_outbox where event_id = $1`, updated.EventID).Scan(&envelope); err != nil {
		t.Fatal(err)
	}
	var outbox struct {
		Changes map[string]json.RawMessage `json:"changes"`
	}
	if err := json.Unmarshal(envelope, &outbox); err != nil {
		t.Fatal(err)
	}
	var outboxTags []string
	if err := json.Unmarshal(outbox.Changes["tags"], &outboxTags); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(outboxTags, []string{secondTarget.String(), firstTarget.String()}) {
		t.Fatalf("relation outbox tags = %#v", outboxTags)
	}

	names := schema.NewRelationPhysicalNames("articles", "tags")
	rows, err := pool.Query(ctx, `select target_id, position from `+quotedRelationTable(names.MultiTable)+` where source_id = $1 order by position`, recordID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var stored []uuid.UUID
	position := 0
	for rows.Next() {
		var target uuid.UUID
		var gotPosition int
		if err := rows.Scan(&target, &gotPosition); err != nil {
			t.Fatal(err)
		}
		if gotPosition != position {
			t.Fatalf("edge position = %d, want %d", gotPosition, position)
		}
		position++
		stored = append(stored, target)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored, []uuid.UUID{secondTarget, firstTarget}) {
		t.Fatalf("stored relation order = %#v", stored)
	}
}

func TestRecordOperationsEmitBoundedRelationWriteExpandAndDeleteTelemetry(t *testing.T) {
	_, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	createRelationCollections(t, ctx, schemas, principal.ID)
	target := createRelationTarget(t, ctx, records, principal, "telemetry")
	recorder := &relationTelemetryRecorder{}
	records.recorder = recorder

	created, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "relation-telemetry-create", Collection: "articles",
		Input: map[string]json.RawMessage{
			"title": json.RawMessage(`"telemetry"`),
			"owner": relationUUIDJSON(target),
			"tags":  relationUUIDArrayJSON(target),
		},
	})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	recordID := uuid.MustParse(created.Record.Data["id"].(string))
	if _, err := records.Get(ctx, GetRequest{
		Principal: principal, RequestID: "relation-telemetry-expand", Collection: "articles", ID: recordID, Expand: "owner",
	}); err != nil {
		t.Fatalf("Get(expand): %v", err)
	}
	if _, err := records.Delete(ctx, DeleteRequest{
		Principal: principal, RequestID: "relation-telemetry-delete", Collection: "articles", ID: recordID,
	}); err != nil {
		t.Fatalf("Delete(): %v", err)
	}

	events := recorder.snapshot()
	want := []observability.Operation{
		observability.OperationRelationWrite,
		observability.OperationRelationExpand,
		observability.OperationRelationDelete,
	}
	if len(events) != len(want) {
		t.Fatalf("relation telemetry = %#v, want %d events", events, len(want))
	}
	for index, event := range events {
		if event.Component() != observability.ComponentRelation || event.Operation() != want[index] ||
			event.Outcome() != observability.OutcomeSuccess || event.Duration() < 0 {
			t.Fatalf("relation telemetry[%d] = %#v", index, event)
		}
	}
}

func TestRecordOperationsRelationFilterSortUsesAuthenticatedPolicySeam(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	createRelationCollections(t, ctx, schemas, principal.ID)
	first := createRelationTarget(t, ctx, records, principal, "Ada")
	second := createRelationTarget(t, ctx, records, principal, "Bob")
	firstArticle, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "relation-query-create-a", Collection: "articles",
		Input: map[string]json.RawMessage{
			"title": json.RawMessage(`"first"`), "owner": relationUUIDJSON(first), "tags": relationUUIDArrayJSON(second),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	secondArticle, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "relation-query-create-b", Collection: "articles",
		Input: map[string]json.RawMessage{
			"title": json.RawMessage(`"second"`), "owner": relationUUIDJSON(second), "tags": relationUUIDArrayJSON(first),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	testutil.RetireRecordMembershipV4(t, ctx, pool)
	filtered, err := records.List(ctx, ListRequest{
		Principal: principal, RequestID: "relation-query-filter", Collection: "articles", Limit: 10,
		Filter: "tags = '" + first.String() + "'", Sort: "owner.name",
	})
	if err != nil {
		t.Fatalf("relation filter/sort: %v", err)
	}
	if got := relationRecordIDs(filtered.Items); !reflect.DeepEqual(got, []string{secondArticle.Record.Data["id"].(string)}) {
		t.Fatalf("relation filter/sort ids = %v", got)
	}

	pageOne, err := records.List(ctx, ListRequest{
		Principal: principal, RequestID: "relation-query-page-a", Collection: "articles", Limit: 1, Sort: "owner.name",
	})
	if err != nil {
		t.Fatal(err)
	}
	pageTwo, err := records.List(ctx, ListRequest{
		Principal: principal, RequestID: "relation-query-page-b", Collection: "articles", Cursor: pageOne.NextCursor,
		Limit: 1, Sort: "owner.name",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{pageOne.Items[0]["id"].(string), pageTwo.Items[0]["id"].(string)}; !reflect.DeepEqual(got, []string{
		firstArticle.Record.Data["id"].(string), secondArticle.Record.Data["id"].(string),
	}) {
		t.Fatalf("relation sort pages = %v", got)
	}
}

func TestRecordOperationsForwardExpandKeepsRootValidatorAndOrderedRawIDs(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	createRelationCollections(t, ctx, schemas, principal.ID)

	firstTarget := createRelationTarget(t, ctx, records, principal, "expand-first")
	secondTarget := createRelationTarget(t, ctx, records, principal, "expand-second")
	created, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "relation-expand-create", Collection: "articles",
		Input: map[string]json.RawMessage{
			"title": json.RawMessage(`"expanded"`),
			"owner": relationUUIDJSON(firstTarget),
			"tags":  relationUUIDArrayJSON(secondTarget, firstTarget),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	recordID := uuid.MustParse(created.Record.Data["id"].(string))
	raw, err := records.Get(ctx, GetRequest{
		Principal: principal, RequestID: "relation-expand-raw", Collection: "articles", ID: recordID,
	})
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := records.Get(ctx, GetRequest{
		Principal: principal, RequestID: "relation-expand-get", Collection: "articles", ID: recordID,
		Expand: "owner,tags",
	})
	if err != nil {
		t.Fatal(err)
	}
	if expanded.ETag != raw.ETag || expanded.Version != raw.Version {
		t.Fatalf("expand changed root validator: raw=%#v expanded=%#v", raw, expanded)
	}
	assertRelationRecord(t, expanded.Data, firstTarget, []uuid.UUID{secondTarget, firstTarget})
	expand, ok := expanded.Data["expand"].(map[string]any)
	if !ok {
		t.Fatalf("expand object = %#v", expanded.Data["expand"])
	}
	owner, ok := expand["owner"].(map[string]any)
	if !ok || owner["id"] != firstTarget.String() || owner["name"] != "expand-first" {
		t.Fatalf("expanded owner = %#v", expand["owner"])
	}
	tags, ok := expand["tags"].([]map[string]any)
	if !ok || len(tags) != 2 || tags[0]["id"] != secondTarget.String() || tags[1]["id"] != firstTarget.String() {
		t.Fatalf("expanded ordered tags = %#v", expand["tags"])
	}

	targetBefore, err := records.Get(ctx, GetRequest{
		Principal: principal, RequestID: "relation-expand-target-before", Collection: "people", ID: firstTarget,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := records.Update(ctx, UpdateRequest{
		Principal: principal, RequestID: "relation-expand-target-update", Collection: "people", ID: firstTarget,
		IfMatch: EntityTagSet{Present: true, Values: []string{targetBefore.ETag}},
		Input:   map[string]json.RawMessage{"name": json.RawMessage(`"changed-target"`)},
	}); err != nil {
		t.Fatal(err)
	}
	afterTargetChange, err := records.Get(ctx, GetRequest{
		Principal: principal, RequestID: "relation-expand-after-target", Collection: "articles", ID: recordID,
		Expand: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if afterTargetChange.ETag != raw.ETag || afterTargetChange.Version != raw.Version {
		t.Fatalf("target-only change invalidated root: before=%#v after=%#v", raw, afterTargetChange)
	}
	afterExpand := afterTargetChange.Data["expand"].(map[string]any)
	if afterExpand["owner"].(map[string]any)["name"] != "changed-target" {
		t.Fatalf("target update missing from expansion: %#v", afterExpand["owner"])
	}

	testutil.RetireRecordMembershipV4(t, ctx, pool)
	rawPage, err := records.List(ctx, ListRequest{
		Principal: principal, RequestID: "relation-expand-list-raw", Collection: "articles", Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	expandedPage, err := records.List(ctx, ListRequest{
		Principal: principal, RequestID: "relation-expand-list", Collection: "articles", Limit: 1, Expand: "owner,tags",
	})
	if err != nil {
		t.Fatal(err)
	}
	if expandedPage.NextCursor != rawPage.NextCursor || len(expandedPage.Items) != 1 || expandedPage.Items[0]["expand"] == nil {
		t.Fatalf("expanded list page = %#v, raw cursor=%q", expandedPage, rawPage.NextCursor)
	}
}

func TestRecordOperationsRelationValidationAndMissingTargetRollback(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	createRelationCollections(t, ctx, schemas, principal.ID)
	target := createRelationTarget(t, ctx, records, principal, "target")

	_, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "relation-duplicate", Collection: "articles",
		Input: map[string]json.RawMessage{
			"title": json.RawMessage(`"duplicate"`),
			"tags":  relationUUIDArrayJSON(target, target),
		},
	})
	assertQueryValidation(t, err, "tags", "duplicate_relation_value")

	var before int
	if err := pool.QueryRow(ctx, `select count(*) from app.articles`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	missing := uuid.MustParse("ffffffff-ffff-4fff-8fff-ffffffffffff")
	_, err = records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "relation-missing-01", Collection: "articles",
		Input: map[string]json.RawMessage{
			"title": json.RawMessage(`"missing"`),
			"owner": relationUUIDJSON(missing),
			"tags":  relationUUIDArrayJSON(target),
		},
	})
	assertQueryValidation(t, err, "owner", "relation_target_invalid")
	var after int
	if err := pool.QueryRow(ctx, `select count(*) from app.articles`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("missing target left %d records, before %d", after, before)
	}
}

func TestRecordOperationsRelationListBoundsFinalResponseEnvelope(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	createRelationCollections(t, ctx, schemas, principal.ID)

	// Individually valid request bodies fill the 48 MiB page. The tail row forces
	// a cursor, whose envelope used to push the
	// actual JSON response over the advertised bound.
	const largeRecords = 50
	if _, err := pool.Exec(ctx, `insert into app.articles(id,created_at,updated_at,title)
		select md5('relation-page-large-' || value::text)::uuid, now(), now(), repeat('x',1048360)
		from generate_series(1,$1::integer) generated(value)`, largeRecords); err != nil {
		t.Fatalf("create relation page fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `insert into app.articles(id,created_at,updated_at,title)
		values(md5('relation-page-tail')::uuid,now(),now(),'tail')`); err != nil {
		t.Fatalf("create relation page tail: %v", err)
	}

	testutil.RetireRecordMembershipV4(t, ctx, pool)
	page, err := records.List(ctx, ListRequest{
		Principal: principal, RequestID: "relation-page-list", Collection: "articles", Limit: largeRecords,
	})
	if err != nil {
		t.Fatalf("list relation page: %v", err)
	}
	if page.NextCursor == "" {
		t.Fatal("byte-bound fixture did not produce a continuation cursor")
	}
	if len(page.Items) >= largeRecords {
		t.Fatalf("byte-bound fixture emitted %d items, want fewer than %d", len(page.Items), largeRecords)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	const maximumResponseBytes = 48<<20 + 4<<10
	if got := len(encoded) + 1; got > maximumResponseBytes { // writeJSON appends one newline.
		t.Fatalf("final relation page response = %d bytes, limit = %d", got, maximumResponseBytes)
	} else if got < maximumResponseBytes-(64<<10) {
		t.Fatalf("final relation page response = %d bytes, want within 64 KiB of %d", got, maximumResponseBytes)
	}
	seen := make(map[string]struct{}, largeRecords+1)
	for _, record := range page.Items {
		seen[record["id"].(string)] = struct{}{}
	}
	next, err := records.List(ctx, ListRequest{
		Principal: principal, RequestID: "relation-page-next", Collection: "articles", Limit: largeRecords,
		Cursor: page.NextCursor,
	})
	if err != nil {
		t.Fatalf("continue bounded relation page: %v", err)
	}
	for _, record := range next.Items {
		seen[record["id"].(string)] = struct{}{}
	}
	if len(seen) != largeRecords+1 || next.NextCursor != "" {
		t.Fatalf("bounded cursor pages returned %d unique records, trailing cursor = %q", len(seen), next.NextCursor)
	}
}

func TestRecordOperationsRelationListClassifiesStoredDriftWithoutDisclosure(t *testing.T) {
	pool, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	createRelationCollections(t, ctx, schemas, principal.ID)
	target := createRelationTarget(t, ctx, records, principal, "drift")
	created, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "relation-drift-create", Collection: "articles",
		Input: map[string]json.RawMessage{
			"title": json.RawMessage(`"drift"`),
			"tags":  relationUUIDArrayJSON(target),
		},
	})
	if err != nil {
		t.Fatalf("create relation drift fixture: %v", err)
	}
	recordID := uuid.MustParse(created.Record.Data["id"].(string))
	physical := schema.NewRelationPhysicalNames("articles", "tags").MultiTable
	table := quotedRelationTable(physical)
	if _, err := pool.Exec(ctx, "alter table "+table+" disable trigger user"); err != nil {
		t.Fatalf("disable relation position trigger: %v", err)
	}
	if _, err := pool.Exec(ctx, "update "+table+" set position = 1 where source_id = $1", recordID); err != nil {
		t.Fatalf("inject relation position drift: %v", err)
	}

	testutil.RetireRecordMembershipV4(t, ctx, pool)
	_, err = records.List(ctx, ListRequest{
		Principal: principal, RequestID: "relation-drift-list", Collection: "articles", Limit: 10,
	})
	if !errors.Is(err, schema.ErrConflict) {
		t.Fatalf("relation storage drift error = %v, want schema conflict", err)
	}
	if strings.Contains(err.Error(), physical) || err.Error() != "hydrate relation values failed" {
		t.Fatalf("relation storage drift disclosed physical object: %q", err)
	}
}

func createRelationCollections(t *testing.T, ctx context.Context, schemas *schema.Service, administratorID uuid.UUID) {
	t.Helper()
	for _, input := range []schema.CreateCollectionInput{
		{Name: "people", Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText, Required: true, Indexed: true}}},
		{Name: "articles", Fields: []schema.FieldInput{
			{Name: "title", Type: schema.TypeText, Required: true},
			{Name: "owner", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "people", Cardinality: schema.RelationSingle, OnDelete: schema.RelationRestrict,
			}},
			{Name: "tags", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "people", Cardinality: schema.RelationMultiple,
				MinSelect: relationInt(0), MaxSelect: relationInt(3), OnDelete: schema.RelationRestrict,
			}},
		}},
	} {
		plan, err := schemas.Preview(ctx, schema.NewCreateChange(input))
		if err != nil {
			t.Fatalf("plan %s: %v", input.Name, err)
		}
		if _, err := schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(input), plan, administratorID)); err != nil {
			t.Fatalf("apply %s: %v", input.Name, err)
		}
	}
}

func createRelationTarget(t *testing.T, ctx context.Context, records *Service, principal Principal, name string) uuid.UUID {
	t.Helper()
	created, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "target-create-" + name, Collection: "people",
		Input: map[string]json.RawMessage{"name": json.RawMessage(`"` + name + `"`)},
	})
	if err != nil {
		t.Fatalf("create target %s: %v", name, err)
	}
	return uuid.MustParse(created.Record.Data["id"].(string))
}

func assertRelationRecord(t *testing.T, record map[string]any, owner uuid.UUID, tags []uuid.UUID) {
	t.Helper()
	if record["owner"] != owner.String() {
		t.Fatalf("owner = %#v, want %s", record["owner"], owner)
	}
	want := make([]string, len(tags))
	for index, id := range tags {
		want[index] = id.String()
	}
	if !reflect.DeepEqual(record["tags"], want) {
		t.Fatalf("tags = %#v, want %#v", record["tags"], want)
	}
}

func relationUUIDJSON(id uuid.UUID) json.RawMessage {
	encoded, _ := json.Marshal(id.String())
	return encoded
}

func relationUUIDArrayJSON(ids ...uuid.UUID) json.RawMessage {
	values := make([]string, len(ids))
	for index, id := range ids {
		values[index] = id.String()
	}
	encoded, _ := json.Marshal(values)
	return encoded
}

func relationInt(value int) *int { return &value }

func relationRecordIDs(records []map[string]any) []string {
	ids := make([]string, len(records))
	for index := range records {
		ids[index], _ = records[index]["id"].(string)
	}
	return ids
}

func quotedRelationTable(name string) string { return `"pgbase_relation".` + `"` + name + `"` }
