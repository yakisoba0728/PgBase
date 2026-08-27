// Package api provides the HTTP safety boundary for pgBase.
package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
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
	TokenSecret        []byte
	System             SystemStatusProvider
}

// NewRouter constructs the pgBase HTTP handler without starting a listener.
func NewRouter(dependencies Dependencies) http.Handler {
	logger := dependencies.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	mux := http.NewServeMux()
	admin := newAdminEndpoints(dependencies)
	schemas := schemaEndpoints{service: dependencies.Schema}
	records := recordEndpoints{service: dependencies.Records}
	mux.HandleFunc("GET /health/live", live)
	mux.Handle("GET /health/ready", ready(dependencies.Readiness))
	mux.Handle("POST /api/admin/setup", sameOriginMiddleware(admin.publicURL, admin.trustedProxies, http.HandlerFunc(admin.setup)))
	mux.Handle("POST /api/admin/login", sameOriginMiddleware(admin.publicURL, admin.trustedProxies, http.HandlerFunc(admin.login)))
	mux.Handle("GET /api/admin/session", adminSessionMiddleware(admin.auth, admin.tokenSecret, admin.publicURL, admin.trustedProxies, http.HandlerFunc(admin.currentSession)))
	mux.Handle("DELETE /api/admin/session", adminSessionMiddleware(admin.auth, admin.tokenSecret, admin.publicURL, admin.trustedProxies, http.HandlerFunc(admin.logout)))
	mux.Handle("POST /api/admin/collections/plan", authenticateAdminMiddleware(admin.auth, http.HandlerFunc(schemas.planCreate)))
	mux.Handle("POST /api/admin/collections/apply", adminSessionMiddleware(admin.auth, admin.tokenSecret, admin.publicURL, admin.trustedProxies, http.HandlerFunc(schemas.applyCreate)))
	mux.Handle("GET /api/admin/collections", authenticateAdminMiddleware(admin.auth, http.HandlerFunc(schemas.catalog)))
	mux.Handle("GET /api/admin/system", authenticateAdminMiddleware(admin.auth, systemStatus(dependencies.System)))
	mux.Handle("GET /api/v1/collections/{collection}/records", adminSessionMiddleware(admin.auth, admin.tokenSecret, admin.publicURL, admin.trustedProxies, http.HandlerFunc(records.list)))
	mux.Handle("POST /api/v1/collections/{collection}/records", adminSessionMiddleware(admin.auth, admin.tokenSecret, admin.publicURL, admin.trustedProxies, http.HandlerFunc(records.create)))
	mux.Handle("GET /api/v1/collections/{collection}/records/{id}", adminSessionMiddleware(admin.auth, admin.tokenSecret, admin.publicURL, admin.trustedProxies, http.HandlerFunc(records.get)))
	mux.Handle("PATCH /api/v1/collections/{collection}/records/{id}", adminSessionMiddleware(admin.auth, admin.tokenSecret, admin.publicURL, admin.trustedProxies, http.HandlerFunc(records.update)))
	mux.Handle("DELETE /api/v1/collections/{collection}/records/{id}", adminSessionMiddleware(admin.auth, admin.tokenSecret, admin.publicURL, admin.trustedProxies, http.HandlerFunc(records.delete)))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeAPIError(w, r, APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Not found"})
	})

	var handler http.Handler = mux
	handler = corsMiddleware(dependencies.CORSAllowedOrigins, handler)
	handler = securityHeadersMiddleware(handler)
	handler = bodyLimitMiddleware(handler)
	handler = accessLogMiddleware(logger, handler)
	handler = requestIDMiddleware(handler)
	return recoveryMiddleware(logger, handler)
}
