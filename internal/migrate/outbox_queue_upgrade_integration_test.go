//go:build integration

package migrate

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/testutil"
	"github.com/yakisoba0728/PgBase/migrations/system"
)

func TestOutboxQueueUpgradePreservesStateAndConcurrentOldWriter(t *testing.T) {
	ctx := t.Context()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	legacy := New(pool, logger)
	for index, migration := range system.Migrations() {
		if migration.Version == 68 {
			legacy.migrations = legacy.migrations[:index]
			break
		}
	}
	if legacy.migrations[len(legacy.migrations)-1].Version != 67 {
		t.Fatal("fixture must start at schema 67")
	}
	if err := legacy.Up(ctx); err != nil {
		t.Fatal(err)
	}
	collection := uuid.New()
	events := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	for _, event := range events[:4] {
		if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			return insertUpgradeOutboxEvent(ctx, tx, event, collection)
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.outbox_delivery(event_id,delivered_at,attempts,dead_at)
 VALUES($1,clock_timestamp(),1,NULL),($2,NULL,2,NULL),($3,NULL,10,clock_timestamp())`, events[0], events[1], events[2]); err != nil {
		t.Fatal(err)
	}
	// This writer uses the old outbox-only SQL and has not committed when the
	// upgrade begins. Installing the trigger must wait for it, then backfill it.
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(context.Background())
	if err := insertUpgradeOutboxEvent(ctx, writer, events[4], collection); err != nil {
		t.Fatal(err)
	}
	upgradeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	runner := New(pool, logger)
	go func() { done <- runner.Up(upgradeCtx) }()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks
 WHERE relation='pgbase_meta.record_outbox'::regclass AND mode='ShareRowExclusiveLock' AND NOT granted)`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("upgrade did not wait for the in-flight old writer: %v", err)
		case <-upgradeCtx.Done():
			t.Fatal("upgrade never reached the writer coordination lock")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var count, pendingIdentities int
	var delivered, dead bool
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE delivered_at IS NULL
 AND collection_id=$1 AND record_id=event_id AND record_version=1) FROM pgbase_meta.outbox_delivery`, collection).Scan(&count, &pendingIdentities); err != nil || count != 5 || pendingIdentities != 4 {
		t.Fatalf("backfilled queue count=%d valid pending=%d error=%v", count, pendingIdentities, err)
	}
	if err := pool.QueryRow(ctx, `SELECT delivered_at IS NOT NULL,attempts FROM pgbase_meta.outbox_delivery WHERE event_id=$1`, events[0]).Scan(&delivered, &attempts); err != nil || !delivered || attempts != 1 {
		t.Fatalf("upgrade changed delivered marker: delivered=%t attempts=%d error=%v", delivered, attempts, err)
	}
	if err := pool.QueryRow(ctx, `SELECT dead_at IS NOT NULL,attempts FROM pgbase_meta.outbox_delivery WHERE event_id=$1`, events[2]).Scan(&dead, &attempts); err != nil || !dead || attempts != 10 {
		t.Fatalf("upgrade changed dead letter: dead=%t attempts=%d error=%v", dead, attempts, err)
	}
	if err := runner.Up(ctx); err != nil {
		t.Fatalf("rerun migration: %v", err)
	}
	status, err := runner.Status(ctx)
	if err != nil || status.Dirty || !status.Compatible || status.Current != status.Latest {
		t.Fatalf("upgraded status=%+v error=%v", status, err)
	}
}

func TestOutboxQueueAdmissionIsAtomicAndAcceptsLegacyDeliveryInsert(t *testing.T) {
	ctx := t.Context()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Up(ctx); err != nil {
		t.Fatal(err)
	}
	collection, rolledBack, committed := uuid.New(), uuid.New(), uuid.New()
	failure := errors.New("roll back source event")
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := insertUpgradeOutboxEvent(ctx, tx, rolledBack, collection); err != nil {
			return err
		}
		var queued bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pgbase_meta.outbox_delivery WHERE event_id=$1)`, rolledBack).Scan(&queued); err != nil || !queued {
			t.Fatalf("source transaction has no delivery row: queued=%t error=%v", queued, err)
		}
		return failure
	}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	for _, table := range []string{"record_audit_events", "record_outbox", "outbox_delivery"} {
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM pgbase_meta."+table+" WHERE event_id=$1", rolledBack).Scan(&count); err != nil || count != 0 {
			t.Fatalf("rollback retained %s: count=%d error=%v", table, count, err)
		}
	}
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return insertUpgradeOutboxEvent(ctx, tx, committed, collection)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.outbox_delivery(event_id) VALUES($1) ON CONFLICT DO NOTHING`, committed); err != nil {
		t.Fatalf("old worker insert failed after upgrade: %v", err)
	}
	var queued bool
	if err := pool.QueryRow(ctx, `SELECT count(*)=1 AND bool_and(collection_id=$2 AND record_id=$1 AND record_version=1)
 FROM pgbase_meta.outbox_delivery WHERE event_id=$1`, committed, collection).Scan(&queued); err != nil || !queued {
		t.Fatalf("committed queue identity=%t error=%v", queued, err)
	}
	// A failed enqueue is a failed source transaction, never an acknowledged
	// outbox event that a future polling pass would need to rediscover.
	if _, err := pool.Exec(ctx, `CREATE FUNCTION pgbase_meta.reject_queue_fixture() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN RAISE EXCEPTION 'injected enqueue failure'; END; $$;
 CREATE TRIGGER reject_queue_fixture BEFORE INSERT ON pgbase_meta.outbox_delivery FOR EACH ROW EXECUTE FUNCTION pgbase_meta.reject_queue_fixture()`); err != nil {
		t.Fatal(err)
	}
	rejected := uuid.New()
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return insertUpgradeOutboxEvent(ctx, tx, rejected, collection)
	}); err == nil {
		t.Fatal("enqueue failure did not reject the source write")
	}
	var artifacts int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM pgbase_meta.record_audit_events WHERE event_id=$1)
 +(SELECT count(*) FROM pgbase_meta.record_outbox WHERE event_id=$1)
 +(SELECT count(*) FROM pgbase_meta.outbox_delivery WHERE event_id=$1)`, rejected).Scan(&artifacts); err != nil || artifacts != 0 {
		t.Fatalf("failed enqueue retained artifacts=%d error=%v", artifacts, err)
	}
}

func insertUpgradeOutboxEvent(ctx context.Context, db database.DBTX, event, collection uuid.UUID) error {
	if _, err := db.Exec(ctx, `INSERT INTO pgbase_meta.record_audit_events(event_id,actor_kind,actor_id,request_id,collection_id,collection_name,
 record_id,operation,schema_version,schema_digest,new_version,new_etag,changed_fields,input_hash,compatibility,committed_at)
 VALUES($1,'administrator',$1,'queue-upgrade',$2,'queue_fixture',$1,'create',1,decode(repeat('aa',32),'hex'),1,'etag',ARRAY['title'],decode(repeat('bb',32),'hex'),'missing-idempotency-key',now())`, event, collection); err != nil {
		return err
	}
	_, err := db.Exec(ctx, `INSERT INTO pgbase_meta.record_outbox(event_id,topic,collection_id,collection_name,record_id,operation,record_version,envelope,created_at)
 VALUES($1,'record.mutation.v1',$2,'queue_fixture',$1,'create',1,jsonb_build_object('version',1,'eventId',$1::uuid::text,'collectionId',$2::uuid::text,
 'collectionName','queue_fixture','recordId',$1::uuid::text,'operation','create','recordVersion',1),now())`, event, collection)
	return err
}
