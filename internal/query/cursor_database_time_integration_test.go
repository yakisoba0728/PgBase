//go:build integration

package query

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/cursor"
	"github.com/yakisoba0728/PgBase/internal/membership"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// A cursor's absolute lifetime is measured against the PostgreSQL clock the
// issuing transaction saw, never against this process's own clock, so a fleet
// whose nodes disagree about the time cannot lengthen or shorten a traversal.
func TestCursorLifetimeUsesPostgresTime(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "cursor_database_clock_records",
		Fields: []schema.FieldInput{
			{Name: "title", Type: schema.TypeText, Required: true, Indexed: true},
		},
	})

	ids := make([]uuid.UUID, 0, 2)
	for _, title := range []string{"alpha", "beta"} {
		record, err := records.Create(ctx, "cursor_database_clock_records", map[string]json.RawMessage{
			"title": json.RawMessage(`"` + title + `"`),
		})
		if err != nil {
			t.Fatalf("Create(%s): %v", title, err)
		}
		ids = append(ids, uuid.MustParse(record["id"].(string)))
	}
	if len(ids) != 2 {
		t.Fatalf("fixture records = %d, want 2", len(ids))
	}
	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	collection := catalog.Collections["cursor_database_clock_records"]
	syncMembershipOrdinals(t, ctx, pool)

	var beforeIssue time.Time
	if err := pool.QueryRow(ctx, `select clock_timestamp()`).Scan(&beforeIssue); err != nil {
		t.Fatal(err)
	}
	first, err := records.List(ctx, ListRequest{
		Principal: testReadPrincipal, Collection: collection.Name, Limit: 1, Sort: "title",
	})
	if err != nil {
		t.Fatalf("List(first page): %v", err)
	}
	if first.NextCursor == "" {
		t.Fatal("first page did not issue a cursor")
	}
	var afterIssue time.Time
	if err := pool.QueryRow(ctx, `select clock_timestamp()`).Scan(&afterIssue); err != nil {
		t.Fatal(err)
	}

	issued, err := records.cursors.Decode(first.NextCursor, collection.ID, beforeIssue)
	if err != nil {
		t.Fatalf("Decode(issued cursor): %v", err)
	}
	if issued.Version != cursor.Version5 {
		t.Fatalf("issued cursor version = %d, want 5", issued.Version)
	}
	issuedAt := issued.ExpiresAt.Add(-membership.CursorTTL)
	if issuedAt.Before(beforeIssue.UTC()) || issuedAt.After(afterIssue.UTC()) {
		t.Fatalf("cursor issue time = %s, want PostgreSQL time in [%s, %s]",
			issuedAt, beforeIssue.UTC(), afterIssue.UTC())
	}

	issued.ExpiresAt = beforeIssue.UTC().Add(-time.Microsecond)
	expired, err := records.cursors.Encode(issued)
	if err != nil {
		t.Fatalf("Encode(expired cursor fixture): %v", err)
	}
	if _, err := records.List(ctx, ListRequest{
		Principal: testReadPrincipal, Collection: collection.Name, Cursor: expired, Limit: 1, Sort: "title",
	}); !IsInvalidCursor(err) {
		t.Fatalf("List(expired cursor) error = %v, want invalid cursor", err)
	}
}
