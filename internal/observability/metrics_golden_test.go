package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/webisnoteasy/PgBase/internal/database"
)

type metricSemanticGolden struct {
	Families []metricFamilySemantic `json:"families"`
	Samples  []metricSampleSemantic `json:"samples"`
}

type metricFamilySemantic struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	MetricCount int    `json:"metricCount"`
}

type metricSampleSemantic struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
	Value  float64           `json:"value,omitempty"`
	Count  uint64            `json:"count,omitempty"`
	Sum    float64           `json:"sum,omitempty"`
}

func TestMetricExpositionSemanticAndByteGoldens(t *testing.T) {
	metrics := NewMetrics(func() database.PoolStats {
		return database.PoolStats{
			AcquireCount: 11, AcquireDuration: 2500 * time.Millisecond, AcquiredConns: 2,
			CanceledAcquireCount: 3, ConstructingConns: 4, EmptyAcquireCount: 5,
			EmptyAcquireWaitTime: 750 * time.Millisecond, IdleConns: 6, MaxConns: 20,
			MaxIdleDestroyCount: 7, MaxLifetimeDestroyCount: 8, NewConnsCount: 9, TotalConns: 10,
		}
	})
	metrics.Record(context.Background(), OperationEvent(OperationSchemaCreate, OutcomeSuccess, 250*time.Millisecond))
	metrics.Record(context.Background(), TransactionRetryEvent(RetryCauseDeadlock, true))
	metrics.Record(context.Background(), ReadinessEvent(ReadinessDatabase, false))
	metrics.Record(context.Background(), ReadinessEvent(ReadinessDatabase, true))
	metrics.Record(context.Background(), Event{})
	// Seed the HTTP collectors directly so their exposition is deterministic;
	// WrapHTTP's observed duration is intentionally wall-clock based.
	metrics.httpRequests.WithLabelValues("GET", "/health/live", "2xx").Inc()
	metrics.httpDuration.WithLabelValues("GET", "/health/live", "2xx").Observe(0.25)
	metrics.httpInFlight.WithLabelValues("GET", "/health/live").Set(0)

	body := scrape(t, metrics, "metrics-golden-token")
	wantBody, err := os.ReadFile("testdata/metrics.golden.txt")
	if err != nil {
		t.Fatal(err)
	}
	if body != string(wantBody) {
		t.Fatalf("metric byte golden mismatch\n--- got ---\n%s--- want ---\n%s", body, wantBody)
	}
	if strings.Contains(body, "metrics-golden-token") {
		t.Fatal("metric exposition leaked the bearer token")
	}

	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse metric exposition: %v", err)
	}
	semantic := metricSemanticGolden{Families: semanticMetricFamilies(families)}
	semantic.Samples = []metricSampleSemantic{
		counterSemantic(t, families, "pgbase_application_operations_total", map[string]string{"component": "schema", "operation": "schema_create", "outcome": "success"}),
		histogramSemantic(t, families, "pgbase_application_operation_duration_seconds", map[string]string{"component": "schema", "operation": "schema_create", "outcome": "success"}),
		counterSemantic(t, families, "pgbase_database_transaction_retries_total", map[string]string{"cause": "deadlock", "exhausted": "true"}),
		gaugeSemantic(t, families, "pgbase_application_readiness", map[string]string{"component": "database"}),
		counterSemantic(t, families, "pgbase_application_readiness_transitions_total", map[string]string{"component": "database", "ready": "true"}),
		counterSemantic(t, families, "pgbase_observability_dropped_events_total", nil),
		counterSemantic(t, families, "pgbase_http_requests_total", map[string]string{"method": "GET", "route": "/health/live", "status": "2xx"}),
		histogramSemantic(t, families, "pgbase_http_request_duration_seconds", map[string]string{"method": "GET", "route": "/health/live", "status": "2xx"}),
		gaugeSemantic(t, families, "pgbase_database_pool_max_connections", nil),
	}
	encoded, err := json.MarshalIndent(semantic, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	wantSemantic, err := os.ReadFile("testdata/metrics_semantic.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, wantSemantic) {
		t.Fatalf("metric semantic golden mismatch\n--- got ---\n%s--- want ---\n%s", encoded, wantSemantic)
	}
}

func semanticMetricFamilies(families map[string]*dto.MetricFamily) []metricFamilySemantic {
	names := make([]string, 0, len(families))
	for name := range families {
		names = append(names, name)
	}
	slices.Sort(names)
	result := make([]metricFamilySemantic, 0, len(names))
	for _, name := range names {
		family := families[name]
		result = append(result, metricFamilySemantic{Name: name, Type: family.GetType().String(), MetricCount: len(family.Metric)})
	}
	return result
}

func counterSemantic(t *testing.T, families map[string]*dto.MetricFamily, name string, labels map[string]string) metricSampleSemantic {
	t.Helper()
	metric := findMetric(t, families, name, labels)
	if metric.Counter == nil {
		t.Fatalf("metric %s is not a counter", name)
	}
	return metricSampleSemantic{Name: name, Labels: labels, Value: metric.Counter.GetValue()}
}

func gaugeSemantic(t *testing.T, families map[string]*dto.MetricFamily, name string, labels map[string]string) metricSampleSemantic {
	t.Helper()
	metric := findMetric(t, families, name, labels)
	if metric.Gauge == nil {
		t.Fatalf("metric %s is not a gauge", name)
	}
	return metricSampleSemantic{Name: name, Labels: labels, Value: metric.Gauge.GetValue()}
}

func histogramSemantic(t *testing.T, families map[string]*dto.MetricFamily, name string, labels map[string]string) metricSampleSemantic {
	t.Helper()
	metric := findMetric(t, families, name, labels)
	if metric.Histogram == nil {
		t.Fatalf("metric %s is not a histogram", name)
	}
	return metricSampleSemantic{
		Name: name, Labels: labels,
		Count: metric.Histogram.GetSampleCount(), Sum: metric.Histogram.GetSampleSum(),
	}
}

func findMetric(t *testing.T, families map[string]*dto.MetricFamily, name string, labels map[string]string) *dto.Metric {
	t.Helper()
	family := families[name]
	if family == nil {
		t.Fatalf("metric family %s is absent", name)
	}
	for _, metric := range family.Metric {
		if metricLabelsEqual(metric, labels) {
			return metric
		}
	}
	t.Fatalf("metric family %s has no sample with labels %v", name, labels)
	return nil
}

func metricLabelsEqual(metric *dto.Metric, want map[string]string) bool {
	if len(metric.Label) != len(want) {
		return false
	}
	for _, pair := range metric.Label {
		if want[pair.GetName()] != pair.GetValue() {
			return false
		}
	}
	return true
}
