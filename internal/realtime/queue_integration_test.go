//go:build integration

package realtime

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPendingQueuePreservesRecordOrderAcrossDeadLetterAndConcurrentWorkers(t *testing.T) {
	s, user, collection := realtimeFixture(t)
	ctx := t.Context()
	first := seedEvent(t, s, user, collection)
	second := seedFollowingEvent(t, s, first)
	if _, err := s.pool.Exec(ctx, `UPDATE pgbase_meta.outbox_delivery SET dead_at=clock_timestamp(),attempts=10 WHERE event_id=$1`, first); err != nil {
		t.Fatal(err)
	}
	unrelated := seedEvent(t, s, user, collection)
	if err := s.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	var only uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT count(*),min(event_id::text)::uuid FROM pgbase_meta.realtime_events`).Scan(&count, &only); err != nil || count != 1 || only != unrelated {
		t.Fatalf("dead predecessor did not isolate record ordering: count=%d event=%s error=%v", count, only, err)
	}
	if err := s.Retry(ctx, first); err != nil {
		t.Fatal(err)
	}
	// Two rounds allow the first version to commit before the next version is
	// eligible. Workers coordinate through the same durable row locks/index.
	for range 2 {
		var workers sync.WaitGroup
		for range 4 {
			workers.Go(func() {
				if err := s.Deliver(ctx); err != nil {
					t.Error(err)
				}
			})
		}
		workers.Wait()
	}
	var firstSequence, secondSequence int64
	if err := s.pool.QueryRow(ctx, `SELECT
 (SELECT sequence FROM pgbase_meta.realtime_events WHERE event_id=$1),
 (SELECT sequence FROM pgbase_meta.realtime_events WHERE event_id=$2),
 (SELECT count(*) FROM pgbase_meta.realtime_events)`, first, second).Scan(&firstSequence, &secondSequence, &count); err != nil || firstSequence >= secondSequence || count != 3 {
		t.Fatalf("delivery order or identity changed: first=%d second=%d count=%d error=%v", firstSequence, secondSequence, count, err)
	}
}

func TestPendingQueueIdentityCommitsWithItsOutboxEvent(t *testing.T) {
	s, user, collection := realtimeFixture(t)
	ctx := t.Context()
	writer, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(context.Background())
	event := insertEvent(t, writer, user, collection)
	var count int
	if err := writer.QueryRow(ctx, `SELECT count(*) FROM pgbase_meta.outbox_delivery d JOIN pgbase_meta.record_outbox o USING(event_id)
 WHERE d.event_id=$1 AND d.collection_id=o.collection_id AND d.record_id=o.record_id AND d.record_version=o.record_version`, event).Scan(&count); err != nil || count != 1 {
		t.Fatalf("source transaction did not atomically enqueue identity: count=%d error=%v", count, err)
	}
	if err := s.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM pgbase_meta.realtime_events`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("worker exposed uncommitted source event: count=%d error=%v", count, err)
	}
	if err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM pgbase_meta.realtime_events WHERE event_id=$1`, event).Scan(&count); err != nil || count != 1 {
		t.Fatalf("committed source event not delivered: count=%d error=%v", count, err)
	}
}

func seedFollowingEvent(t *testing.T, s *Service, first uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	event := uuid.New()
	if err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.record_audit_events(event_id,actor_kind,actor_id,request_id,collection_id,collection_name,
 record_id,operation,schema_version,schema_digest,previous_version,new_version,previous_etag,new_etag,changed_fields,input_hash,compatibility,committed_at)
 SELECT $2,actor_kind,actor_id,request_id,collection_id,collection_name,record_id,'update',schema_version,schema_digest,
 new_version,new_version+1,new_etag,'next-etag',changed_fields,input_hash,'missing-if-match',clock_timestamp()
 FROM pgbase_meta.record_audit_events WHERE event_id=$1`, first, event); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.record_outbox(event_id,topic,collection_id,collection_name,record_id,operation,record_version,envelope,created_at,owner_user_id,record_snapshot)
 SELECT $2,topic,collection_id,collection_name,record_id,'update',record_version+1,
 envelope||jsonb_build_object('eventId',$2::uuid::text,'operation','update','recordVersion',record_version+1),
 clock_timestamp(),owner_user_id,record_snapshot FROM pgbase_meta.record_outbox WHERE event_id=$1`, first, event)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return event
}

func TestBatchPublicationSkipsAnExpiredLeaseAndKeepsOrder(t *testing.T) {
	s, user, collection := realtimeFixture(t)
	ctx := t.Context()
	for range 3 {
		seedEvent(t, s, user, collection)
	}
	claims, err := s.claim(ctx)
	if err != nil || len(claims) != 3 {
		t.Fatalf("claims=%v error=%v", claims, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE pgbase_meta.outbox_delivery SET lease_until=clock_timestamp()-interval '1 second' WHERE event_id=$1`, claims[1].id); err != nil {
		t.Fatal(err)
	}
	if err := s.publishBatch(ctx, claims); err != nil {
		t.Fatal(err)
	}
	var delivered []uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT array_agg(event_id ORDER BY sequence) FROM pgbase_meta.realtime_events`).Scan(&delivered); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 2 || delivered[0] != claims[0].id || delivered[1] != claims[2].id {
		t.Fatalf("batch delivered %v, want %v and %v", delivered, claims[0].id, claims[2].id)
	}
}
