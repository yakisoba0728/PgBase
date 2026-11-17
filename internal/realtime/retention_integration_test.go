//go:build integration

package realtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetentionCannotCrossAnUnexpiredSequence(t *testing.T) {
	s, user, collection := realtimeFixture(t)
	ctx := t.Context()
	first := seedEvent(t, s, user, collection)
	if err := s.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	second := seedEvent(t, s, user, collection)
	if err := s.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	// A corrected database clock can give a later committed sequence an older
	// timestamp. Retention may never invalidate an unexpired earlier cursor.
	if _, err := s.pool.Exec(ctx, `UPDATE pgbase_meta.realtime_events SET delivered_at=clock_timestamp()-interval '2 days' WHERE event_id=$1`, second); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(ctx, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	events, _, err := s.Read(ctx, user, "notes", &zero)
	if err != nil || len(events) != 2 || events[0].EventID != first || events[1].EventID != second {
		t.Fatalf("unexpired prefix lost: events=%v error=%v", events, err)
	}
	var floor int64
	if err := s.pool.QueryRow(ctx, `SELECT floor_sequence FROM pgbase_meta.realtime_retention WHERE singleton`).Scan(&floor); err != nil || floor != 0 {
		t.Fatalf("cursor floor crossed the unexpired prefix: floor=%d error=%v", floor, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE pgbase_meta.realtime_events SET delivered_at=clock_timestamp()-interval '2 days' WHERE event_id=$1`, first); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(ctx, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM pgbase_meta.realtime_events`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("fully expired prefix retained: count=%d error=%v", count, err)
	}
}

func TestRetentionCatchesUpInBoundedPassesWhileDeliveringNewEvents(t *testing.T) {
	s, user, collection := realtimeFixture(t)
	ctx := t.Context()
	const history = 2501
	seedDeliveredHistory(t, s, user, collection, history)
	if _, err := s.pool.Exec(ctx, `INSERT INTO pgbase_meta.realtime_events(event_id,owner_user_id,collection_id,collection_name,record_id,operation,record_version,delivered_at)
 SELECT event_id,owner_user_id,collection_id,collection_name,record_id,operation,record_version,clock_timestamp()-interval '2 days'
 FROM pgbase_meta.record_outbox ORDER BY created_at,event_id`); err != nil {
		t.Fatal(err)
	}
	pending := true
	for pass := range 3 {
		event := seedEvent(t, s, user, collection)
		pending = s.runPass(ctx, pending)
		if pending != (pass < 2) {
			t.Fatalf("pass %d catch-up pending=%t", pass, pending)
		}
		var count int
		want := max(0, history-(pass+1)*retentionBatchSize) + pass + 1
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM pgbase_meta.realtime_events`).Scan(&count); err != nil || count != want {
			t.Fatalf("pass %d retained=%d want=%d error=%v", pass, count, want, err)
		}
		var delivered bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pgbase_meta.realtime_events WHERE event_id=$1)`, event).Scan(&delivered); err != nil || !delivered {
			t.Fatalf("pass %d starved new delivery: delivered=%t error=%v", pass, delivered, err)
		}
	}
	var floor int64
	if err := s.pool.QueryRow(ctx, `SELECT floor_sequence FROM pgbase_meta.realtime_retention WHERE singleton`).Scan(&floor); err != nil || floor != history {
		t.Fatalf("completed catch-up floor=%d error=%v", floor, err)
	}
	events, _, err := s.Read(ctx, user, "notes", &floor)
	if err != nil || len(events) != 3 {
		t.Fatalf("new events after catch-up=%v error=%v", events, err)
	}
}

func TestRetentionCanceledLockWaitCannotAdvanceTheFloor(t *testing.T) {
	s, user, collection := realtimeFixture(t)
	ctx := t.Context()
	seedEvent(t, s, user, collection)
	if err := s.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE pgbase_meta.realtime_events SET delivered_at=clock_timestamp()-interval '2 days'`); err != nil {
		t.Fatal(err)
	}
	blocker, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, deliveryLock); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := s.Prune(deadline, 24*time.Hour); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked retention = %v, want deadline expiry", err)
	}
	var count, floor int64
	if err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM pgbase_meta.realtime_events),floor_sequence FROM pgbase_meta.realtime_retention WHERE singleton`).Scan(&count, &floor); err != nil || count != 1 || floor != 0 {
		t.Fatalf("canceled cleanup changed replay: count=%d floor=%d error=%v", count, floor, err)
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(ctx, 24*time.Hour); err != nil {
		t.Fatalf("cleanup did not recover after lock release: %v", err)
	}
}
