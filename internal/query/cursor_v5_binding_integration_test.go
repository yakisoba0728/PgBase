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
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func TestListContinuesSignedV5ScalarPageWithinBoundMembership(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "cursor_v5_scalar_records",
		Fields: []schema.FieldInput{
			{Name: "title", Type: schema.TypeText, Required: true, Indexed: true},
		},
	})

	ids := make([]uuid.UUID, 0, 3)
	for _, title := range []string{"alpha", "beta", "gamma"} {
		record, err := records.Create(ctx, "cursor_v5_scalar_records", map[string]json.RawMessage{
			"title": json.RawMessage(`"` + title + `"`),
		})
		if err != nil {
			t.Fatalf("Create(%s): %v", title, err)
		}
		ids = append(ids, uuid.MustParse(record["id"].(string)))
	}

	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	collection := catalog.Collections["cursor_v5_scalar_records"]
	installReadyMembershipFixture(t, ctx, pool, collection, ids)
	token := encodeScalarV5CursorFixture(t, records, catalog, collection, testReadPrincipal, ids[0], "alpha", 2)

	page, err := records.List(ctx, ListRequest{
		Principal: testReadPrincipal, Collection: collection.Name, Cursor: token, Limit: 10, Sort: "title",
	})
	if err != nil {
		t.Fatalf("List(v5 continuation): %v", err)
	}
	if got, want := recordIDs(page.Items), []string{ids[1].String()}; !reflect.DeepEqual(got, want) {
		t.Fatalf("v5 continuation ids = %v, want membership-bounded %v", got, want)
	}
	if page.NextCursor != "" {
		t.Fatalf("membership-bounded terminal page cursor = %q, want empty", page.NextCursor)
	}
}

func TestListKeepsV5BindingAndAbsoluteExpiryAcrossContinuationPages(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "cursor_v5_chain_records",
		Fields: []schema.FieldInput{
			{Name: "title", Type: schema.TypeText, Required: true, Indexed: true},
		},
	})
	ids := make([]uuid.UUID, 0, 4)
	for _, title := range []string{"alpha", "beta", "gamma", "omega"} {
		record, err := records.Create(ctx, "cursor_v5_chain_records", map[string]json.RawMessage{
			"title": json.RawMessage(`"` + title + `"`),
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, uuid.MustParse(record["id"].(string)))
	}
	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	collection := catalog.Collections["cursor_v5_chain_records"]
	installReadyMembershipFixture(t, ctx, pool, collection, ids)
	firstPage, err := records.List(ctx, ListRequest{
		Principal: testReadPrincipal, Collection: collection.Name, Limit: 1, Sort: "title",
	})
	if err != nil {
		t.Fatalf("List(cursorless first page): %v", err)
	}
	firstCursor, err := records.cursors.Decode(firstPage.NextCursor, collection.ID, time.Time{})
	if err != nil || firstCursor.Version != cursor.Version4 {
		t.Fatalf("cursorless first page issued %#v, %v; want explicit v4", firstCursor, err)
	}
	token := encodeScalarV5CursorFixture(t, records, catalog, collection, testReadPrincipal, ids[0], "alpha", 4)
	initial, err := records.cursors.Decode(token, collection.ID, time.Time{})
	if err != nil {
		t.Fatalf("Decode(initial v5 cursor): %v", err)
	}
	wantExpiry := initial.ExpiresAt

	for pageNumber, wantID := range ids[1:] {
		page, err := records.List(ctx, ListRequest{
			Principal: testReadPrincipal, Collection: collection.Name, Cursor: token, Limit: 1, Sort: "title",
		})
		if err != nil {
			t.Fatalf("List(v5 page %d): %v", pageNumber+2, err)
		}
		if got := recordIDs(page.Items); !reflect.DeepEqual(got, []string{wantID.String()}) {
			t.Fatalf("v5 page %d ids = %v, want %s", pageNumber+2, got, wantID)
		}
		if pageNumber == len(ids)-2 {
			if page.NextCursor != "" {
				t.Fatalf("terminal v5 page cursor = %q, want empty", page.NextCursor)
			}
			continue
		}
		decoded, err := records.cursors.Decode(page.NextCursor, collection.ID, time.Time{})
		if err != nil {
			t.Fatalf("Decode(v5 page %d cursor): %v", pageNumber+2, err)
		}
		if decoded.Version != cursor.Version5 || decoded.V5 == nil || decoded.V5.HighWater != 4 ||
			decoded.V5.MembershipGeneration != 1 || !decoded.ExpiresAt.Equal(wantExpiry) {
			t.Fatalf("v5 page %d continuation = %#v", pageNumber+2, decoded)
		}
		token = page.NextCursor
	}
}

func TestListRejectsV5ContinuationOutsideItsSignedBindings(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "cursor_v5_binding_records",
		Fields: []schema.FieldInput{
			{Name: "title", Type: schema.TypeText, Required: true, Indexed: true},
		},
	})
	ids := make([]uuid.UUID, 0, 2)
	for _, title := range []string{"alpha", "beta"} {
		record, err := records.Create(ctx, "cursor_v5_binding_records", map[string]json.RawMessage{
			"title": json.RawMessage(`"` + title + `"`),
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, uuid.MustParse(record["id"].(string)))
	}
	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	collection := catalog.Collections["cursor_v5_binding_records"]
	installReadyMembershipFixture(t, ctx, pool, collection, ids)
	valid := encodeScalarV5CursorFixture(t, records, catalog, collection, testReadPrincipal, ids[0], "alpha", 2)
	otherPrincipal := Principal{Kind: PrincipalAdministrator, ID: uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")}

	tests := []struct {
		name      string
		mutate    func(*cursor.Page)
		principal Principal
		filter    string
		sort      string
	}{
		{name: "catalog", mutate: func(page *cursor.Page) { page.V5.CatalogVersion++ }},
		{name: "membership generation", mutate: func(page *cursor.Page) { page.V5.MembershipGeneration++ }},
		{name: "query", mutate: func(page *cursor.Page) { page.V5.QueryHash[0] ^= 0xff }},
		{name: "scope principal", principal: otherPrincipal},
		{name: "filter", filter: "title >= 'beta'"},
		{name: "order", sort: "-title"},
		{name: "order descriptor", mutate: func(page *cursor.Page) { page.V5.Order[0].Desc = true }},
		{name: "high water above counter", mutate: func(page *cursor.Page) { page.V5.HighWater = 3 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			principal := test.principal
			if !principal.Valid() {
				principal = testReadPrincipal
			}
			sortText := test.sort
			if sortText == "" {
				sortText = "title"
			}
			token := valid
			if test.mutate != nil {
				page, err := records.cursors.Decode(valid, collection.ID, time.Time{})
				if err != nil {
					t.Fatal(err)
				}
				test.mutate(&page)
				token, err = records.cursors.Encode(page)
				if err != nil {
					t.Fatalf("Encode(mutated v5 cursor): %v", err)
				}
			}
			_, err := records.List(ctx, ListRequest{
				Principal: principal, Collection: collection.Name, Cursor: token,
				Limit: 1, Filter: test.filter, Sort: sortText,
			})
			if !IsInvalidCursor(err) {
				t.Fatalf("List(binding mismatch) error = %v, want invalid cursor", err)
			}
		})
	}

	if _, err := pool.Exec(ctx, `delete from pgbase_meta.record_membership_capability where singleton`); err != nil {
		t.Fatal(err)
	}
	if _, err := records.List(ctx, ListRequest{
		Principal: testReadPrincipal, Collection: collection.Name, Cursor: valid, Limit: 1, Sort: "title",
	}); !IsInvalidCursor(err) {
		t.Fatalf("List(with unavailable durable capability) error = %v, want invalid cursor", err)
	}
}

func TestListContinuesAnchorOnlyV5RelationPageWithinBoundMembership(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	fixture := createRelationFilterSortFixture(t, ctx, pool, schemas, records)
	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	posts := catalog.Collections["relation_query_posts"]
	people := catalog.Collections["relation_query_people"]
	installReadyMembershipFixture(t, ctx, pool, posts, []uuid.UUID{fixture.postAda, fixture.postBob})
	expiresAt := databaseCursorExpiryFixture(t, records)

	encode := func(highWater int64) string {
		t.Helper()
		var ownerField, nameField schema.Field
		for _, field := range posts.Fields {
			if field.Name == "owner" {
				ownerField = field
			}
		}
		for _, field := range people.Fields {
			if field.Name == "name" {
				nameField = field
			}
		}
		nameJSON, _ := json.Marshal("Ada")
		idJSON, _ := json.Marshal(fixture.postAda.String())
		token, err := records.cursors.Encode(cursor.Page{
			Version: cursor.Version5, CollectionID: posts.ID,
			Values: []json.RawMessage{nameJSON, idJSON}, AnchorID: fixture.postAda,
			PrivateValues: true, ExpiresAt: expiresAt,
			V5: &cursor.V5Descriptor{
				CatalogVersion: catalog.Version, MembershipGeneration: 1, HighWater: highWater,
				QueryHash: records.cursors.QueryFingerprint([]byte(`{"filter":null}`)),
				ScopeHash: records.cursors.ScopeFingerprint([]byte(
					`{"kind":"administrator","id":"` + testReadPrincipal.ID.String() + `","policy":"administrator-all-v1"}`,
				)),
				Order: []cursor.V5OrderTerm{
					{Path: []string{ownerField.ID.String(), nameField.ID.String()}, Type: "text", Nulls: cursor.NullsLast},
					{Path: []string{"$id"}, Type: "uuid", Nulls: cursor.NullsLast},
				},
			},
		})
		if err != nil {
			t.Fatalf("Encode(anchor-only relation v5): %v", err)
		}
		decoded, err := records.cursors.Decode(token, posts.ID, time.Time{})
		if err != nil || len(decoded.Values) != 0 || decoded.AnchorID != fixture.postAda {
			t.Fatalf("relation cursor is not anchor-only: %#v, %v", decoded, err)
		}
		return token
	}

	page, err := records.List(ctx, ListRequest{
		Principal: testReadPrincipal, Collection: posts.Name, Cursor: encode(2), Limit: 10, Sort: "owner.name",
	})
	if err != nil {
		t.Fatalf("List(anchor-only relation v5): %v", err)
	}
	if got := recordIDs(page.Items); !reflect.DeepEqual(got, []string{fixture.postBob.String()}) {
		t.Fatalf("anchor-only relation continuation ids = %v, want %s", got, fixture.postBob)
	}

	bounded, err := records.List(ctx, ListRequest{
		Principal: testReadPrincipal, Collection: posts.Name, Cursor: encode(1), Limit: 10, Sort: "owner.name",
	})
	if err != nil {
		t.Fatalf("List(anchor-only bounded relation v5): %v", err)
	}
	if len(bounded.Items) != 0 || bounded.NextCursor != "" {
		t.Fatalf("relation high-water page = %#v, want terminal empty page", bounded)
	}
}

func TestListPreservesV5HighWaterAcrossSequentialHydrationBatches(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "cursor_v5_batch_targets",
		Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText, Required: true},
		},
	})
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "cursor_v5_batch_roots",
		Fields: []schema.FieldInput{
			{Name: "title", Type: schema.TypeText, Required: true, Indexed: true},
			{Name: "targets", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "cursor_v5_batch_targets", Cardinality: schema.RelationMultiple,
				MinSelect: relationFilterInt(0), MaxSelect: relationFilterInt(4), OnDelete: schema.RelationRestrict,
			}},
		},
	})
	ids := make([]uuid.UUID, 0, 24)
	for index := range 24 {
		title := "record-" + string(rune('a'+index))
		record, err := records.Create(ctx, "cursor_v5_batch_roots", map[string]json.RawMessage{
			"title": json.RawMessage(`"` + title + `"`),
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, uuid.MustParse(record["id"].(string)))
	}
	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	collection := catalog.Collections["cursor_v5_batch_roots"]
	installReadyMembershipFixture(t, ctx, pool, collection, ids)
	token := encodeScalarV5CursorFixture(t, records, catalog, collection, testReadPrincipal, ids[0], "record-a", 18)

	page, err := records.List(ctx, ListRequest{
		Principal: testReadPrincipal, Collection: collection.Name, Cursor: token, Limit: 20, Sort: "title",
	})
	if err != nil {
		t.Fatalf("List(v5 multi-batch continuation): %v", err)
	}
	want := make([]string, 0, 17)
	for _, id := range ids[1:18] {
		want = append(want, id.String())
	}
	if got := recordIDs(page.Items); !reflect.DeepEqual(got, want) {
		t.Fatalf("multi-batch v5 continuation ids = %v, want %v", got, want)
	}
	if page.NextCursor != "" {
		t.Fatalf("multi-batch bounded page cursor = %q, want empty", page.NextCursor)
	}
}

func encodeScalarV5CursorFixture(
	t *testing.T,
	records *Service,
	catalog schema.Catalog,
	collection schema.Collection,
	principal Principal,
	anchorID uuid.UUID,
	anchorTitle string,
	highWater int64,
) string {
	t.Helper()
	var titleField schema.Field
	for _, field := range collection.Fields {
		if field.Name == "title" {
			titleField = field
			break
		}
	}
	if titleField.ID == uuid.Nil {
		t.Fatal("title field has no stable identity")
	}
	anchorTitleJSON, _ := json.Marshal(anchorTitle)
	anchorIDJSON, _ := json.Marshal(anchorID.String())
	queryHash := records.cursors.QueryFingerprint([]byte(`{"filter":null}`))
	scopeHash := records.cursors.ScopeFingerprint([]byte(
		`{"kind":"administrator","id":"` + principal.ID.String() + `","policy":"administrator-all-v1"}`,
	))
	token, err := records.cursors.Encode(cursor.Page{
		Version:      cursor.Version5,
		CollectionID: collection.ID,
		Values:       []json.RawMessage{anchorTitleJSON, anchorIDJSON},
		ExpiresAt:    databaseCursorExpiryFixture(t, records),
		V5: &cursor.V5Descriptor{
			CatalogVersion: catalog.Version, MembershipGeneration: 1,
			QueryHash: queryHash, ScopeHash: scopeHash, HighWater: highWater,
			Order: []cursor.V5OrderTerm{
				{Path: []string{titleField.ID.String()}, Type: string(schema.TypeText), Nulls: cursor.NullsLast},
				{Path: []string{"$id"}, Type: string(schema.TypeUUID), Nulls: cursor.NullsLast},
			},
		},
	})
	if err != nil {
		t.Fatalf("Encode(v5 fixture): %v", err)
	}
	return token
}

func databaseCursorExpiryFixture(t *testing.T, records *Service) time.Time {
	t.Helper()
	var databaseTime time.Time
	if err := records.pool.QueryRow(context.Background(), `select clock_timestamp()`).Scan(&databaseTime); err != nil {
		t.Fatalf("read database time: %v", err)
	}
	return databaseTime.UTC().Add(cursorTTL)
}

func installReadyMembershipFixture(
	t *testing.T,
	ctx context.Context,
	pool *database.Pool,
	collection schema.Collection,
	recordIDs []uuid.UUID,
) {
	t.Helper()
	err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var lastCreatedAt time.Time
		var lastRecordID uuid.UUID
		if err := tx.QueryRow(ctx, `select created_at, id from `+pgx.Identifier{"app", collection.TableName}.Sanitize()+`
			where id = any($1::uuid[]) order by created_at desc, id desc limit 1`, recordIDs).Scan(&lastCreatedAt, &lastRecordID); err != nil {
			return err
		}
		now := time.Date(2026, 9, 2, 3, 0, 0, 0, time.UTC)
		if _, err := tx.Exec(ctx, `update pgbase_meta.collection_creation_counters
			set generation = 1, last_ordinal = $2, updated_at = $3 where collection_id = $1`,
			collection.ID, len(recordIDs), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update pgbase_meta.record_membership_backfills set
			generation = 1, state = 'ready', cutover_count = $2,
			cutover_last_created_at = $3, cutover_last_record_id = $4,
			checkpoint_created_at = null, checkpoint_record_id = null,
			last_assigned_ordinal = $2, validated_base_count = $2,
			validated_sidecar_count = $2, updated_at = $5, completed_at = $5
			where collection_id = $1`, collection.ID, len(recordIDs), lastCreatedAt, lastRecordID, now); err != nil {
			return err
		}
		for index, recordID := range recordIDs {
			if _, err := tx.Exec(ctx, `insert into pgbase_meta.record_creation_ordinals
				(collection_id, record_id, ordinal) values ($1, $2, $3)`,
				collection.ID, recordID, index+1); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `update pgbase_meta.record_membership_capability set
			state = 'ready', generation = 1, minimum_writer_version = 1,
			cursor_issue_version = 4, cutover_at = $1, ready_at = $1,
			cutover_collection_count = 1, cutover_record_count = $2,
			ready_collection_count = 1, updated_at = $1 where singleton`, now, len(recordIDs))
		return err
	})
	if err != nil {
		t.Fatalf("install ready membership fixture: %v", err)
	}
}
