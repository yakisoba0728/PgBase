package observability

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/webisnoteasy/PgBase/internal/database"
)

// Recorder is the narrow application seam used to emit bounded telemetry
// events. Implementations must not derive labels from request or record data.
type Recorder interface {
	Record(context.Context, Event)
}

// EventKind identifies one of the closed event shapes accepted by Recorder.
type EventKind string

const (
	EventKindOperation        EventKind = "operation"
	EventKindTransactionRetry EventKind = "transaction_retry"
	EventKindReadiness        EventKind = "readiness"
)

// Component is a bounded application subsystem label.
type Component string

const (
	ComponentMigration      Component = "migration"
	ComponentSchema         Component = "schema"
	ComponentSchemaListener Component = "schema_listener"
	ComponentAdminAuth      Component = "admin_auth"
	ComponentRelation       Component = "relation"
)

// Operation is a bounded application operation label.
type Operation string

const (
	OperationMigrationUp     Operation = "migration_up"
	OperationMigrationStatus Operation = "migration_status"
	OperationSchemaCreate    Operation = "schema_create"
	OperationSchemaUpdate    Operation = "schema_update"
	OperationSchemaDelete    Operation = "schema_delete"
	OperationListenerConnect Operation = "listener_connect"
	OperationListenerReload  Operation = "listener_reload"
	OperationAdminSetup      Operation = "admin_setup"
	OperationAdminLogin      Operation = "admin_login"
	OperationRelationSchema  Operation = "relation_schema"
	OperationRelationWrite   Operation = "relation_write"
	OperationRelationDelete  Operation = "relation_delete"
	OperationRelationExpand  Operation = "relation_expand"
	OperationRelationRead    Operation = "relation_read"
)

// Outcome is a bounded operation result label.
type Outcome string

const (
	OutcomeSuccess            Outcome = "success"
	OutcomeFailure            Outcome = "failure"
	OutcomeConflict           Outcome = "conflict"
	OutcomeValidation         Outcome = "validation"
	OutcomeUnavailable        Outcome = "unavailable"
	OutcomeCanceled           Outcome = "canceled"
	OutcomeInvalidCredentials Outcome = "invalid_credentials"
	OutcomeRateLimited        Outcome = "rate_limited"
	OutcomeSetupUnavailable   Outcome = "setup_unavailable"
	OutcomeBudget             Outcome = "budget"
)

// RetryCause is a bounded transaction retry cause label.
type RetryCause = database.RetryCause

const (
	RetryCauseCachedPlan    = database.RetryCauseCachedPlan
	RetryCauseSerialization = database.RetryCauseSerialization
	RetryCauseDeadlock      = database.RetryCauseDeadlock
)

// ReadinessComponent is a bounded readiness dependency label.
type ReadinessComponent string

const (
	ReadinessHTTP           ReadinessComponent = "http"
	ReadinessDatabase       ReadinessComponent = "database"
	ReadinessMigrations     ReadinessComponent = "migrations"
	ReadinessSchemaListener ReadinessComponent = "schema_listener"
)

// Event is an immutable, bounded telemetry event. Its fields deliberately stay
// private so callers cannot attach secrets or unbounded labels.
type Event struct {
	kind               EventKind
	component          Component
	operation          Operation
	outcome            Outcome
	duration           time.Duration
	retryCause         RetryCause
	exhausted          bool
	readinessComponent ReadinessComponent
	ready              bool
}

// OperationEvent constructs an operation event. The component is derived from
// the operation rather than supplied, because an operation belongs to exactly
// one subsystem and letting a caller name it is a way to mislabel a metric
// series. An operation outside the closed set, or an invalid outcome or
// duration, is rejected by Recorder and counted only as a dropped event.
func OperationEvent(operation Operation, outcome Outcome, duration time.Duration) Event {
	return Event{
		kind:      EventKindOperation,
		component: componentForOperation(operation),
		operation: operation,
		outcome:   outcome,
		duration:  duration,
	}
}

// componentForOperation is the single operation-to-component mapping. Its zero
// value for an unlisted operation is what makes such an event invalid, so the
// mapping and the validity rule cannot disagree.
func componentForOperation(operation Operation) Component {
	switch operation {
	case OperationMigrationUp, OperationMigrationStatus:
		return ComponentMigration
	case OperationSchemaCreate, OperationSchemaUpdate, OperationSchemaDelete:
		return ComponentSchema
	case OperationListenerConnect, OperationListenerReload:
		return ComponentSchemaListener
	case OperationAdminSetup, OperationAdminLogin:
		return ComponentAdminAuth
	case OperationRelationSchema, OperationRelationWrite, OperationRelationDelete,
		OperationRelationExpand, OperationRelationRead:
		return ComponentRelation
	default:
		return ""
	}
}

func TransactionRetryEvent(cause RetryCause, exhausted bool) Event {
	return Event{kind: EventKindTransactionRetry, retryCause: cause, exhausted: exhausted}
}

func ReadinessEvent(component ReadinessComponent, ready bool) Event {
	return Event{kind: EventKindReadiness, readinessComponent: component, ready: ready}
}

func (e Event) Kind() EventKind { return e.kind }

func (e Event) Component() Component { return e.component }

func (e Event) Operation() Operation { return e.operation }

func (e Event) Outcome() Outcome { return e.outcome }

func (e Event) Duration() time.Duration { return e.duration }

func (e Event) RetryCause() RetryCause { return e.retryCause }

func (e Event) Exhausted() bool { return e.exhausted }

func (e Event) ReadinessComponent() ReadinessComponent { return e.readinessComponent }

func (e Event) Ready() bool { return e.ready }

// Metrics owns an application-private Prometheus registry. No collector is
// registered with Prometheus's process-global registry.
type Metrics struct {
	registry *prometheus.Registry

	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec
	httpInFlight *prometheus.GaugeVec

	operations           *prometheus.CounterVec
	operationDuration    *prometheus.HistogramVec
	transactionRetries   *prometheus.CounterVec
	readiness            *prometheus.GaugeVec
	readinessTransitions *prometheus.CounterVec
	droppedEvents        prometheus.Counter

	readinessMu    sync.Mutex
	readinessState map[ReadinessComponent]bool
	readinessSeen  map[ReadinessComponent]bool
}

var (
	httpDurationBuckets      = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 15, 30}
	operationDurationBuckets = []float64{
		0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600,
	}
)

// NewMetrics creates an isolated metrics registry. poolStats is evaluated at
// scrape time so pool gauges and counters describe one current pgx snapshot.
func NewMetrics(poolStats func() database.PoolStats) *Metrics {
	metrics := &Metrics{
		registry: prometheus.NewRegistry(),
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "pgbase",
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "Total HTTP requests handled by route template, method, and status class.",
		}, []string{"method", "route", "status"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "pgbase",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "HTTP request duration in seconds by route template, method, and status class.",
			Buckets:   httpDurationBuckets,
		}, []string{"method", "route", "status"}),
		httpInFlight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "pgbase",
			Subsystem: "http",
			Name:      "requests_in_flight",
			Help:      "Current in-flight HTTP requests by route template and method.",
		}, []string{"method", "route"}),
		operations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "pgbase",
			Subsystem: "application",
			Name:      "operations_total",
			Help:      "Total bounded application operations by component, operation, and outcome.",
		}, []string{"component", "operation", "outcome"}),
		operationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "pgbase",
			Subsystem: "application",
			Name:      "operation_duration_seconds",
			Help:      "Bounded application operation duration in seconds.",
			Buckets:   operationDurationBuckets,
		}, []string{"component", "operation", "outcome"}),
		transactionRetries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "pgbase",
			Subsystem: "database",
			Name:      "transaction_retries_total",
			Help:      "Total transaction retries by bounded cause and exhausted state.",
		}, []string{"cause", "exhausted"}),
		readiness: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "pgbase",
			Subsystem: "application",
			Name:      "readiness",
			Help:      "Most recently observed readiness state of each bounded dependency (1 ready, 0 not ready).",
		}, []string{"component"}),
		readinessTransitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "pgbase",
			Subsystem: "application",
			Name:      "readiness_transitions_total",
			Help:      "Total observed readiness state transitions by dependency and destination state.",
		}, []string{"component", "ready"}),
		droppedEvents: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "pgbase",
			Subsystem: "observability",
			Name:      "dropped_events_total",
			Help:      "Total invalid telemetry events dropped before labels were created.",
		}),
		readinessState: make(map[ReadinessComponent]bool, 4),
		readinessSeen:  make(map[ReadinessComponent]bool, 4),
	}

	metrics.registry.MustRegister(
		metrics.httpRequests,
		metrics.httpDuration,
		metrics.httpInFlight,
		metrics.operations,
		metrics.operationDuration,
		metrics.transactionRetries,
		metrics.readiness,
		metrics.readinessTransitions,
		metrics.droppedEvents,
		newPoolCollector(poolStats),
	)
	for _, component := range allReadinessComponents {
		metrics.readiness.WithLabelValues(string(component)).Set(0)
		metrics.readinessTransitions.WithLabelValues(string(component), "false").Add(0)
		metrics.readinessTransitions.WithLabelValues(string(component), "true").Add(0)
	}
	return metrics
}

var allReadinessComponents = [...]ReadinessComponent{
	ReadinessHTTP,
	ReadinessDatabase,
	ReadinessMigrations,
	ReadinessSchemaListener,
}

// Record records a valid bounded event. An invalid event has no side effect
// other than incrementing the single dropped-events counter.
func (m *Metrics) Record(_ context.Context, event Event) {
	if m == nil {
		return
	}
	switch event.kind {
	case EventKindOperation:
		if !validOperationEvent(event) {
			m.droppedEvents.Inc()
			return
		}
		labels := []string{string(event.component), string(event.operation), string(event.outcome)}
		m.operations.WithLabelValues(labels...).Inc()
		m.operationDuration.WithLabelValues(labels...).Observe(event.duration.Seconds())
	case EventKindTransactionRetry:
		if !validRetryCause(event.retryCause) {
			m.droppedEvents.Inc()
			return
		}
		m.transactionRetries.WithLabelValues(string(event.retryCause), strconv.FormatBool(event.exhausted)).Inc()
	case EventKindReadiness:
		if !validReadinessComponent(event.readinessComponent) {
			m.droppedEvents.Inc()
			return
		}
		m.recordReadiness(event.readinessComponent, event.ready)
	default:
		m.droppedEvents.Inc()
	}
}

func (m *Metrics) recordReadiness(component ReadinessComponent, ready bool) {
	m.readinessMu.Lock()
	defer m.readinessMu.Unlock()

	previous, seen := m.readinessState[component], m.readinessSeen[component]
	m.readiness.WithLabelValues(string(component)).Set(boolFloat(ready))
	if seen && previous != ready {
		m.readinessTransitions.WithLabelValues(string(component), strconv.FormatBool(ready)).Inc()
	}
	m.readinessState[component] = ready
	m.readinessSeen[component] = true
}

func validOperationEvent(event Event) bool {
	if event.duration < 0 || !validOutcome(event.outcome) {
		return false
	}
	component := componentForOperation(event.operation)
	return component != "" && component == event.component
}

func validOutcome(outcome Outcome) bool {
	switch outcome {
	case OutcomeSuccess, OutcomeFailure, OutcomeConflict, OutcomeValidation, OutcomeUnavailable,
		OutcomeCanceled, OutcomeInvalidCredentials, OutcomeRateLimited, OutcomeSetupUnavailable, OutcomeBudget:
		return true
	default:
		return false
	}
}

func validRetryCause(cause RetryCause) bool {
	return cause == RetryCauseCachedPlan || cause == RetryCauseSerialization || cause == RetryCauseDeadlock
}

func validReadinessComponent(component ReadinessComponent) bool {
	switch component {
	case ReadinessHTTP, ReadinessDatabase, ReadinessMigrations, ReadinessSchemaListener:
		return true
	default:
		return false
	}
}

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

// WrapHTTP records bounded HTTP metrics. Route labels come exclusively from
// ServeMux.Handler's registered pattern and never from Request.URL.Path.
func (m *Metrics) WrapHTTP(mux *http.ServeMux, next http.Handler) http.Handler {
	if m == nil {
		return next
	}
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		method := boundedHTTPMethod(request.Method)
		route := matchedRoute(mux, request)
		started := time.Now()
		m.httpInFlight.WithLabelValues(method, route).Inc()
		captured := &statusResponseWriter{ResponseWriter: response}
		defer func() {
			m.httpInFlight.WithLabelValues(method, route).Dec()
			status := boundedHTTPStatus(captured.statusCode())
			m.httpRequests.WithLabelValues(method, route, status).Inc()
			m.httpDuration.WithLabelValues(method, route, status).Observe(time.Since(started).Seconds())
		}()
		next.ServeHTTP(captured, request)
	})
}

func matchedRoute(mux *http.ServeMux, request *http.Request) string {
	if mux == nil || request == nil {
		return "unmatched"
	}
	_, pattern := mux.Handler(request)
	if pattern == "" {
		return "unmatched"
	}
	if _, route, found := strings.Cut(pattern, " "); found {
		pattern = route
	}
	if pattern == "" {
		return "unmatched"
	}
	return pattern
}

func boundedHTTPMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return method
	default:
		return "OTHER"
	}
}

func boundedHTTPStatus(status int) string {
	switch status / 100 {
	case 1:
		return "1xx"
	case 2:
		return "2xx"
	case 3:
		return "3xx"
	case 4:
		return "4xx"
	case 5:
		return "5xx"
	default:
		return "unknown"
	}
}

type statusResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusResponseWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

// Unwrap lets http.ResponseController retain access to optional capabilities
// such as flushing and hijacking on the original ResponseWriter.
func (w *statusResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusResponseWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// Handler serves this Metrics instance's private registry. The expected token
// is hashed at construction so the handler neither retains nor exposes it.
func (m *Metrics) Handler(token []byte) http.Handler {
	expectedHash := sha256.Sum256(token)
	var metricsHandler http.Handler = http.NotFoundHandler()
	if m != nil {
		metricsHandler = promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
			ErrorHandling:     promhttp.HTTPErrorOnError,
			CoalesceGather:    true,
			EnableOpenMetrics: true,
		})
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		if request.Method != http.MethodGet {
			response.Header().Set("Allow", http.MethodGet)
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		credential, ok := bearerCredential(request.Header)
		if !ok || !constantTimeTokenMatch(credential, expectedHash) {
			response.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		metricsHandler.ServeHTTP(response, request)
	})
}

func bearerCredential(header http.Header) ([]byte, bool) {
	values := header.Values("Authorization")
	if len(values) != 1 {
		return nil, false
	}
	scheme, credential, found := strings.Cut(values[0], " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || credential == "" ||
		strings.ContainsAny(credential, " \t\r\n,") {
		return nil, false
	}
	return []byte(credential), true
}

func constantTimeTokenMatch(credential []byte, expectedHash [sha256.Size]byte) bool {
	candidateHash := sha256.Sum256(credential)
	return subtle.ConstantTimeCompare(candidateHash[:], expectedHash[:]) == 1
}

type poolCollector struct {
	stats func() database.PoolStats

	acquireCount            *prometheus.Desc
	acquireDuration         *prometheus.Desc
	acquiredConns           *prometheus.Desc
	canceledAcquireCount    *prometheus.Desc
	constructingConns       *prometheus.Desc
	emptyAcquireCount       *prometheus.Desc
	emptyAcquireWaitTime    *prometheus.Desc
	idleConns               *prometheus.Desc
	maxConns                *prometheus.Desc
	maxIdleDestroyCount     *prometheus.Desc
	maxLifetimeDestroyCount *prometheus.Desc
	newConnsCount           *prometheus.Desc
	totalConns              *prometheus.Desc
}

func newPoolCollector(stats func() database.PoolStats) *poolCollector {
	if stats == nil {
		stats = func() database.PoolStats { return database.PoolStats{} }
	}
	newDesc := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName("pgbase", "database_pool", name), help, nil, nil)
	}
	return &poolCollector{
		stats:                   stats,
		acquireCount:            newDesc("acquire_count_total", "Total successful connection acquisitions."),
		acquireDuration:         newDesc("acquire_duration_seconds_total", "Cumulative duration of successful connection acquisitions in seconds."),
		acquiredConns:           newDesc("acquired_connections", "Current number of acquired connections."),
		canceledAcquireCount:    newDesc("canceled_acquire_count_total", "Total connection acquisitions canceled by context."),
		constructingConns:       newDesc("constructing_connections", "Current number of connections being constructed."),
		emptyAcquireCount:       newDesc("empty_acquire_count_total", "Total acquisitions that waited because the pool was empty."),
		emptyAcquireWaitTime:    newDesc("empty_acquire_wait_seconds_total", "Cumulative time waiting because the pool was empty in seconds."),
		idleConns:               newDesc("idle_connections", "Current number of idle connections."),
		maxConns:                newDesc("max_connections", "Configured maximum pool connections."),
		maxIdleDestroyCount:     newDesc("max_idle_destroy_count_total", "Total connections destroyed after exceeding maximum idle time."),
		maxLifetimeDestroyCount: newDesc("max_lifetime_destroy_count_total", "Total connections destroyed after exceeding maximum lifetime."),
		newConnsCount:           newDesc("new_connections_count_total", "Total connections constructed by the pool."),
		totalConns:              newDesc("total_connections", "Current total pool connections."),
	}
}

func (c *poolCollector) Describe(descriptions chan<- *prometheus.Desc) {
	for _, description := range c.descriptions() {
		descriptions <- description
	}
}

func (c *poolCollector) Collect(metrics chan<- prometheus.Metric) {
	stats := c.stats()
	metrics <- prometheus.MustNewConstMetric(c.acquireCount, prometheus.CounterValue, float64(stats.AcquireCount))
	metrics <- prometheus.MustNewConstMetric(c.acquireDuration, prometheus.CounterValue, stats.AcquireDuration.Seconds())
	metrics <- prometheus.MustNewConstMetric(c.acquiredConns, prometheus.GaugeValue, float64(stats.AcquiredConns))
	metrics <- prometheus.MustNewConstMetric(c.canceledAcquireCount, prometheus.CounterValue, float64(stats.CanceledAcquireCount))
	metrics <- prometheus.MustNewConstMetric(c.constructingConns, prometheus.GaugeValue, float64(stats.ConstructingConns))
	metrics <- prometheus.MustNewConstMetric(c.emptyAcquireCount, prometheus.CounterValue, float64(stats.EmptyAcquireCount))
	metrics <- prometheus.MustNewConstMetric(c.emptyAcquireWaitTime, prometheus.CounterValue, stats.EmptyAcquireWaitTime.Seconds())
	metrics <- prometheus.MustNewConstMetric(c.idleConns, prometheus.GaugeValue, float64(stats.IdleConns))
	metrics <- prometheus.MustNewConstMetric(c.maxConns, prometheus.GaugeValue, float64(stats.MaxConns))
	metrics <- prometheus.MustNewConstMetric(c.maxIdleDestroyCount, prometheus.CounterValue, float64(stats.MaxIdleDestroyCount))
	metrics <- prometheus.MustNewConstMetric(c.maxLifetimeDestroyCount, prometheus.CounterValue, float64(stats.MaxLifetimeDestroyCount))
	metrics <- prometheus.MustNewConstMetric(c.newConnsCount, prometheus.CounterValue, float64(stats.NewConnsCount))
	metrics <- prometheus.MustNewConstMetric(c.totalConns, prometheus.GaugeValue, float64(stats.TotalConns))
}

func (c *poolCollector) descriptions() []*prometheus.Desc {
	return []*prometheus.Desc{
		c.acquireCount,
		c.acquireDuration,
		c.acquiredConns,
		c.canceledAcquireCount,
		c.constructingConns,
		c.emptyAcquireCount,
		c.emptyAcquireWaitTime,
		c.idleConns,
		c.maxConns,
		c.maxIdleDestroyCount,
		c.maxLifetimeDestroyCount,
		c.newConnsCount,
		c.totalConns,
	}
}
