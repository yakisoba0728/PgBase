package observability

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/database"
)

func TestEventConstructorsExposeReadOnlyValues(t *testing.T) {
	duration := 125 * time.Millisecond
	operation := OperationEvent(ComponentSchema, OperationSchemaUpdate, OutcomeConflict, duration)
	if operation.Kind() != EventKindOperation || operation.Component() != ComponentSchema ||
		operation.Operation() != OperationSchemaUpdate || operation.Outcome() != OutcomeConflict ||
		operation.Duration() != duration {
		t.Fatalf("operation event getters returned unexpected values: %+v", operation)
	}

	retry := TransactionRetryEvent(RetryCauseSerialization, true)
	if retry.Kind() != EventKindTransactionRetry || retry.RetryCause() != RetryCauseSerialization || !retry.Exhausted() {
		t.Fatalf("retry event getters returned unexpected values: %+v", retry)
	}

	readiness := ReadinessEvent(ReadinessDatabase, true)
	if readiness.Kind() != EventKindReadiness || readiness.ReadinessComponent() != ReadinessDatabase || !readiness.Ready() {
		t.Fatalf("readiness event getters returned unexpected values: %+v", readiness)
	}
}

func TestMetricsRecordsOnlyBoundedEvents(t *testing.T) {
	metrics := NewMetrics(nil)
	metrics.Record(context.Background(), OperationEvent(ComponentSchema, OperationSchemaCreate, OutcomeSuccess, 250*time.Millisecond))
	metrics.Record(context.Background(), OperationEvent(ComponentRelation, OperationRelationExpand, OutcomeBudget, 10*time.Millisecond))
	metrics.Record(context.Background(), TransactionRetryEvent(RetryCauseDeadlock, true))
	metrics.Record(context.Background(), ReadinessEvent(ReadinessDatabase, false))
	metrics.Record(context.Background(), ReadinessEvent(ReadinessDatabase, true))
	metrics.Record(context.Background(), ReadinessEvent(ReadinessDatabase, true))
	metrics.Record(context.Background(), ReadinessEvent(ReadinessDatabase, false))

	invalidLabels := []string{"raw-email@example.test", "secret-token-sentinel", "unbounded-readiness"}
	metrics.Record(context.Background(), OperationEvent(Component(invalidLabels[0]), OperationSchemaCreate, OutcomeSuccess, time.Second))
	metrics.Record(context.Background(), OperationEvent(ComponentSchema, OperationMigrationUp, OutcomeSuccess, time.Second))
	metrics.Record(context.Background(), OperationEvent(ComponentSchema, OperationSchemaCreate, Outcome(invalidLabels[1]), time.Second))
	metrics.Record(context.Background(), OperationEvent(ComponentSchema, OperationSchemaCreate, OutcomeSuccess, -time.Second))
	metrics.Record(context.Background(), TransactionRetryEvent(RetryCause(invalidLabels[1]), false))
	metrics.Record(context.Background(), ReadinessEvent(ReadinessComponent(invalidLabels[2]), true))
	metrics.Record(context.Background(), Event{})

	body := scrape(t, metrics, "metrics-access-token")
	for _, sentinel := range append(invalidLabels, "metrics-access-token") {
		if strings.Contains(body, sentinel) {
			t.Fatalf("metrics output leaked unbounded or secret value %q:\n%s", sentinel, body)
		}
	}
	assertContains(t, body,
		`pgbase_application_operations_total{component="schema",operation="schema_create",outcome="success"} 1`,
		`pgbase_application_operations_total{component="relation",operation="relation_expand",outcome="budget"} 1`,
		`pgbase_database_transaction_retries_total{cause="deadlock",exhausted="true"} 1`,
		`pgbase_application_readiness{component="database"} 0`,
		`pgbase_application_readiness_transitions_total{component="database",ready="true"} 1`,
		`pgbase_application_readiness_transitions_total{component="database",ready="false"} 1`,
		`pgbase_observability_dropped_events_total 7`,
	)
}

// TestRelationReadOperationIsInsideTheClosedWhitelist pins the relation
// operation set. A relation read that only filters or sorts must be able to
// leave a sample of its own instead of being charged to expansion, and the
// label stays a closed whitelist: an operation nobody registered still drops.
func TestRelationReadOperationIsInsideTheClosedWhitelist(t *testing.T) {
	metrics := NewMetrics(nil)
	unlisted := "relation_sort_sentinel"
	metrics.Record(context.Background(), OperationEvent(ComponentRelation, OperationRelationRead, OutcomeBudget, 10*time.Millisecond))
	metrics.Record(context.Background(), OperationEvent(ComponentRelation, OperationRelationRead, OutcomeValidation, time.Millisecond))
	metrics.Record(context.Background(), OperationEvent(ComponentRelation, Operation(unlisted), OutcomeBudget, time.Millisecond))

	body := scrape(t, metrics, "metrics-access-token")
	if strings.Contains(body, unlisted) {
		t.Fatalf("metrics output accepted the unlisted relation operation %q:\n%s", unlisted, body)
	}
	assertContains(t, body,
		`pgbase_application_operations_total{component="relation",operation="relation_read",outcome="budget"} 1`,
		`pgbase_application_operations_total{component="relation",operation="relation_read",outcome="validation"} 1`,
		`pgbase_observability_dropped_events_total 1`,
	)
}

func TestWrapHTTPUsesMuxRouteTemplatesWithoutRequestData(t *testing.T) {
	metrics := NewMetrics(nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/collections/{collection}/records/{id}", func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusAccepted)
	})
	wrapped := metrics.WrapHTTP(mux, mux)

	dynamicEmail := "someone@example.test"
	dynamicToken := "raw-token-sentinel"
	request := httptest.NewRequest(http.MethodGet,
		"/api/v1/collections/"+dynamicEmail+"/records/"+dynamicToken+"?token=query-secret", nil)
	response := httptest.NewRecorder()
	wrapped.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("response status = %d, want %d", response.Code, http.StatusAccepted)
	}

	unmatched := httptest.NewRequest("ATTACKER-METHOD", "/private/another@example.test/raw-secret", nil)
	wrapped.ServeHTTP(httptest.NewRecorder(), unmatched)
	body := scrape(t, metrics, "scrape-secret")
	for _, sentinel := range []string{dynamicEmail, dynamicToken, "query-secret", "another@example.test", "raw-secret", "ATTACKER-METHOD", "scrape-secret"} {
		if strings.Contains(body, sentinel) {
			t.Fatalf("HTTP metrics leaked raw request value %q:\n%s", sentinel, body)
		}
	}
	assertContains(t, body,
		`pgbase_http_requests_total{method="GET",route="/api/v1/collections/{collection}/records/{id}",status="2xx"} 1`,
		`pgbase_http_requests_total{method="OTHER",route="unmatched",status="4xx"} 1`,
		`pgbase_http_requests_in_flight{method="GET",route="/api/v1/collections/{collection}/records/{id}"} 0`,
	)
}

func TestBoundedHTTPStatusUsesOnlyClasses(t *testing.T) {
	for status, want := range map[int]string{
		99: "unknown", 100: "1xx", 204: "2xx", 304: "3xx", 422: "4xx", 599: "5xx", 600: "unknown",
	} {
		if got := boundedHTTPStatus(status); got != want {
			t.Fatalf("boundedHTTPStatus(%d) = %q, want %q", status, got, want)
		}
	}
}

func TestHistogramBucketsCoverRuntimeContracts(t *testing.T) {
	if len(httpDurationBuckets) != 13 || httpDurationBuckets[len(httpDurationBuckets)-1] != 30 {
		t.Fatalf("HTTP duration buckets = %v, want 13 finite buckets through 30s", httpDurationBuckets)
	}
	if len(operationDurationBuckets) != 16 || operationDurationBuckets[len(operationDurationBuckets)-1] != 600 {
		t.Fatalf("operation duration buckets = %v, want 16 finite buckets through 10m", operationDurationBuckets)
	}
}

func TestMetricsHandlerRequiresOneWellFormedBearerAndDisablesCaching(t *testing.T) {
	metrics := NewMetrics(nil)
	token := "correct-token-sentinel"
	tests := []struct {
		name       string
		method     string
		headers    []string
		wantStatus int
	}{
		{name: "missing", method: http.MethodGet, wantStatus: http.StatusUnauthorized},
		{name: "wrong", method: http.MethodGet, headers: []string{"Bearer wrong-token-sentinel"}, wantStatus: http.StatusUnauthorized},
		{name: "missing space", method: http.MethodGet, headers: []string{"Bearer"}, wantStatus: http.StatusUnauthorized},
		{name: "extra segment", method: http.MethodGet, headers: []string{"Bearer " + token + " extra"}, wantStatus: http.StatusUnauthorized},
		{name: "comma combined", method: http.MethodGet, headers: []string{"Bearer " + token + ",Bearer " + token}, wantStatus: http.StatusUnauthorized},
		{name: "duplicate", method: http.MethodGet, headers: []string{"Bearer " + token, "Bearer " + token}, wantStatus: http.StatusUnauthorized},
		{name: "wrong method", method: http.MethodPost, headers: []string{"Bearer " + token}, wantStatus: http.StatusMethodNotAllowed},
		{name: "case insensitive scheme", method: http.MethodGet, headers: []string{"bearer " + token}, wantStatus: http.StatusOK},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "/metrics", nil)
			for _, value := range test.headers {
				request.Header.Add("Authorization", value)
			}
			response := httptest.NewRecorder()
			metrics.Handler([]byte(token)).ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
			for _, secret := range []string{token, "wrong-token-sentinel"} {
				if strings.Contains(response.Body.String(), secret) {
					t.Fatalf("response leaked bearer secret %q", secret)
				}
			}
		})
	}
}

func TestMetricsRegistriesArePrivatePerApplication(t *testing.T) {
	first := NewMetrics(nil)
	second := NewMetrics(nil)
	first.Record(context.Background(), OperationEvent(ComponentAdminAuth, OperationAdminLogin, OutcomeSuccess, time.Millisecond))

	firstBody := scrape(t, first, "first-token")
	secondBody := scrape(t, second, "second-token")
	if !strings.Contains(firstBody, "pgbase_application_operations_total") {
		t.Fatalf("first private registry is missing its operation metric:\n%s", firstBody)
	}
	if strings.Contains(secondBody, "pgbase_application_operations_total") {
		t.Fatalf("second private registry contains first registry's metric:\n%s", secondBody)
	}
	for _, processMetric := range []string{"go_gc_", "go_goroutines", "process_cpu_"} {
		if strings.Contains(firstBody, processMetric) || strings.Contains(secondBody, processMetric) {
			t.Fatalf("private registry unexpectedly contains global collector %q", processMetric)
		}
	}
}

func TestPoolCollectorTakesOneCompleteSnapshotAtScrape(t *testing.T) {
	var calls atomic.Int64
	metrics := NewMetrics(func() database.PoolStats {
		calls.Add(1)
		return database.PoolStats{
			AcquireCount:            11,
			AcquireDuration:         2500 * time.Millisecond,
			AcquiredConns:           2,
			CanceledAcquireCount:    3,
			ConstructingConns:       4,
			EmptyAcquireCount:       5,
			EmptyAcquireWaitTime:    750 * time.Millisecond,
			IdleConns:               6,
			MaxConns:                20,
			MaxIdleDestroyCount:     7,
			MaxLifetimeDestroyCount: 8,
			NewConnsCount:           9,
			TotalConns:              10,
		}
	})

	body := scrape(t, metrics, "pool-token")
	if got := calls.Load(); got != 1 {
		t.Fatalf("pool stats callback calls = %d, want one per scrape", got)
	}
	assertContains(t, body,
		"pgbase_database_pool_acquire_count_total 11",
		"pgbase_database_pool_acquire_duration_seconds_total 2.5",
		"pgbase_database_pool_acquired_connections 2",
		"pgbase_database_pool_canceled_acquire_count_total 3",
		"pgbase_database_pool_constructing_connections 4",
		"pgbase_database_pool_empty_acquire_count_total 5",
		"pgbase_database_pool_empty_acquire_wait_seconds_total 0.75",
		"pgbase_database_pool_idle_connections 6",
		"pgbase_database_pool_max_connections 20",
		"pgbase_database_pool_max_idle_destroy_count_total 7",
		"pgbase_database_pool_max_lifetime_destroy_count_total 8",
		"pgbase_database_pool_new_connections_count_total 9",
		"pgbase_database_pool_total_connections 10",
	)
}

func TestMetricsRecordWrapAndScrapeAreConcurrentSafe(t *testing.T) {
	metrics := NewMetrics(nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})
	wrapped := metrics.WrapHTTP(mux, mux)

	const workers = 16
	const iterations = 100
	var wait sync.WaitGroup
	wait.Add(workers + 4)
	for worker := range workers {
		go func(worker int) {
			defer wait.Done()
			for iteration := range iterations {
				metrics.Record(context.Background(), OperationEvent(ComponentSchema, OperationSchemaUpdate, OutcomeSuccess, time.Microsecond))
				metrics.Record(context.Background(), TransactionRetryEvent(RetryCauseCachedPlan, false))
				metrics.Record(context.Background(), ReadinessEvent(ReadinessSchemaListener, (worker+iteration)%2 == 0))
				request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/items/%d", worker*iterations+iteration), nil)
				wrapped.ServeHTTP(httptest.NewRecorder(), request)
			}
		}(worker)
	}
	for range 4 {
		go func() {
			defer wait.Done()
			for range 10 {
				_ = scrapeStatus(metrics, "concurrent-token")
			}
		}()
	}
	wait.Wait()

	body := scrape(t, metrics, "final-token")
	assertContains(t, body,
		`pgbase_application_operations_total{component="schema",operation="schema_update",outcome="success"} 1600`,
		`pgbase_database_transaction_retries_total{cause="cached_plan",exhausted="false"} 1600`,
		`pgbase_http_requests_total{method="GET",route="/items/{id}",status="2xx"} 1600`,
	)
}

func BenchmarkHTTPInstrumentation(b *testing.B) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/items/019551d0-e9c0-7000-8000-000000000999", nil)

	b.Run("disabled", func(b *testing.B) {
		response := &benchmarkResponseWriter{header: make(http.Header)}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			mux.ServeHTTP(response, request)
		}
	})

	b.Run("enabled", func(b *testing.B) {
		metrics := NewMetrics(nil)
		handler := metrics.WrapHTTP(mux, mux)
		response := &benchmarkResponseWriter{header: make(http.Header)}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			handler.ServeHTTP(response, request)
		}
	})
}

type benchmarkResponseWriter struct {
	header http.Header
}

func (w *benchmarkResponseWriter) Header() http.Header          { return w.header }
func (*benchmarkResponseWriter) WriteHeader(int)                {}
func (*benchmarkResponseWriter) Write(body []byte) (int, error) { return len(body), nil }

func scrape(t *testing.T, metrics *Metrics, token string) string {
	t.Helper()
	response := scrapeStatus(metrics, token)
	if response.Code != http.StatusOK {
		t.Fatalf("metrics scrape status = %d; body=%s", response.Code, response.Body.String())
	}
	return response.Body.String()
}

func scrapeStatus(metrics *Metrics, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	metrics.Handler([]byte(token)).ServeHTTP(response, request)
	return response
}

func assertContains(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, value := range want {
		if !strings.Contains(body, value) {
			t.Errorf("metrics output does not contain %q:\n%s", value, body)
		}
	}
}
