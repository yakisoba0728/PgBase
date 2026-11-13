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

func TestRequestLogContextIsExplicitAndOptIn(t *testing.T) {
	var log RequestLog
	log.Record("GET", "/anonymous", 200, time.Millisecond)
	log.RecordContext("GET", "/identified", 200, time.Millisecond, "192.0.2.4", "admin", "019551d0-e9c0-7000-8000-0000000000aa")
	page := log.Page(0, 0)
	if len(page.Items) != 2 {
		t.Fatalf("items = %d", len(page.Items))
	}
	if page.Items[0].ClientIP != "192.0.2.4" || page.Items[0].AuthKind != "admin" || page.Items[0].AuthID == "" {
		t.Fatalf("identified entry = %#v", page.Items[0])
	}
	if page.Items[1].ClientIP != "" || page.Items[1].AuthID != "" {
		t.Fatalf("anonymous entry retained context = %#v", page.Items[1])
	}
}

func TestRequestLogFlushAfterShutdownDoesNotBlock(t *testing.T) {
	log := &RequestLog{flush: make(chan chan error)}
	log.closed.Store(true)
	if err := log.Flush(context.Background()); !errors.Is(err, ErrRequestLogClosed) {
		t.Fatalf("flush closed = %v", err)
	}
}

func TestRequestLogShutdownSharesFinalFailureAfterCallerCancellation(t *testing.T) {
	log := &RequestLog{stop: make(chan struct{}), done: make(chan struct{})}
	want := errors.New("flush failed")
	release := make(chan struct{})
	go func() {
		<-log.stop
		<-release
		log.shutdownErr = want
		close(log.done)
	}()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := log.Shutdown(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("first caller cancellation = %v", err)
	}
	var callers sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		callers.Go(func() { results <- log.Shutdown(context.Background()) })
	}
	close(release)
	callers.Wait()
	close(results)
	for err := range results {
		if !errors.Is(err, want) {
			t.Fatalf("concurrent caller lost final failure: %v", err)
		}
	}
	if err := log.Shutdown(canceled); !errors.Is(err, want) {
		t.Fatalf("completed shutdown lost final failure: %v", err)
	}
}

func TestRequestLogFlushKeepsItsAcknowledgementDuringShutdown(t *testing.T) {
	want := errors.New("write failed")
	for range 100 {
		log := &RequestLog{flush: make(chan chan error), done: make(chan struct{})}
		go func() {
			ack := <-log.flush
			ack <- want
			close(log.done)
		}()
		if err := log.Flush(context.Background()); !errors.Is(err, want) {
			t.Fatalf("shutdown replaced admitted flush result: %v", err)
		}
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
	stats, err := log.Statistics(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 123 || stats.StatusClasses["4xx"] != 3 {
		t.Fatalf("recent statistics = %#v", stats)
	}
	log.RecordContext("GET", "/other", 200, time.Millisecond, "192.0.2.9", "user", "019551d0-e9c0-7000-8000-0000000000bb")
	for query, want := range map[string]int{"WANTED": 3, "192.0.2.9": 1, "0000000000bb": 1, "absent": 0} {
		if page, err := log.Search(context.Background(), RequestFilter{Query: query}); err != nil || len(page.Items) != want {
			t.Fatalf("query %q = %d items, want %d (%v)", query, len(page.Items), want, err)
		}
	}
	future := time.Now().Add(time.Hour)
	if stats, err := log.Statistics(context.Background(), &future, nil); err != nil || stats.Total != 0 {
		t.Fatalf("future statistics = %#v %v", stats, err)
	}
}

func TestRequestSearchPatternPreservesLiteralWildcards(t *testing.T) {
	for query, want := range map[string]string{"": "", "Hello": "%hello%", "a_b%": `%a\_b\%%`, `a\b`: `%a\\b%`} {
		if got := requestSearchPattern(query); got != want {
			t.Fatalf("pattern(%q)=%q want %q", query, got, want)
		}
	}
}
