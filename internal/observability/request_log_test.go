package observability

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
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

type recordedEvents struct {
	mu     sync.Mutex
	events []Event
}

func (r *recordedEvents) Record(_ context.Context, event Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *recordedEvents) outcomes() []Outcome {
	r.mu.Lock()
	defer r.mu.Unlock()
	outcomes := make([]Outcome, 0, len(r.events))
	for _, event := range r.events {
		outcomes = append(outcomes, event.Outcome())
	}
	return outcomes
}

// A cleanup that keeps failing lets the table grow, so it is never silent: the
// first failure warns, repeats are rate limited, recovery is announced and
// every attempt reaches the metric with a bounded outcome.
func TestRequestLogCleanupFailuresWarnAndReachTheMetric(t *testing.T) {
	var logs bytes.Buffer
	events := &recordedEvents{}
	log := &RequestLog{options: RequestLogOptions{Logger: slog.New(slog.NewJSONHandler(&logs, nil)), Recorder: events}.normalized()}
	ctx := context.Background()
	started := time.Now()
	log.observePrune(ctx, started, 0, &pgconn.PgError{Code: "57014"})
	log.observePrune(ctx, started, 0, context.DeadlineExceeded)
	log.observePrune(ctx, started, 0, errors.New("broken"))
	if got := strings.Count(logs.String(), "request log cleanup failed"); got != 1 {
		t.Fatalf("warnings = %d, want 1 within the interval:\n%s", got, logs.String())
	}
	if !strings.Contains(logs.String(), `"level":"WARN"`) || !strings.Contains(logs.String(), `"outcome":"budget"`) {
		t.Fatalf("warning = %s", logs.String())
	}
	log.pruneMu.Lock()
	log.pruneWarnedAt = time.Now().Add(-requestLogPruneWarningInterval)
	log.pruneMu.Unlock()
	log.observePrune(ctx, started, 0, errors.New("broken"))
	if got := strings.Count(logs.String(), "request log cleanup failed"); got != 2 || !strings.Contains(logs.String(), `"consecutive_failures":4`) {
		t.Fatalf("repeated warning after the interval:\n%s", logs.String())
	}
	log.observePrune(ctx, started, 12, nil)
	log.observePrune(ctx, started, 0, nil)
	if got := strings.Count(logs.String(), "request log cleanup recovered"); got != 1 || !strings.Contains(logs.String(), `"failed_attempts":4`) {
		t.Fatalf("recovery notice:\n%s", logs.String())
	}
	log.observePrune(ctx, started, 0, context.Canceled)
	want := []Outcome{OutcomeBudget, OutcomeBudget, OutcomeFailure, OutcomeFailure, OutcomeSuccess, OutcomeSuccess, OutcomeCanceled}
	if got := events.outcomes(); !slices.Equal(got, want) {
		t.Fatalf("outcomes = %v, want %v", got, want)
	}
	for _, event := range events.events {
		if event.Operation() != OperationRequestLogPrune || event.Component() != ComponentRequestLog || !validOperationEvent(event) {
			t.Fatalf("event = %+v", event)
		}
	}
}

func TestRequestSearchPatternPreservesLiteralWildcards(t *testing.T) {
	for query, want := range map[string]string{"": "", "Hello": "%hello%", "a_b%": `%a\_b\%%`, `a\b`: `%a\\b%`} {
		if got := requestSearchPattern(query); got != want {
			t.Fatalf("pattern(%q)=%q want %q", query, got, want)
		}
	}
}
