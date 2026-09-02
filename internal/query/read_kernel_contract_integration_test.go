//go:build integration

package query

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/google/uuid"
)

func TestReadKernelListPreservesRootIdentityAndCursorAcrossScalarRelationAndExpandPlans(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	fixture := createRelationFilterSortFixture(t, ctx, pool, schemas, records)
	principal := Principal{
		Kind: PrincipalAdministrator,
		ID:   uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
	}
	collectionID := mustCollectionID(t, schemas, "relation_query_posts")

	t.Run("scalar root plan", func(t *testing.T) {
		legacy, err := records.listScalar(ctx, "relation_query_posts", "", 1, "", "title")
		if err != nil {
			t.Fatalf("List(): %v", err)
		}
		relationAware, err := records.ListRelationAware(ctx, RelationListRequest{
			Principal: principal, Collection: "relation_query_posts", Limit: 1, Sort: "title",
		})
		if err != nil {
			t.Fatalf("ListRelationAware(): %v", err)
		}
		expanded, err := records.ListExpanded(ctx, ListRequest{
			Principal: principal, Collection: "relation_query_posts", Limit: 1,
			Sort: "title", Expand: "owner.manager",
		})
		if err != nil {
			t.Fatalf("ListExpanded(): %v", err)
		}

		wantFirst := []string{fixture.postAda.String()}
		if got := recordIDs(legacy.Items); !reflect.DeepEqual(got, wantFirst) {
			t.Fatalf("legacy root ids = %v, want %v", got, wantFirst)
		}
		if got := recordIDs(relationAware.Items); !reflect.DeepEqual(got, wantFirst) {
			t.Fatalf("relation-aware root ids = %v, want %v", got, wantFirst)
		}
		if got := expandedRootIDs(expanded.Items); !reflect.DeepEqual(got, wantFirst) {
			t.Fatalf("expanded root ids = %v, want %v", got, wantFirst)
		}
		if legacy.NextCursor == "" {
			t.Fatal("scalar root page did not issue a cursor")
		}
		assertEquivalentCursorIgnoringExpiry(t, records, collectionID, relationAware.NextCursor, legacy.NextCursor)
		assertEquivalentCursorIgnoringExpiry(t, records, collectionID, expanded.NextCursor, legacy.NextCursor)

		fromLegacy, err := records.ListExpanded(ctx, ListRequest{
			Principal: principal, Collection: "relation_query_posts", Cursor: legacy.NextCursor,
			Limit: 1, Sort: "title", Expand: "owner.manager",
		})
		if err != nil {
			t.Fatalf("ListExpanded(cursor from List): %v", err)
		}
		fromExpanded, err := records.listScalar(ctx, "relation_query_posts", expanded.NextCursor, 1, "", "title")
		if err != nil {
			t.Fatalf("List(cursor from ListExpanded): %v", err)
		}
		wantSecond := []string{fixture.postBob.String()}
		if got := expandedRootIDs(fromLegacy.Items); !reflect.DeepEqual(got, wantSecond) || fromLegacy.NextCursor != "" {
			t.Fatalf("expanded continuation ids/cursor = %v/%q, want %v/empty", got, fromLegacy.NextCursor, wantSecond)
		}
		if got := recordIDs(fromExpanded.Items); !reflect.DeepEqual(got, wantSecond) || fromExpanded.NextCursor != "" {
			t.Fatalf("scalar continuation ids/cursor = %v/%q, want %v/empty", got, fromExpanded.NextCursor, wantSecond)
		}
	})

	t.Run("relation root plan", func(t *testing.T) {
		raw, err := records.ListRelationAware(ctx, RelationListRequest{
			Principal: principal, Collection: "relation_query_posts", Limit: 1, Sort: "owner.name",
		})
		if err != nil {
			t.Fatalf("ListRelationAware(): %v", err)
		}
		expanded, err := records.ListExpanded(ctx, ListRequest{
			Principal: principal, Collection: "relation_query_posts", Limit: 1,
			Sort: "owner.name", Expand: "owner.manager",
		})
		if err != nil {
			t.Fatalf("ListExpanded(): %v", err)
		}

		wantFirst := []string{fixture.postAda.String()}
		if got := recordIDs(raw.Items); !reflect.DeepEqual(got, wantFirst) {
			t.Fatalf("raw relation root ids = %v, want %v", got, wantFirst)
		}
		if got := expandedRootIDs(expanded.Items); !reflect.DeepEqual(got, wantFirst) {
			t.Fatalf("expanded relation root ids = %v, want %v", got, wantFirst)
		}
		if raw.NextCursor == "" {
			t.Fatal("relation root page did not issue a cursor")
		}
		assertEquivalentCursorIgnoringExpiry(t, records, collectionID, expanded.NextCursor, raw.NextCursor)

		fromRaw, err := records.ListExpanded(ctx, ListRequest{
			Principal: principal, Collection: "relation_query_posts", Cursor: raw.NextCursor,
			Limit: 1, Sort: "owner.name", Expand: "owner.manager",
		})
		if err != nil {
			t.Fatalf("ListExpanded(cursor from ListRelationAware): %v", err)
		}
		fromExpanded, err := records.ListRelationAware(ctx, RelationListRequest{
			Principal: principal, Collection: "relation_query_posts", Cursor: expanded.NextCursor,
			Limit: 1, Sort: "owner.name",
		})
		if err != nil {
			t.Fatalf("ListRelationAware(cursor from ListExpanded): %v", err)
		}
		wantSecond := []string{fixture.postBob.String()}
		if got := expandedRootIDs(fromRaw.Items); !reflect.DeepEqual(got, wantSecond) || fromRaw.NextCursor != "" {
			t.Fatalf("expanded relation continuation ids/cursor = %v/%q, want %v/empty", got, fromRaw.NextCursor, wantSecond)
		}
		if got := recordIDs(fromExpanded.Items); !reflect.DeepEqual(got, wantSecond) || fromExpanded.NextCursor != "" {
			t.Fatalf("raw relation continuation ids/cursor = %v/%q, want %v/empty", got, fromExpanded.NextCursor, wantSecond)
		}
	})
}

func TestReadKernelRelationDescendingSortCrossesDuplicateAndNullBoundariesWithoutGaps(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	fixture := createRelationFilterSortFixture(t, ctx, pool, schemas, records)
	principal := Principal{
		Kind: PrincipalAdministrator,
		ID:   uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
	}
	createPerson := func(name string) uuid.UUID {
		t.Helper()
		record, err := records.Create(ctx, "relation_query_people", map[string]json.RawMessage{
			"name": json.RawMessage(`"` + name + `"`),
		})
		if err != nil {
			t.Fatalf("create person %q: %v", name, err)
		}
		return uuid.MustParse(record["id"].(string))
	}
	createPost := func(title string, owner *uuid.UUID) string {
		t.Helper()
		input := map[string]json.RawMessage{"title": json.RawMessage(`"` + title + `"`)}
		if owner != nil {
			input["owner"] = relationFilterUUIDJSON(*owner)
		}
		record, err := records.Create(ctx, "relation_query_posts", input)
		if err != nil {
			t.Fatalf("create post %q: %v", title, err)
		}
		return record["id"].(string)
	}

	firstDuplicateOwner := createPerson("Zed")
	secondDuplicateOwner := createPerson("Zed")
	duplicateIDs := []string{
		createPost("zed-first", &firstDuplicateOwner),
		createPost("zed-second", &secondDuplicateOwner),
	}
	nullIDs := []string{
		createPost("null-first", nil),
		createPost("null-second", nil),
	}
	sort.Sort(sort.Reverse(sort.StringSlice(duplicateIDs)))
	sort.Sort(sort.Reverse(sort.StringSlice(nullIDs)))
	want := append([]string{}, duplicateIDs...)
	want = append(want, fixture.postBob.String(), fixture.postAda.String())
	want = append(want, nullIDs...)

	got := make([]string, 0, len(want))
	cursor := ""
	for pageNumber := 0; ; pageNumber++ {
		if pageNumber > len(want) {
			t.Fatalf("descending relation cursor did not terminate after %d pages", pageNumber)
		}
		page, err := records.ListRelationAware(ctx, RelationListRequest{
			Principal: principal, Collection: "relation_query_posts", Cursor: cursor,
			Limit: 1, Sort: "-owner.name",
		})
		if err != nil {
			t.Fatalf("ListRelationAware(page %d): %v", pageNumber, err)
		}
		if len(page.Items) != 1 {
			t.Fatalf("page %d items = %d, want 1", pageNumber, len(page.Items))
		}
		got = append(got, page.Items[0]["id"].(string))
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("descending relation cursor walk = %v, want %v", got, want)
	}
}
