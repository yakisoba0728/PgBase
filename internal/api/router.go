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
	PublicURL          *url.URL
	TrustedProxies     []netip.Prefix
	TokenSecret        []byte
}

// NewRouter constructs the pgBase HTTP handler without starting a listener.
func NewRouter(dependencies Dependencies) http.Handler {
	logger := dependencies.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	mux := http.NewServeMux()
	admin := newAdminEndpoints(dependencies)
	mux.HandleFunc("GET /health/live", live)
	mux.Handle("GET /health/ready", ready(dependencies.Readiness))
	mux.Handle("POST /api/admin/setup", sameOriginMiddleware(admin.publicURL, admin.trustedProxies, http.HandlerFunc(admin.setup)))
	mux.Handle("POST /api/admin/login", sameOriginMiddleware(admin.publicURL, admin.trustedProxies, http.HandlerFunc(admin.login)))
	mux.Handle("GET /api/admin/session", adminSessionMiddleware(admin.auth, admin.tokenSecret, admin.publicURL, admin.trustedProxies, http.HandlerFunc(admin.currentSession)))
	mux.Handle("DELETE /api/admin/session", adminSessionMiddleware(admin.auth, admin.tokenSecret, admin.publicURL, admin.trustedProxies, http.HandlerFunc(admin.logout)))
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
