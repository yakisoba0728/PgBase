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

type Dependencies struct {
	LifecycleHooks              LifecycleHooks
	CustomRoutes                []CustomRoute
	AuthAdministration          AuthAdministration
	RequestLog                  *observability.RequestLog
	Jobs                        ManagedJobs
	Backups                     ManagedBackups
	ServiceTokens               ServiceTokens
	RecordRules                 RecordRules
	AuthCollections             AuthCollections
	Files                       FileService
	StorageAdministration       StorageAdministration
	BackupStorageAdministration BackupStorageAdministration
	StorageMigrations           StorageMigrationAdministration
	MaximumFileUploadBytes      int64
	Readiness                   Readiness
	Logger                      *slog.Logger
	CORSAllowedOrigins          []string
	AdminAuth                   AdminAuth
	UserAuth                    UserAuth
	RecordAccess                RecordAccess
	Realtime                    Realtime
	RealtimeLifecycle           RealtimeLifecycle
	EmailActions                EmailActions
	AuthFactors                 AuthFactors
	UserRegistrationEnabled     bool
	Schema                      SchemaService
	Records                     RecordService
	PublicURL                   *url.URL
	TrustedProxies              []netip.Prefix
	System                      SystemStatusProvider
	SchemaHealth                SchemaHealthProvider
	AdminUI                     http.Handler
	Metrics                     *observability.Metrics
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
	records := recordEndpoints{service: dependencies.Records, files: dependencies.Files, hooks: dependencies.LifecycleHooks}
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
			response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault,
			maximumRequestBytes: maximumCredentialRequestBytes, handler: http.HandlerFunc(admin.setup)},
		{method: "POST", pattern: "/api/admin/auth/login", access: accessSameOrigin,
			response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault,
			maximumRequestBytes: maximumCredentialRequestBytes, handler: login},
		{method: "POST", pattern: "/api/admin/login", access: accessSameOrigin,
			response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault,
			maximumRequestBytes: maximumCredentialRequestBytes, handler: login},
		{method: "GET", pattern: "/api/admin/session", access: accessAdminSameOriginRead,
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
		// The recovery surface for a guarded apply. Both routes verify the
		// current administrator and the operation owner, so a receipt is only
		// ever readable by the administrator who created it.
		{method: "GET", pattern: "/api/admin/schema-operations", access: accessAdminSameOriginRead,
			response: atomicJSON(maximumOperationPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(schemas.operationHistory)},
		{method: "GET", pattern: "/api/admin/schema-operations/{id}", access: accessAdminSameOriginRead,
			response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(schemas.operationStatus)},
		{method: "GET", pattern: "/api/admin/collections", access: accessAdminSameOriginRead,
			response: atomicJSON(maximumCatalogResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(schemas.catalog)},
		{method: "GET", pattern: "/api/admin/system", access: accessAdminSameOriginRead,
			response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: systemStatus(dependencies.System)},
		// Cookie-authenticated reads reject a presented foreign Origin even when
		// it appears in the public CORS allowlist.
		{method: "GET", pattern: "/api/admin/system/health", access: accessAdminSameOriginRead,
			response: atomicJSON(maximumSchemaHealthResponseBytes), deadline: deadlineDefault,
			handler: schemaHealth(dependencies.SchemaHealth)},
		{method: "GET", pattern: "/api/v1/collections/{collection}/records", access: accessAdminSameOriginRead,
			response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.list)},
		{method: "POST", pattern: "/api/v1/collections/{collection}/records", access: accessAdminSession,
			response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumRecordRequestBytes, recordDocument: true, handler: http.HandlerFunc(records.create)},
		{method: "GET", pattern: "/api/v1/collections/{collection}/records/{id}", access: accessAdminSameOriginRead,
			response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.get)},
		{method: "PATCH", pattern: "/api/v1/collections/{collection}/records/{id}", access: accessAdminSession,
			response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumRecordRequestBytes, recordDocument: true, handler: http.HandlerFunc(records.update)},
		{method: "DELETE", pattern: "/api/v1/collections/{collection}/records/{id}", access: accessAdminSession,
			response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.delete)},
	}
	routes = append(routes, storageAdministrationRoutes(dependencies)...)
	routes = append(routes, storageMigrationRoutes(dependencies.StorageMigrations)...)
	routes = append(routes, backupStorageAdministrationRoutes(dependencies.BackupStorageAdministration)...)
	if dependencies.UserAuth != nil {
		users := userEndpoints{dependencies: dependencies}
		routes = append(routes, users.lifecycleRoutes()...)
		routes = append(routes, users.emailRoutes()...)
		routes = append(routes, users.factorRoutes()...)
		routes = append(routes,
			route{method: "POST", pattern: "/api/v1/auth/register", access: accessPublic, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: users.credentials(true)},
			route{method: "POST", pattern: "/api/v1/auth/login", access: accessPublic, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: users.credentials(false)},
			route{method: "GET", pattern: "/api/v1/auth/session", access: accessUserSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(users.session)},
			route{method: "POST", pattern: "/api/v1/auth/logout", access: accessUserSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(users.logout)},
			route{method: "GET", pattern: "/api/v1/user/collections/{collection}/records", access: accessUserSession, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.list)},
			route{method: "POST", pattern: "/api/v1/user/collections/{collection}/records", access: accessUserSession, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumRecordRequestBytes, recordDocument: true, handler: http.HandlerFunc(records.create)},
			route{method: "GET", pattern: "/api/v1/user/collections/{collection}/records/{id}", access: accessUserSession, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.get)},
			route{method: "PATCH", pattern: "/api/v1/user/collections/{collection}/records/{id}", access: accessUserSession, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumRecordRequestBytes, recordDocument: true, handler: http.HandlerFunc(records.update)},
			route{method: "DELETE", pattern: "/api/v1/user/collections/{collection}/records/{id}", access: accessUserSession, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.delete)},
			route{method: "GET", pattern: "/api/admin/collections/{collection}/user-access", access: accessAdminSameOriginRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(users.access)},
			route{method: "PUT", pattern: "/api/admin/collections/{collection}/user-access", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(users.access)},
		)
	}
	routes = append(routes, route{method: "POST", pattern: "/api/v1/collections/batch", access: accessAdminSession, response: atomicJSON(maximumBatchResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumMultipartBatchRequestBytes, streamedUpload: true, handler: http.HandlerFunc(records.batch)})
	if dependencies.UserAuth != nil {
		routes = append(routes, route{method: "POST", pattern: "/api/v1/user/collections/batch", access: accessUserSession, response: atomicJSON(maximumBatchResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumMultipartBatchRequestBytes, streamedUpload: true, handler: http.HandlerFunc(records.batch)})
	}
	routes = append(routes, authCollectionRoutes(dependencies)...)
	routes = append(routes, schemaDocumentRoutes(dependencies)...)
	routes = append(routes, authAdministrationRoutes(dependencies)...)
	routes = append(routes, operationsConsoleRoutes(dependencies)...)
	routes = append(routes, serviceTokenRoutes(dependencies)...)
	routes = append(routes, serviceRecordRoutes(dependencies)...)
	routes = append(routes, realtimeOperationRoutes(dependencies)...)
	routes = append(routes, ruleRoutes(dependencies)...)
	routes = append(routes, fileRoutes(dependencies)...)
	if dependencies.Realtime != nil && dependencies.UserAuth != nil {
		routes = append(routes, route{method: "GET", pattern: "/api/v1/user/collections/{collection}/events",
			access: accessUserSession, response: passthrough(), deadline: deadlineDefault, handler: realtimeHandler(dependencies)})
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
	extensions, err := dependencies.customRoutes()
	if err != nil {
		return nil, err
	}
	routes = append(routes, extensions...)
	mux := http.NewServeMux()
	for _, declared := range routes {
		// Buffering is innermost so the write deadline is installed on the
		// real connection before any byte is held, and so a rejected request
		// never allocates a buffer for a body it will not produce.
		baseHandler := declared.handler
		if declared.noStore {
			// Enforce the policy on the buffered response after an extension
			// handler returns, even if that handler tried to make it cacheable.
			baseHandler = noStoreCustomHandler(baseHandler)
		}
		handler := applyResponseMode(declared.response, baseHandler)
		handler = applyDeadline(declared.deadline, dependencies.SchemaApplyTimeout, handler)
		requestLimit := declared.maximumRequestBytes
		if requestLimit == 0 {
			requestLimit = maxJSONBodyBytes
		}
		handler = requestBodyLimitMiddleware(requestLimit, handler)
		handler = dependencies.admit(declared.access, handler)
		if declared.noStore {
			handler = noStoreCustomHandler(handler)
		}
		if err := registerRoute(mux, declared.muxPattern(), handler); err != nil {
			return nil, err
		}
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeAPIError(w, r, APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Not found"})
	})

	var handler http.Handler = mux
	handler = corsMiddleware(dependencies.CORSAllowedOrigins, handler)
	handler = securityHeadersMiddleware(handler)
	handler = accessLogMiddleware(logger, handler, dependencies.RequestLog)
	handler = requestIDMiddleware(handler)
	handler = recoveryMiddleware(logger, handler)
	if metricsEnabled {
		handler = dependencies.Metrics.WrapHTTP(mux, handler)
	}
	return handler, nil
}
