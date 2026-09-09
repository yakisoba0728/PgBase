package observability

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRequestLogBoundedPagesAndFilter(t *testing.T) {
	var log RequestLog
	for i := 0; i < 1200; i++ {
		status := 200
		if i%2 == 0 {
			status = 500
		}
		log.Record("GET", "/records/{id}", status, time.Millisecond)
	}
	first := log.Page(0, 5)
	if len(first.Items) != 50 || first.Items[0].ID != 1199 || first.OldestID != 201 || first.NextBefore != 1101 {
		t.Fatalf("first page: %#v", first)
	}
	next := log.Page(first.NextBefore, 5)
	if len(next.Items) != 50 || next.Items[0].ID != 1099 {
		t.Fatalf("next page: %#v", next)
	}
	if len(log.Page(201, 0).Items) != 0 {
		t.Fatal("expired entries returned")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			for j := 0; j < 100; j++ {
				log.Record("POST", "/records", 201, time.Millisecond)
				log.Page(0, 0)
			}
		})
	}
	wg.Wait()
	if got := log.Page(0, 0); len(got.Items) != 50 || got.Items[0].ID != 2000 {
		t.Fatalf("concurrent page: %#v", got)
	}
}

func TestRequestLogFlushAfterShutdownDoesNotBlock(t *testing.T) {
	log := &RequestLog{flush: make(chan chan error)}
	log.closed.Store(true)
	if err := log.Flush(context.Background()); !errors.Is(err, ErrRequestLogClosed) {
		t.Fatalf("flush closed = %v", err)
	}
}

func TestRequestLogQueueDropsAreBoundedAndObservable(t *testing.T) {
	log := &RequestLog{queue: make(chan RequestEntry, 1)}
	log.Record("GET", "/safe/{id}", 200, time.Millisecond)
	log.Record("POST", "/safe", 201, time.Millisecond)
	if got := log.Dropped(); got != 1 {
		t.Fatalf("dropped = %d, want 1", got)
	}
	if got := log.Page(0, 0); len(got.Items) != 2 {
		t.Fatalf("recent compatibility lost: %#v", got)
	}
}

func TestRequestLogRecentSearchFiltersBeforePagination(t *testing.T) {
	var log RequestLog
	for range 60 {
		log.Record("GET", "/new", 200, time.Millisecond)
	}
	for range 3 {
		log.Record("POST", "/wanted/{id}", 404, 2*time.Millisecond)
	}
	for range 60 {
		log.Record("GET", "/new", 200, time.Millisecond)
	}
	page, err := log.Search(context.Background(), RequestFilter{Method: "post", Route: "/wanted/{id}", StatusClass: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("filtered recent page = %#v", page)
	}
	stats, err := log.Statistics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 123 || stats.StatusClasses["4xx"] != 3 {
		t.Fatalf("recent statistics = %#v", stats)
	}
}
