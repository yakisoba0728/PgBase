//go:build integration

package observability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/testutil"
	"github.com/yakisoba0728/PgBase/migrations/system"
)

func TestPersistentRequestLogRetentionSearchAndShutdown(t *testing.T) {
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	testutil.ApplySystemMigrationPrefix(t, databaseURL, system.Migrations())
	pool, err := database.Open(ctx, databaseURL, 5*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	log := NewRequestLog(pool, RequestLogOptions{MaximumRows: 3, Retention: time.Hour, QueueCapacity: 8, BatchSize: 2, FlushInterval: time.Hour})
	for i := range 5 {
		status := 200
		if i == 4 {
			status = 503
		}
		log.Record("GET", "/records/{id}", status, time.Duration(i+1)*time.Millisecond)
	}
	if err := log.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := log.Search(ctx, RequestFilter{StatusClass: 5, Method: "get", Route: "/records/{id}"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Status != 503 {
		t.Fatalf("filtered page = %#v", page)
	}
	stats, err := log.Statistics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 3 || stats.Dropped != 0 {
		t.Fatalf("statistics = %#v", stats)
	}
	if _, err := log.Detail(ctx, page.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Detail(ctx, page.Items[0].ID+1000); !errors.Is(err, ErrRequestLogNotFound) {
		t.Fatalf("missing detail error = %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := log.Detail(canceled, page.Items[0].ID); !errors.Is(err, context.Canceled) || errors.Is(err, ErrRequestLogNotFound) {
		t.Fatalf("canceled detail error = %v", err)
	}
	if err := log.Truncate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := log.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if page, err := log.Search(ctx, RequestFilter{}); err != nil || len(page.Items) != 0 {
		t.Fatalf("after truncate = %#v, %v", page, err)
	}
}
