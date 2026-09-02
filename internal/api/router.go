// Package api provides the HTTP safety boundary for pgBase.
package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"github.com/webisnoteasy/PgBase/internal/observability"
)

// Dependencies supplies components used by API handlers.
type Dependencies struct {
	Readiness          Readiness
	Logger             *slog.Logger
	CORSAllowedOrigins []string
	AdminAuth          AdminAuth
	Schema             SchemaService
	Records            RecordService
	PublicURL          *url.URL
	TrustedProxies     []netip.Prefix
	System             SystemStatusProvider
	AdminUI            http.Handler
	Metrics            *observability.Metrics
	// MetricsExposition is the already-authenticated exposition handler. The
	// caller builds it so the bearer secret never becomes a router
	// dependency; the metrics module hashes it on construction and keeps only
	// the hash.
	MetricsExposition http.Handler
	// SchemaApplyTimeout bounds the one route whose DDL may outlive the
	// default write timeout. Zero means the caller did not supply it and
	// construction fails rather than silently running an apply unbounded.
	SchemaApplyTimeout time.Duration
}

// declareRoutes is the single authoritative description of the HTTP surface.
// Access, response buffering and deadline are three independent axes, so each
// route names all three and a reviewer never has to reconstruct admission from
// the shape of a registration call.
func declareRoutes(dependencies Dependencies, metricsEnabled bool) []route {
	admin := newAdminEndpoints(dependencies)
	schemas := schemaEndpoints{service: dependencies.Schema}
	records := recordEndpoints{service: dependencies.Records}
	logout := http.HandlerFunc(admin.logout)
	login := http.HandlerFunc(admin.login)

	routes := []route{
		{method: "GET", pattern: "/health/live", access: accessPublic,
			response: atomicJSON(maximumHealthResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(live)},
		{method: "GET", pattern: "/health/ready", access: accessPublic,
			response: atomicJSON(maximumHealthResponseBytes), deadline: deadlineDefault, handler: ready(dependencies.Readiness)},
		{method: "GET", pattern: "/api/admin/status", access: accessPublic,
			response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(admin.status)},
		{method: "POST", pattern: "/api/admin/setup", access: accessSameOrigin,
			response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(admin.setup)},
		{method: "POST", pattern: "/api/admin/auth/login", access: accessSameOrigin,
			response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: login},
		{method: "POST", pattern: "/api/admin/login", access: accessSameOrigin,
			response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: login},
		{method: "GET", pattern: "/api/admin/session", access: accessAdminSession,
			response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(admin.currentSession)},
		{method: "POST", pattern: "/api/admin/auth/logout", access: accessAdminSession,
			response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: logout},
		{method: "DELETE", pattern: "/api/admin/session", access: accessAdminSession,
			response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: logout},
		{method: "POST", pattern: "/api/admin/collections/plan", access: accessAdminSession,
			response: atomicJSON(maximumCatalogResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(schemas.plan)},
		// Apply is the one route whose DDL legitimately outlives the default
		// write timeout, so it is the only holder of the schema-apply deadline.
		{method: "POST", pattern: "/api/admin/collections/apply", access: accessAdminSession,
			response: atomicJSON(maximumCatalogResponseBytes), deadline: deadlineSchemaApply, handler: http.HandlerFunc(schemas.apply)},
		{method: "GET", pattern: "/api/admin/collections", access: accessAdminBearer,
			response: atomicJSON(maximumCatalogResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(schemas.catalog)},
		{method: "GET", pattern: "/api/admin/system", access: accessAdminBearer,
			response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: systemStatus(dependencies.System)},
		{method: "GET", pattern: "/api/v1/collections/{collection}/records", access: accessAdminSession,
			response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.list)},
		{method: "POST", pattern: "/api/v1/collections/{collection}/records", access: accessAdminSession,
			response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.create)},
		{method: "GET", pattern: "/api/v1/collections/{collection}/records/{id}", access: accessAdminSession,
			response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.get)},
		{method: "PATCH", pattern: "/api/v1/collections/{collection}/records/{id}", access: accessAdminSession,
			response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.update)},
		{method: "DELETE", pattern: "/api/v1/collections/{collection}/records/{id}", access: accessAdminSession,
			response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.delete)},
	}
	if metricsEnabled {
		// Prometheus exposition streams and is not a JSON envelope, so it is
		// the one route that must not be buffered by the router.
		routes = append(routes, route{method: "GET", pattern: "/metrics", access: accessMetricsToken,
			response: passthrough(), deadline: deadlineDefault,
			handler: dependencies.MetricsExposition})
	}
	if dependencies.AdminUI != nil {
		// The embedded UI serves prebuilt assets of its own sizes and sets its
		// own cache headers, so it streams rather than being buffered here.
		routes = append(routes,
			route{pattern: "/admin", access: accessEmbeddedUI, response: passthrough(),
				deadline: deadlineDefault, handler: dependencies.AdminUI},
			route{pattern: "/admin/", access: accessEmbeddedUI, response: passthrough(),
				deadline: deadlineDefault, handler: dependencies.AdminUI},
		)
	}
	return routes
}

// NewRouter constructs the pgBase HTTP handler without starting a listener.
// It fails rather than returning a handler that would answer requests with an
// internal error for a module the caller forgot to supply.
func NewRouter(dependencies Dependencies) (http.Handler, error) {
	if err := dependencies.requiredModules(); err != nil {
		return nil, err
	}
	metricsEnabled := dependencies.Metrics != nil && dependencies.MetricsExposition != nil
	if !metricsEnabled {
		dependencies.Metrics = nil
	}
	logger := dependencies.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	routes := declareRoutes(dependencies, metricsEnabled)
	if err := validateRoutes(routes); err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	for _, declared := range routes {
		handler := applyDeadline(declared.deadline, dependencies.SchemaApplyTimeout, declared.handler)
		mux.Handle(declared.muxPattern(), dependencies.admit(declared.access, handler))
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeAPIError(w, r, APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Not found"})
	})

	var handler http.Handler = mux
	handler = corsMiddleware(dependencies.CORSAllowedOrigins, handler)
	handler = securityHeadersMiddleware(handler)
	handler = bodyLimitMiddleware(handler)
	handler = accessLogMiddleware(logger, handler)
	handler = requestIDMiddleware(handler)
	handler = recoveryMiddleware(logger, handler)
	if metricsEnabled {
		handler = dependencies.Metrics.WrapHTTP(mux, handler)
	}
	return handler, nil
}
