//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/membership"
)

// BenchmarkHotCollectionCreate is the fixed-concurrency comparison point for
// the Release-A creation fence. Run it with a fixed GOMAXPROCS/-cpu value and
// compare the same benchmark at the baseline and feature commits.
func BenchmarkHotCollectionCreate(b *testing.B) {
	_, pool, schemas, _, principal := newRecordOperationsTestWithURL(b)
	ctx := context.Background()
	runtime, err := membership.RegisterWriter(ctx, membership.New(pool), membership.WriterA)
	if err != nil {
		b.Fatal(err)
	}
	records := NewService(
		pool, schemas, []byte("0123456789abcdef0123456789abcdef"), nil, nil, runtime,
	)
	latencies := make([]int64, b.N)
	var sequence atomic.Uint64

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(worker *testing.PB) {
		for worker.Next() {
			index := sequence.Add(1) - 1
			value := strconv.FormatUint(index, 10)
			started := time.Now()
			_, err := records.Create(ctx, CreateRequest{
				Principal: principal, RequestID: "hot_create_" + value, Collection: "records",
				Input: map[string]json.RawMessage{
					"title": json.RawMessage(strconv.Quote("hot-record-" + value)),
				},
			})
			latencies[index] = time.Since(started).Nanoseconds()
			if err != nil {
				b.Errorf("Create(%d): %v", index, err)
				return
			}
		}
	})
	b.StopTimer()

	if len(latencies) == 0 {
		return
	}
	sort.Slice(latencies, func(left, right int) bool { return latencies[left] < latencies[right] })
	p99Index := (len(latencies)*99 + 99) / 100
	b.ReportMetric(float64(latencies[p99Index-1])/float64(time.Microsecond), "p99_us")
}
