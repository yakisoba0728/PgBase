//go:build integration

package recordops

import (
	"context"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestListTotalIsOptInAuthorizedAndBounded(t *testing.T) {
	f := newUserAccessFixture(t)
	ctx := context.Background()
	f.enable(t, "records")
	f.create(t, f.first, "mine", "list-total-mine-0001")
	f.create(t, f.second, "theirs", "list-total-theirs-01")
	page, err := f.records.List(ctx, ListRequest{Principal: f.admin, RequestID: "list-total-default", Collection: "records", Limit: 1})
	if err != nil || page.TotalItems != nil {
		t.Fatalf("default page total = %v, %v", page.TotalItems, err)
	}
	page, err = f.records.List(ctx, ListRequest{Principal: f.first, RequestID: "list-total-owner", Collection: "records", Limit: 1, Total: true})
	if err != nil || page.TotalItems == nil || *page.TotalItems != 1 || page.TotalCapped {
		t.Fatalf("owner total = %v capped=%v, %v", page.TotalItems, page.TotalCapped, err)
	}
	page, err = f.records.List(ctx, ListRequest{Principal: f.admin, RequestID: "list-total-filter", Collection: "records", Limit: 1, Filter: "title = 'theirs'", Total: true})
	if err != nil || *page.TotalItems != 1 {
		t.Fatalf("filtered total = %v, %v", page.TotalItems, err)
	}
	if _, err := f.pool.Exec(ctx, `insert into app.records(id,created_at,updated_at,title) select gen_random_uuid(),now(),now(),'bulk-'||g from generate_series(1,10000) g`); err != nil {
		t.Fatal(err)
	}
	testutil.RetireRecordMembershipV4(t, ctx, f.pool)
	page, err = f.records.List(ctx, ListRequest{Principal: f.admin, RequestID: "list-total-capped", Collection: "records", Limit: 2, Total: true})
	if err != nil || *page.TotalItems != 10000 || !page.TotalCapped || len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("capped total = %v capped=%v items=%d, %v", *page.TotalItems, page.TotalCapped, len(page.Items), err)
	}
	page, err = f.records.List(ctx, ListRequest{Principal: f.admin, RequestID: "list-total-capped-filter", Collection: "records", Limit: 2, Filter: "title ~ 'bulk-1'", Total: true})
	if err != nil || *page.TotalItems != 1112 || page.TotalCapped {
		t.Fatalf("filtered bulk total = %v capped=%v, %v", page.TotalItems, page.TotalCapped, err)
	}
}
