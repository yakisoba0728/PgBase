//go:build integration

package query

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

// BenchmarkConcurrentRecordReads is a bounded, closed-loop query-service
// workload, not an HTTP or production-capacity claim. Fixtures live in a new
// disposable database and use direct SQL only for synthetic seed generation.
// Run with -benchtime=1000x or more for useful tail-latency samples.
// Add PGBASE_BENCH_EXPLAIN=1 and -v to print native SQL execution plans.
func BenchmarkConcurrentRecordReads(b *testing.B) {
	rows := 100_000
	if raw := os.Getenv("PGBASE_BENCH_ROWS"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < benchmarkSeedRows || value > 2_000_000 || value%benchmarkSeedRows != 0 {
			b.Fatal("PGBASE_BENCH_ROWS must be a multiple of 10000 in [10000, 2000000]")
		}
		rows = value
	}
	warmup := 100
	if raw := os.Getenv("PGBASE_BENCH_WARMUP"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 5 || value > 10000 {
			b.Fatal("PGBASE_BENCH_WARMUP must be in [5, 10000]")
		}
		warmup = value
	}
	records, _, id := benchmarkRecordService(b)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var collection schema.Collection
	if err := records.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		catalog, err := records.schemas.EnsureCurrent(ctx, tx)
		if err == nil {
			collection = catalog.Collections["benchmark_records"]
		}
		return err
	}); err != nil {
		b.Fatal(err)
	}
	table := pgx.Identifier{"app", collection.TableName}.Sanitize()
	if batches := rows/benchmarkSeedRows - 1; batches > 0 {
		columns := []string{`"id"`, `"created_at"`, `"updated_at"`}
		values := []string{"gen_random_uuid()", "source.created_at + batch * interval '1 second'", "source.updated_at + batch * interval '1 second'"}
		for _, field := range collection.Fields {
			column := pgx.Identifier{field.ColumnName}.Sanitize()
			columns = append(columns, column)
			values = append(values, "source."+column)
		}
		statement := fmt.Sprintf(`WITH source AS MATERIALIZED (SELECT * FROM %s ORDER BY created_at,id LIMIT %d)
			INSERT INTO %s (%s) SELECT %s FROM source CROSS JOIN generate_series(1,$1) AS batch`,
			table, benchmarkSeedRows, table, strings.Join(columns, ","), strings.Join(values, ","))
		if _, err := records.pool.Exec(ctx, statement, batches); err != nil {
			b.Fatal(err)
		}
	}
	// Direct synthetic inserts bypass Record Operations. Rebuild their cursor
	// sidecars with the existing fixture helper before measuring native reads.
	// Never use this helper as an application or production import path.
	testutil.RetireRecordMembershipV4(b, ctx, records.pool)
	var actual int
	if err := records.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&actual); err != nil || actual != rows {
		b.Fatalf("fixture rows=%d want=%d err=%v", actual, rows, err)
	}
	var ordinals int
	if err := records.pool.QueryRow(ctx, "SELECT count(*) FROM pgbase_meta.record_creation_ordinals WHERE collection_id=$1", collection.ID).Scan(&ordinals); err != nil || ordinals != rows {
		b.Fatalf("fixture cursor ordinals=%d want=%d err=%v", ordinals, rows, err)
	}
	if _, err := records.pool.Exec(ctx, "ANALYZE "+table); err != nil {
		b.Fatal(err)
	}
	// The fixture bulk-rebuilds this sidecar too. Leaving its cardinality stale
	// makes plan selection depend on when asynchronous autoanalyze happens.
	if _, err := records.pool.Exec(ctx, "ANALYZE pgbase_meta.record_creation_ordinals"); err != nil {
		b.Fatal(err)
	}
	// A diagnostic index on this disposable synthetic table, not a migration
	// or an implicit change to application schemas. Compare with the same
	// fixture without this flag and retain both execution plans.
	if os.Getenv("PGBASE_BENCH_FILTER_INDEX") == "1" {
		began := time.Now()
		if _, err := records.pool.Exec(ctx, "CREATE INDEX benchmark_priority_page ON "+table+" (priority, created_at, id)"); err != nil {
			b.Fatal(err)
		}
		var indexBytes int64
		if err := records.pool.QueryRow(ctx, "SELECT pg_relation_size('app.benchmark_priority_page')").Scan(&indexBytes); err != nil {
			b.Fatal(err)
		}
		b.Logf("diagnostic_filter_index=true build_time=%s index_bytes=%d", time.Since(began), indexBytes)
	}
	base := ListRequest{Principal: testReadPrincipal, Collection: "benchmark_records", Limit: 50}
	if count, err := records.Count(ctx, base); err != nil || count != int64(rows) {
		b.Fatalf("native visible rows=%d want=%d err=%v", count, rows, err)
	}
	deep := base
	// Walk at most ten thousand rows and no more than half a small fixture,
	// using native cursors rather than an offset adapter.
	for range min(200, rows/100) {
		page, err := records.List(ctx, deep)
		if err != nil || len(page.Items) != 50 || page.NextCursor == "" {
			b.Fatalf("prepare deep cursor: rows=%d err=%v", len(page.Items), err)
		}
		deep.Cursor = page.NextCursor
	}
	readPage := func(request ListRequest) func(context.Context) error {
		return func(ctx context.Context) error {
			page, err := records.List(ctx, request)
			if err != nil {
				return err
			}
			if len(page.Items) != 50 {
				return fmt.Errorf("page contains %d records, want 50", len(page.Items))
			}
			return nil
		}
	}
	filtered := base
	filtered.Filter = "priority = 42"
	// Verify semantics outside the timer. An index experiment must preserve the
	// actual filtered identities and default order, not merely return 50 rows.
	expectedRows, err := records.pool.Query(ctx, "SELECT id::text FROM "+table+" WHERE priority=42 ORDER BY created_at,id LIMIT 51")
	if err != nil {
		b.Fatal(err)
	}
	expected, err := pgx.CollectRows(expectedRows, pgx.RowTo[string])
	if err != nil || len(expected) != 51 {
		b.Fatalf("filtered reference rows=%d err=%v", len(expected), err)
	}
	checked, err := records.List(ctx, filtered)
	if err != nil || len(checked.Items) != 50 || checked.NextCursor == "" {
		b.Fatalf("filtered semantic check: rows=%d cursor=%q err=%v", len(checked.Items), checked.NextCursor, err)
	}
	for index, item := range checked.Items {
		if item["id"] != expected[index] || fmt.Sprint(item["priority"]) != "42" {
			b.Fatalf("filtered semantic check at %d: id=%v priority=%v want=%s/42", index, item["id"], item["priority"], expected[index])
		}
	}
	if os.Getenv("PGBASE_BENCH_EXPLAIN") == "1" {
		benchmarkReadPlan(b, records, base, "list_50")
		benchmarkReadPlan(b, records, filtered, "filtered_50")
	}
	workloads := []struct {
		name string
		run  func(context.Context) error
	}{
		{"get", func(ctx context.Context) error {
			_, err := records.Get(ctx, GetRequest{Principal: testReadPrincipal, Collection: "benchmark_records", ID: id})
			return err
		}},
		{"list_50", readPage(base)},
		{"deep_cursor_50", readPage(deep)},
		{"filtered_50", readPage(filtered)},
	}
	b.Logf("workload=closed-loop query-service rows=%d fields=20 warmup=%d go=%s cpus=%d GOMAXPROCS=%d", rows, warmup, runtime.Version(), runtime.NumCPU(), runtime.GOMAXPROCS(0))
	for _, workload := range workloads {
		for _, concurrency := range []int{1, 8, 32} {
			b.Run(fmt.Sprintf("rows=%d/%s/concurrency=%d", rows, workload.name, concurrency), func(b *testing.B) {
				b.StopTimer()
				for range warmup {
					if err := workload.run(ctx); err != nil {
						b.Fatal(err)
					}
				}
				latency := make([]time.Duration, b.N)
				var next atomic.Int64
				var workers sync.WaitGroup
				failures := make(chan error, concurrency)
				start := make(chan struct{})
				for range concurrency {
					workers.Add(1)
					go func() {
						defer workers.Done()
						<-start
						for {
							index := int(next.Add(1) - 1)
							if index >= b.N {
								return
							}
							requestCtx, requestCancel := context.WithTimeout(ctx, 15*time.Second)
							began := time.Now()
							err := workload.run(requestCtx)
							latency[index] = time.Since(began)
							requestCancel()
							if err != nil {
								failures <- err
								return
							}
						}
					}()
				}
				b.ReportAllocs()
				poolBefore := records.pool.Stats()
				var memoryBefore, memoryAfter runtime.MemStats
				runtime.ReadMemStats(&memoryBefore)
				b.StartTimer()
				began := time.Now()
				close(start)
				workers.Wait()
				elapsed := time.Since(began)
				b.StopTimer()
				poolAfter := records.pool.Stats()
				runtime.ReadMemStats(&memoryAfter)
				close(failures)
				for err := range failures {
					b.Errorf("request failed: %v", err)
				}
				if b.Failed() {
					return
				}
				if os.Getenv("PGBASE_BENCH_SAMPLES") == "1" {
					samples := make([]int64, len(latency))
					for index, elapsed := range latency {
						samples[index] = elapsed.Nanoseconds()
					}
					b.Logf("raw_latency_ns=%v", samples)
				}
				slices.Sort(latency)
				for _, percentile := range []int{50, 95, 99} {
					index := (len(latency)*percentile+99)/100 - 1
					b.ReportMetric(float64(latency[index])/float64(time.Millisecond), fmt.Sprintf("p%d-ms", percentile))
				}
				b.ReportMetric(float64(b.N)/elapsed.Seconds(), "queries/s")
				b.ReportMetric(0, "errors")
				b.ReportMetric(float64(poolAfter.EmptyAcquireCount-poolBefore.EmptyAcquireCount)/float64(b.N), "pool-waits/op")
				b.ReportMetric(float64(poolAfter.EmptyAcquireWaitTime-poolBefore.EmptyAcquireWaitTime)/float64(b.N), "pool-wait-ns/op")
				b.ReportMetric(float64(poolAfter.CanceledAcquireCount-poolBefore.CanceledAcquireCount), "pool-canceled")
				b.ReportMetric(float64(memoryAfter.NumGC-memoryBefore.NumGC), "gc-cycles")
				b.ReportMetric(float64(memoryAfter.PauseTotalNs-memoryBefore.PauseTotalNs)/float64(time.Millisecond), "gc-pause-ms")
			})
		}
	}
	if os.Getenv("PGBASE_BENCH_INDEX_WRITE") == "1" {
		// Run only after every timed read. This synthetic SQL update measures
		// index-maintenance buffers/WAL, not the Record Operations write API.
		// Roll back the probe so the committed fixture remains unchanged.
		rollback := errors.New("rollback diagnostic write probe")
		var plan []byte
		err := records.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			statement := "EXPLAIN (ANALYZE, BUFFERS, WAL, FORMAT JSON) UPDATE " + table +
				" SET priority=(priority+1)%100 WHERE id IN (SELECT id FROM " + table + " ORDER BY created_at,id LIMIT 1000)"
			if err := tx.QueryRow(ctx, statement).Scan(&plan); err != nil {
				return err
			}
			return rollback
		})
		if err != rollback {
			b.Fatalf("diagnostic write probe: %v", err)
		}
		b.Logf("diagnostic_write_update_rows=1000 rolled_back=true plan=%s", plan)
	}
}

// Explain the actual first-page statement, including native membership and
// authorization bindings, outside the timed workload. This is opt-in because
// ANALYZE executes the query and warms PostgreSQL's caches.
func benchmarkReadPlan(b *testing.B, records *Service, request ListRequest, name string) {
	b.Helper()
	ctx := context.Background()
	err := records.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		catalog, err := records.schemas.EnsureCurrent(ctx, tx)
		if err != nil {
			return err
		}
		collection := catalog.Collections[request.Collection]
		filter, err := parseListFilter(request)
		if err != nil {
			return err
		}
		sort, err := parseSort(request.Sort)
		if err != nil {
			return err
		}
		plan, err := records.planNativeRead(ctx, tx, catalog, collection, request, filter, sort)
		if err != nil {
			return err
		}
		traversal, err := records.resolveReadTraversal(ctx, tx, collection)
		if err != nil {
			return err
		}
		descriptor, err := records.newCursorV5Descriptor(plan, traversal)
		if err != nil {
			return err
		}
		statement, err := plan.pageStatement(&pageBoundary{membership: &descriptor.HighWater, membershipOnly: true}, normalizedFetchLimit(request.Limit), 0)
		if err != nil {
			return err
		}
		var explain []byte
		if err := tx.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+statement.SQL, statement.Args...).Scan(&explain); err != nil {
			return err
		}
		b.Logf("explain workload=%s plan=%s", name, explain)
		return nil
	})
	if err != nil {
		b.Fatalf("explain %s: %v", name, err)
	}
}
