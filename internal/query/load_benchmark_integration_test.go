//go:build integration

package query

import (
	"context"
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
		if err != nil || value < benchmarkSeedRows || value > 1_000_000 || value%benchmarkSeedRows != 0 {
			b.Fatal("PGBASE_BENCH_ROWS must be a multiple of 10000 in [10000, 1000000]")
		}
		rows = value
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
	b.Logf("workload=closed-loop query-service rows=%d fields=20 go=%s cpus=%d GOMAXPROCS=%d", rows, runtime.Version(), runtime.NumCPU(), runtime.GOMAXPROCS(0))
	for _, workload := range workloads {
		for _, concurrency := range []int{1, 8, 32} {
			b.Run(fmt.Sprintf("rows=%d/%s/concurrency=%d", rows, workload.name, concurrency), func(b *testing.B) {
				b.StopTimer()
				for range 5 {
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
