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
	stats, err := log.Statistics(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 3 || stats.Dropped != 0 {
		t.Fatalf("statistics = %#v", stats)
	}
	log.RecordContext("POST", "/api/admin/logs", 204, time.Millisecond, "192.0.2.4", "admin", "019551d0-e9c0-7000-8000-0000000000aa")
	if err := log.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	// The address is returned without the inet netmask suffix.
	for query, want := range map[string]int{"ADMIN/LOGS": 1, "192.0.2.4": 1, "0000000000aa": 1, "records": 2, "absent": 0} {
		found, err := log.Search(ctx, RequestFilter{Query: query})
		if err != nil || len(found.Items) != want {
			t.Fatalf("query %q = %#v %v", query, found, err)
		}
		if query == "192.0.2.4" && found.Items[0].ClientIP != "192.0.2.4" {
			t.Fatalf("client IP = %q", found.Items[0].ClientIP)
		}
	}
	future := time.Now().Add(time.Hour)
	if ranged, err := log.Statistics(ctx, nil, &future); err != nil || ranged.Total != 3 {
		t.Fatalf("bounded statistics = %#v %v", ranged, err)
	}
	if ranged, err := log.Statistics(ctx, &future, nil); err != nil || ranged.Total != 0 || ranged.OldestAt != nil {
		t.Fatalf("empty range statistics = %#v %v", ranged, err)
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
	if deleted, err := log.DeleteBefore(ctx, future); err != nil || deleted != 3 {
		t.Fatalf("conditional delete = %d %v", deleted, err)
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

func TestRequestLogMalformedEntryDoesNotLoseItsBatch(t *testing.T) {
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	testutil.ApplySystemMigrationPrefix(t, databaseURL, system.Migrations())
	pool, err := database.Open(ctx, databaseURL, 5*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	log := NewRequestLog(pool, RequestLogOptions{MaximumRows: 100, Retention: time.Hour, QueueCapacity: 8, BatchSize: 8, FlushInterval: time.Hour})
	log.RecordContext("GET", "/before", 200, time.Millisecond, "192.0.2.4", "", "")
	log.RecordContext("GET", "/zoned", 200, time.Millisecond, "fe80::1%en0", "", "")
	log.RecordContext("GET", "/after", 200, time.Millisecond, "2001:db8::1", "", "")
	if err := log.Flush(ctx); err == nil {
		t.Fatal("flush of a malformed entry reported no error")
	}
	stats, err := log.Statistics(ctx, nil, nil)
	if err != nil || stats.Total != 2 || stats.Dropped != 1 {
		t.Fatalf("statistics = %#v %v", stats, err)
	}
	if err := log.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRequestLogShutdownPersistsItsFinalWriteFailure(t *testing.T) {
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	testutil.ApplySystemMigrationPrefix(t, databaseURL, system.Migrations())
	pool, err := database.Open(ctx, databaseURL, 5*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	log := NewRequestLog(pool, RequestLogOptions{QueueCapacity: 8, BatchSize: 8, FlushInterval: time.Hour})
	log.RecordContext("GET", "/invalid", 200, time.Millisecond, "invalid address", "", "")
	first := log.Shutdown(ctx)
	if first == nil || log.Dropped() != 1 {
		t.Fatalf("shutdown failure/dropped = %v/%d", first, log.Dropped())
	}
	if again := log.Shutdown(ctx); again != first {
		t.Fatalf("repeat shutdown lost final failure: %v, want %v", again, first)
	}
}
