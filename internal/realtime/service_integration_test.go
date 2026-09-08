//go:build integration

package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/recordaccess"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func realtimeFixture(t *testing.T) (*Service, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := migrate.New(pool, logger).Up(ctx); err != nil {
		t.Fatal(err)
	}
	user, collection := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.users(id,email,password_hash,created_at,updated_at) VALUES($1,'events@example.com','fixture',now(),now())`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.collections(id,name,table_name,status,created_at,updated_at) VALUES($1,'notes','notes','active',now(),now())`, collection); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.collection_access(collection_id,enabled) VALUES($1,true)`, collection); err != nil {
		t.Fatal(err)
	}
	return NewService(pool, logger), user, collection
}

func insertEvent(t *testing.T, tx pgx.Tx, user, collection uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	event, record := uuid.New(), uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.record_audit_events(event_id,actor_kind,actor_id,request_id,collection_id,collection_name,
 record_id,operation,schema_version,schema_digest,new_version,new_etag,changed_fields,input_hash,compatibility,committed_at)
 VALUES($1,'user',$2,'realtime-test',$3,'notes',$4,'create',1,decode(repeat('aa',32),'hex'),1,'etag',ARRAY['title'],decode(repeat('bb',32),'hex'),'missing-idempotency-key',now())`, event, user, collection, record); err != nil {
		t.Fatal(err)
	}
	envelope, _ := json.Marshal(map[string]any{"version": 1, "eventId": event, "collectionId": collection, "collectionName": "notes", "recordId": record, "operation": "create", "recordVersion": 1, "changes": map[string]any{"title": "private"}})
	if _, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.record_outbox(event_id,topic,collection_id,collection_name,record_id,operation,record_version,envelope,created_at,owner_user_id)
 VALUES($1,'record.mutation.v1',$2,'notes',$3,'create',1,$4::jsonb,now(),$5)`, event, collection, record, string(envelope), user); err != nil {
		t.Fatal(err)
	}
	return event
}

func seedEvent(t *testing.T, s *Service, user, collection uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := s.pool.InTx(context.Background(), pgx.TxOptions{}, func(tx pgx.Tx) error { id = insertEvent(t, tx, user, collection); return nil }); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLateCommitAndConcurrentDeliveryReplay(t *testing.T) {
	s, user, collection := realtimeFixture(t)
	ctx := context.Background()
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	slow, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Rollback(ctx)
	late := insertEvent(t, slow, user, collection)
	early := seedEvent(t, s, user, collection)
	if err := s.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	cursor := int64(0)
	events, next, err := s.Read(ctx, user, "notes", &cursor)
	if err != nil || len(events) != 1 || events[0].EventID != early {
		t.Fatalf("first replay=%v %v", events, err)
	}
	if err := slow.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := s.Deliver(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	events, _, err = s.Read(ctx, user, "notes", &next)
	if err != nil || len(events) != 1 || events[0].EventID != late {
		t.Fatalf("late replay=%v %v", events, err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM pgbase_meta.realtime_events`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("delivery count=%d %v", count, err)
	}
}

func TestLeaseFenceCurrentPolicyAndRetention(t *testing.T) {
	s, user, collection := realtimeFixture(t)
	ctx := context.Background()
	event := seedEvent(t, s, user, collection)
	claims, err := s.claim(ctx)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims=%v %v", claims, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE pgbase_meta.outbox_delivery SET lease_until=now()-interval '1 second' WHERE event_id=$1`, event); err != nil {
		t.Fatal(err)
	}
	replacement, err := s.claim(ctx)
	if err != nil || len(replacement) != 1 {
		t.Fatalf("replacement=%v %v", replacement, err)
	}
	if err := s.publish(ctx, claims[0]); !errors.Is(err, errLeaseLost) {
		t.Fatalf("stale publish=%v", err)
	}
	if err := s.publish(ctx, replacement[0]); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if _, err := s.pool.Exec(ctx, `UPDATE pgbase_meta.collection_access SET enabled=false WHERE collection_id=$1`, collection); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Read(ctx, user, "notes", &zero); !errors.Is(err, recordaccess.ErrDenied) {
		t.Fatalf("disabled policy=%v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE pgbase_meta.collection_access SET enabled=true WHERE collection_id=$1`, collection); err != nil {
		t.Fatal(err)
	}
	foreign := uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO pgbase_meta.users(id,email,password_hash,created_at,updated_at) VALUES($1,'other@example.com','fixture',now(),now())`, foreign); err != nil {
		t.Fatal(err)
	}
	if events, _, err := s.Read(ctx, foreign, "notes", &zero); err != nil || len(events) != 0 {
		t.Fatalf("foreign replay=%v %v", events, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE pgbase_meta.realtime_events SET delivered_at=now()-interval '2 days'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(ctx, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Read(ctx, user, "notes", &zero); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("expired cursor=%v", err)
	}
	if err := s.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM pgbase_meta.realtime_events`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("pruned event redelivered=%d %v", count, err)
	}
}
