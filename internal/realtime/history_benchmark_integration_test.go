//go:build integration

package realtime

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// BenchmarkOutboxDeliveredHistory measures an idle delivery poll after an
// explicitly sized committed history. It exercises Service.Deliver, including
// its claim transaction, without including fixture creation in timings.
// Use -benchtime=100x -count=3 or more; PGBASE_BENCH_EXPLAIN=1 prints the
// historical discovery query plan as a labelled diagnostic for comparison.
func BenchmarkOutboxDeliveredHistory(b *testing.B) {
	for _, count := range []int{1_000, 100_000} {
		b.Run(fmt.Sprintf("delivered=%d", count), func(b *testing.B) {
			s, user, collection := realtimeFixture(b)
			seedDeliveredHistory(b, s, user, collection, count)
			ctx := b.Context()
			if _, err := s.pool.Exec(ctx, `VACUUM (ANALYZE) pgbase_meta.record_outbox`); err != nil {
				b.Fatal(err)
			}
			if _, err := s.pool.Exec(ctx, `VACUUM (ANALYZE) pgbase_meta.outbox_delivery`); err != nil {
				b.Fatal(err)
			}
			if os.Getenv("PGBASE_BENCH_EXPLAIN") == "1" {
				// This is deliberately the historical SQL, pinned to b376e366.
				// Its label must not be confused with the final runtime query.
				const legacyDiscovery = `INSERT INTO pgbase_meta.outbox_delivery(event_id)
				 SELECT o.event_id FROM pgbase_meta.record_outbox o WHERE NOT EXISTS
				 (SELECT 1 FROM pgbase_meta.outbox_delivery d WHERE d.event_id=o.event_id)
				 ORDER BY o.created_at,o.event_id LIMIT 100 ON CONFLICT DO NOTHING`
				var plan []byte
				if err := s.pool.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+legacyDiscovery).Scan(&plan); err != nil {
					b.Fatal(err)
				}
				b.Logf("legacy_discovery_b376e366 rows=%d plan=%s", count, plan)
				if err := s.pool.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+claimPendingQuery, uuid.New()).Scan(&plan); err != nil {
					b.Fatal(err)
				}
				b.Logf("current_pending_claim rows=%d plan=%s", count, plan)
			}
			for range 10 {
				if err := s.Deliver(ctx); err != nil {
					b.Fatal(err)
				}
			}
			latency := make([]time.Duration, b.N)
			b.Logf("closed-loop idle delivery poll: delivered=%d go=%s GOMAXPROCS=%d", count, runtime.Version(), runtime.GOMAXPROCS(0))
			b.ReportAllocs()
			b.ResetTimer()
			for index := range b.N {
				started := time.Now()
				if err := s.Deliver(ctx); err != nil {
					b.Fatal(err)
				}
				latency[index] = time.Since(started)
			}
			b.StopTimer()
			if os.Getenv("PGBASE_BENCH_SAMPLES") == "1" {
				samples := make([]int64, len(latency))
				for index, elapsed := range latency {
					samples[index] = elapsed.Nanoseconds()
				}
				b.Logf("latency_ns rows=%d samples=%v", count, samples)
			}
			slices.Sort(latency)
			for _, percentile := range []int{50, 95, 99} {
				b.ReportMetric(float64(latency[(len(latency)-1)*percentile/100].Nanoseconds())/1e6, fmt.Sprintf("p%d-ms", percentile))
			}
		})
	}
}

// seedDeliveredHistory creates synthetic durable artifacts directly in a
// disposable database. It is never an application import path. It leaves the
// replay table empty, so it also represents history after replay retention.
func seedDeliveredHistory(t testing.TB, s *Service, user, collection uuid.UUID, count int) {
	t.Helper()
	ctx := context.Background()
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.record_audit_events(event_id,actor_kind,actor_id,request_id,collection_id,collection_name,
 record_id,operation,schema_version,schema_digest,new_version,new_etag,changed_fields,input_hash,compatibility,committed_at)
 SELECT gen_random_uuid(),'user',$1,'realtime-history',$2,'notes',gen_random_uuid(),'create',1,
 decode(repeat('aa',32),'hex'),1,'etag',ARRAY['title'],decode(repeat('bb',32),'hex'),'missing-idempotency-key',clock_timestamp()
 FROM generate_series(1,$3::integer)`, user, collection, count); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.record_outbox(event_id,topic,collection_id,collection_name,record_id,operation,record_version,envelope,created_at,owner_user_id,record_snapshot)
 SELECT event_id,'record.mutation.v1',collection_id,collection_name,record_id,operation,new_version,
 jsonb_build_object('version',1,'eventId',event_id::text,'collectionId',collection_id::text,'collectionName',collection_name,
 'recordId',record_id::text,'operation',operation,'recordVersion',new_version,'changes',jsonb_build_object('title','history')),
 committed_at,actor_id,jsonb_build_object('id',record_id::text)
 FROM pgbase_meta.record_audit_events WHERE request_id='realtime-history' AND collection_id=$1`, collection); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.outbox_delivery(event_id,delivered_at)
 SELECT event_id,clock_timestamp()-interval '2 days' FROM pgbase_meta.record_outbox WHERE collection_id=$1
 ON CONFLICT(event_id) DO UPDATE SET delivered_at=excluded.delivered_at`, collection)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
