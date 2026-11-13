//go:build integration

package realtime

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestDeliveryFailureDoesNotStrandTheRestOfTheClaimedBatch(t *testing.T) {
	s, user, collection := realtimeFixture(t)
	ctx := context.Background()
	seedEvent(t, s, user, collection)
	seedEvent(t, s, user, collection)
	if _, err := s.pool.Exec(ctx, `CREATE FUNCTION pgbase_meta.fail_delivery() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture publish failure'; END $$;
 CREATE TRIGGER fixture_delivery_failure BEFORE INSERT ON pgbase_meta.realtime_events FOR EACH ROW EXECUTE FUNCTION pgbase_meta.fail_delivery()`); err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(ctx); err == nil {
		t.Fatal("delivery should report the failed batch")
	}
	var retrying int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM pgbase_meta.outbox_delivery WHERE attempts=1 AND lease_token IS NULL AND lease_until IS NULL AND available_at>clock_timestamp()`).Scan(&retrying); err != nil {
		t.Fatal(err)
	}
	if retrying != 2 {
		t.Fatalf("retrying=%d, want both claims released for backoff", retrying)
	}
	if _, err := s.pool.Exec(ctx, `DROP TRIGGER fixture_delivery_failure ON pgbase_meta.realtime_events; DROP FUNCTION pgbase_meta.fail_delivery();
 UPDATE pgbase_meta.outbox_delivery SET available_at=clock_timestamp()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	status, err := s.Status(ctx)
	if err != nil || status.Delivered != 2 || status.Pending != 0 {
		t.Fatalf("recovered status=%+v err=%v", status, err)
	}
}

func TestDeliveryFailureBackoffDeadLetterAndOperatorRetry(t *testing.T) {
	s, user, collection := realtimeFixture(t)
	ctx := context.Background()
	event := seedEvent(t, s, user, collection)
	// A database-side fault exercises rollback of the real publish transaction.
	if _, err := s.pool.Exec(ctx, `CREATE FUNCTION pgbase_meta.fail_delivery() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture publish failure'; END $$;
 CREATE TRIGGER fixture_delivery_failure BEFORE INSERT ON pgbase_meta.realtime_events FOR EACH ROW EXECUTE FUNCTION pgbase_meta.fail_delivery()`); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 10; attempt++ {
		if err := s.Deliver(ctx); err == nil {
			t.Fatalf("attempt %d should fail", attempt)
		}
		var attempts int
		var waiting, dead bool
		if err := s.pool.QueryRow(ctx, `SELECT attempts,available_at>clock_timestamp(),dead_at IS NOT NULL FROM pgbase_meta.outbox_delivery WHERE event_id=$1`, event).Scan(&attempts, &waiting, &dead); err != nil {
			t.Fatal(err)
		}
		if attempts != attempt || !waiting || dead != (attempt == 10) {
			t.Fatalf("attempt=%d waiting=%t dead=%t", attempts, waiting, dead)
		}
		// A repeated poll must respect backoff and cannot consume another attempt.
		if err := s.Deliver(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `UPDATE pgbase_meta.outbox_delivery SET available_at=clock_timestamp()-interval '1 second' WHERE event_id=$1`, event); err != nil {
			t.Fatal(err)
		}
	}
	status, err := s.Status(ctx)
	if err != nil || status.Dead != 1 || status.Delivered != 0 {
		t.Fatalf("dead status=%+v err=%v", status, err)
	}
	dead, err := s.DeadEvents(ctx, "", 0)
	if err != nil || len(dead.Items) != 1 || dead.Items[0].EventID != event || dead.Items[0].Attempts != 10 || dead.NextBefore != "" {
		t.Fatalf("dead page=%+v err=%v", dead, err)
	}
	// A second dead row pages newest first with an opaque position cursor.
	second := seedEvent(t, s, user, collection)
	if _, err := s.pool.Exec(ctx, `INSERT INTO pgbase_meta.outbox_delivery(event_id,attempts,dead_at) VALUES($1,10,clock_timestamp())`, second); err != nil {
		t.Fatal(err)
	}
	first, err := s.DeadEvents(ctx, "", 1)
	if err != nil || len(first.Items) != 1 || first.Items[0].EventID != second || first.NextBefore == "" {
		t.Fatalf("first page=%+v err=%v", first, err)
	}
	// Retrying the cursor row must not invalidate the cursor.
	if err := s.Retry(ctx, second); err != nil {
		t.Fatal(err)
	}
	next, err := s.DeadEvents(ctx, first.NextBefore, 1)
	if err != nil || len(next.Items) != 1 || next.Items[0].EventID != event || next.NextBefore != "" {
		t.Fatalf("next page=%+v err=%v", next, err)
	}
	if _, err := s.DeadEvents(ctx, "not-a-cursor", 1); !errors.Is(err, ErrCursorInvalid) {
		t.Fatalf("invalid cursor err=%v", err)
	}
	// Keep the remaining assertions about the first event exact.
	if _, err := s.pool.Exec(ctx, `DELETE FROM pgbase_meta.record_outbox WHERE event_id=$1`, second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `DROP TRIGGER fixture_delivery_failure ON pgbase_meta.realtime_events; DROP FUNCTION pgbase_meta.fail_delivery()`); err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Retry(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	// Retrying delivered work is harmless and preserves the event identity.
	if err := s.Retry(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	var got uuid.UUID
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*),min(event_id::text)::uuid FROM pgbase_meta.realtime_events`).Scan(&count, &got); err != nil {
		t.Fatal(err)
	}
	if count != 1 || got != event {
		t.Fatalf("events=%d identity=%v", count, got)
	}
	status, err = s.Status(ctx)
	if err != nil || status.Dead != 0 || status.Pending != 0 || status.Delivered != 1 {
		t.Fatalf("recovered status=%+v err=%v", status, err)
	}
}
