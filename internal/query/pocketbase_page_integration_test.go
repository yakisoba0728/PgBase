//go:build integration

package query

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestPocketBaseListPageCountFilterAndSnapshot(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: "pb_pages", Fields: []schema.FieldInput{
		{Name: "title", Type: schema.TypeText, Required: true},
		{Name: "rank", Type: schema.TypeInteger, Required: true, Indexed: true},
	}})
	ctx := context.Background()
	for rank := 1; rank <= 5; rank++ {
		if _, err := records.Create(ctx, "pb_pages", map[string]json.RawMessage{
			"title": json.RawMessage(fmt.Sprintf(`"row-%d"`, rank)),
			"rank":  json.RawMessage(fmt.Sprintf(`%d`, rank)),
		}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := pool.Exec(ctx, `insert into app.pb_pages(id,title,rank,created_at,updated_at) values($1,'untracked',99,current_timestamp,current_timestamp)`, uuid.New()); err != nil {
		t.Fatal(err)
	}

	records.countPageHook = func() {
		records.countPageHook = nil
		if _, err := records.Create(ctx, "pb_pages", map[string]json.RawMessage{
			"title": json.RawMessage(`"inserted-after-count"`),
			"rank":  json.RawMessage(`0`),
		}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := ListPocketBase(ctx, records, PocketBaseListRequest{
		ListRequest: ListRequest{
			Principal:  testReadPrincipal,
			Collection: "pb_pages",
			Filter:     `title != "excluded"`,
			Sort:       "rank",
		},
		Page: 2, PerPage: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Page != 2 || page.PerPage != 2 || page.TotalItems != 5 || page.TotalPages != 3 || len(page.Items) != 2 {
		t.Fatalf("page = %#v", page)
	}
	if fmt.Sprint(page.Items[0]["rank"]) != "3" || fmt.Sprint(page.Items[1]["rank"]) != "4" {
		t.Fatalf("page ranks = %#v", []any{page.Items[0]["rank"], page.Items[1]["rank"]})
	}
	var current int
	if err := pool.QueryRow(ctx, `select count(*) from app.pb_pages`).Scan(&current); err != nil || current != 7 {
		t.Fatalf("current count=%d err=%v", current, err)
	}
}

func TestPocketBaseListSkipTotalAvoidsCountAndRandomSortRuns(t *testing.T) {
	_, schemas, records := newRecordServiceTest(t)
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: "pb_skip_total", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText, Required: true}}})
	ctx := context.Background()
	for index := range 4 {
		if _, err := records.Create(ctx, "pb_skip_total", map[string]json.RawMessage{"title": json.RawMessage(fmt.Sprintf(`"row-%d"`, index))}); err != nil {
			t.Fatal(err)
		}
	}
	counted := false
	records.countPageHook = func() { counted = true }
	page, err := ListPocketBase(ctx, records, PocketBaseListRequest{
		ListRequest: ListRequest{Principal: testReadPrincipal, Collection: "pb_skip_total", Sort: "@random"},
		Page:        1, PerPage: 3, SkipTotal: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if counted || page.TotalItems != -1 || page.TotalPages != -1 || len(page.Items) != 3 {
		t.Fatalf("skip total page=%#v countHook=%v", page, counted)
	}
	for _, item := range page.Items {
		if _, err := uuid.Parse(item["id"].(string)); err != nil {
			t.Fatalf("random result id=%#v", item["id"])
		}
	}
}

func TestPocketBaseOffsetPageDoesNotSkipByteLimitedRows(t *testing.T) {
	_, schemas, records := newRecordServiceTest(t)
	const collection = "pb_page_budget"
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: collection, Fields: []schema.FieldInput{
		{Name: "title", Type: schema.TypeText, Indexed: true},
		{Name: "body", Type: schema.TypeJSONB},
	}})
	ctx := t.Context()
	for index := range 4 {
		// Individually valid rows that together cross the escaped JSON budget.
		if _, err := records.Create(ctx, collection, map[string]json.RawMessage{
			"title": json.RawMessage(fmt.Sprintf(`"item-%d"`, index)),
			"body":  json.RawMessage(`"` + strings.Repeat("<", 3<<20) + `"`),
		}); err != nil {
			t.Fatal(err)
		}
	}
	request := PocketBaseListRequest{
		ListRequest: ListRequest{Principal: testReadPrincipal, Collection: collection, Sort: "title"},
		Page:        1, PerPage: 3, SkipTotal: true,
	}
	_, err := ListPocketBase(ctx, records, request)
	assertValidationDetail(t, err, "response", "response_too_large")

	request.Sort = "@random"
	_, err = ListPocketBase(ctx, records, request)
	assertValidationDetail(t, err, "response", "response_too_large")

	request.Sort, request.Fields, request.SkipTotal = "title", "title", false
	first, err := ListPocketBase(ctx, records, request)
	if err != nil || first.TotalItems != 4 || len(first.Items) != 3 || first.Items[2]["title"] != "item-2" {
		t.Fatalf("projected first page: items=%v total=%d err=%v", first.Items, first.TotalItems, err)
	}
	request.Page = 2
	next, err := ListPocketBase(ctx, records, request)
	if err != nil || len(next.Items) != 1 || next.Items[0]["title"] != "item-3" {
		t.Fatalf("projected next page: items=%v err=%v", next.Items, err)
	}
}
