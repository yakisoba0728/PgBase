//go:build integration

package query

import (
	"context"
	"testing"
)

func BenchmarkRecordServiceCRUD20Fields10000Rows(b *testing.B) {
	records, input, seededID := benchmarkRecordService(b)
	ctx := context.Background()

	b.Run("get", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := records.Get(ctx, GetRequest{Principal: testReadPrincipal, Collection: "benchmark_records", ID: seededID}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("list_50", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := records.listScalar(ctx, "benchmark_records", "", 50, "", ""); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("create", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := records.Create(ctx, "benchmark_records", input); err != nil {
				b.Fatal(err)
			}
		}
	})
}
