//go:build integration

package realtime

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

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
