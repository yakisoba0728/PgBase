//go:build integration

package query

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/cursor"
	"github.com/webisnoteasy/PgBase/internal/membership"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func TestCursorLifetimeUsesPostgresTimeAcrossV5Drain(t *testing.T) {
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
	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	collection := catalog.Collections["cursor_database_clock_records"]
	installReadyMembershipFixture(t, ctx, pool, collection, ids)
	if _, err := pool.Exec(ctx, `update pgbase_meta.record_membership_capability set
		cutover_at = clock_timestamp() - interval '2 minutes',
		ready_at = clock_timestamp() - interval '1 minute',
		updated_at = clock_timestamp() where singleton`); err != nil {
		t.Fatal(err)
	}

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

	controller := membership.New(pool)
	runtimeB, err := membership.RegisterWriter(ctx, controller, membership.WriterB)
	if err != nil {
		t.Fatalf("RegisterWriter(B): %v", err)
	}
	activation, err := controller.EnableV5(ctx, membership.FleetProof{
		Members: []membership.FleetMember{runtimeB.FleetMember()},
	})
	if err != nil {
		t.Fatalf("EnableV5: %v", err)
	}

	issued, err := records.cursors.Decode(first.NextCursor, collection.ID, activation.EnabledAt)
	if err != nil {
		t.Fatalf("Decode(pre-activation v4 cursor): %v", err)
	}
	if issued.Version != cursor.Version4 {
		t.Fatalf("pre-activation cursor version = %d, want 4", issued.Version)
	}
	issuedAt := issued.ExpiresAt.Add(-membership.CursorTTL)
	if issuedAt.Before(beforeIssue.UTC()) || issuedAt.After(activation.EnabledAt) {
		t.Fatalf("cursor issue time = %s, want PostgreSQL time in [%s, %s]",
			issuedAt, beforeIssue.UTC(), activation.EnabledAt)
	}
	if issued.ExpiresAt.After(activation.DecodeV4Until) {
		t.Fatalf("v4 cursor expiry = %s, beyond absolute drain deadline %s",
			issued.ExpiresAt, activation.DecodeV4Until)
	}

	issued.ExpiresAt = activation.EnabledAt.Add(-time.Microsecond)
	expired, err := records.cursors.Encode(issued)
	if err != nil {
		t.Fatalf("Encode(expired v4 cursor fixture): %v", err)
	}
	if _, err := records.List(ctx, ListRequest{
		Principal: testReadPrincipal, Collection: collection.Name, Cursor: expired, Limit: 1, Sort: "title",
	}); !IsInvalidCursor(err) {
		t.Fatalf("List(expired cursor) error = %v, want invalid cursor", err)
	}
}
