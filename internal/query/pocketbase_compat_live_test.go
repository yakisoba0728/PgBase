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

func TestPocketBaseCompatibilityLikeWildcardUsesPercent(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	fixture := createRelationFilterSortFixture(t, ctx, pool, schemas, records)
	request := ListRequest{Principal: Principal{Kind: PrincipalAdministrator, ID: uuid.New()}, Collection: "relation_query_posts", Limit: 20, Sort: "title", PocketBaseCompatibility: true}
	request.Filter = `title ~ 'post-%'`
	page, err := records.List(ctx, request)
	if err != nil || !reflect.DeepEqual(recordIDs(page.Items), []string{fixture.postAda.String(), fixture.postBob.String()}) {
		t.Fatalf("percent items=%v err=%v", recordIDs(page.Items), err)
	}
	request.Filter = `title ~ 'post-*'`
	page, err = records.List(ctx, request)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("literal star items=%v err=%v", recordIDs(page.Items), err)
	}
}

func TestPocketBaseCompatibilityFiltersFieldToField(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	fixture := createRelationFilterSortFixture(t, ctx, pool, schemas, records)
	page, err := records.List(ctx, ListRequest{Principal: Principal{Kind: PrincipalAdministrator, ID: uuid.New()}, Collection: "relation_query_posts", Limit: 20, Filter: `title = title`, Sort: "title", PocketBaseCompatibility: true})
	if err != nil || !reflect.DeepEqual(recordIDs(page.Items), []string{fixture.postAda.String(), fixture.postBob.String()}) {
		t.Fatalf("field comparison items=%v err=%v", recordIDs(page.Items), err)
	}
}

func TestPocketBaseCompatibilitySortsReverseRelationPath(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	fixture := createRelationFilterSortFixture(t, ctx, pool, schemas, records)
	page, err := records.List(ctx, ListRequest{Principal: Principal{Kind: PrincipalAdministrator, ID: uuid.New()}, Collection: "relation_query_people", Limit: 20, Sort: "relation_query_posts_via_owner.title", PocketBaseCompatibility: true})
	if err != nil || !reflect.DeepEqual(recordIDs(page.Items), []string{fixture.ada.String(), fixture.bob.String()}) {
		t.Fatalf("reverse sort items=%v err=%v", recordIDs(page.Items), err)
	}
}

func TestPocketBaseCompatibilitySkipsInvalidAndMissingExpandPathsWithStableSortTies(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	fixture := createRelationFilterSortFixture(t, ctx, pool, schemas, records)
	if _, err := records.Update(ctx, "relation_query_posts", fixture.postAda, map[string]json.RawMessage{"amount": json.RawMessage(`"0.0"`)}); err != nil {
		t.Fatal(err)
	}
	request := ListRequest{Principal: Principal{Kind: PrincipalAdministrator, ID: uuid.New()}, Collection: "relation_query_posts", Limit: 1, Sort: "amount", Expand: "owner,missing_relation,owner.missing", PocketBaseCompatibility: true}
	first, err := records.ListExpanded(ctx, request)
	if err != nil || len(first.Items) != 1 || first.Items[0].Expand["owner"] == nil || first.NextCursor == "" {
		t.Fatalf("lenient first=%#v err=%v", first, err)
	}
	request.Cursor = first.NextCursor
	second, err := records.ListExpanded(ctx, request)
	if err != nil || len(second.Items) != 1 || second.NextCursor != "" {
		t.Fatalf("lenient second=%#v err=%v", second, err)
	}
	got := expandedRootIDs([]ExpandedRecord{first.Items[0], second.Items[0]})
	want := []string{fixture.postAda.String(), fixture.postBob.String()}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stable amount ties=%v want=%v", got, want)
	}
	missing, err := records.ListExpanded(ctx, ListRequest{Principal: request.Principal, Collection: "relation_query_posts", Limit: 20, Expand: "missing_relation", PocketBaseCompatibility: true})
	if err != nil || len(missing.Items) != 2 || len(missing.Items[0].Expand) != 0 {
		t.Fatalf("missing expand=%#v err=%v", missing, err)
	}
}
