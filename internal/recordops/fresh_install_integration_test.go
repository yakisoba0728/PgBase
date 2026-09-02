//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"testing"
)

// A brand new install has migrated cleanly and has no data. Listing records is
// the most ordinary thing an administrator does next.
func TestFreshInstallCanListRecords(t *testing.T) {
	_, _, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	if _, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "fresh-install-1", Collection: "records",
		IdempotencyKey: "fresh-install-0001",
		Input:          map[string]json.RawMessage{"title": json.RawMessage(`"first"`)},
	}); err != nil {
		t.Fatalf("create on a fresh install: %v", err)
	}
	page, err := records.List(ctx, ListRequest{
		Principal: principal, RequestID: "fresh-install-list-1", Collection: "records", Limit: 10,
	})
	if err != nil {
		t.Fatalf("list on a fresh install: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("fresh install list = %#v", page)
	}
}
