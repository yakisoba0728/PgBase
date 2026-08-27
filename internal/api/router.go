// Package api provides the HTTP safety boundary for pgBase.
package api

import (
	"io"
	"log/slog"
	"net/http"
)

// Dependencies supplies components used by API handlers.
type Dependencies struct {
	Readiness          Readiness
	Logger             *slog.Logger
	CORSAllowedOrigins []string
}

// NewRouter constructs the pgBase HTTP handler without starting a listener.
func NewRouter(dependencies Dependencies) http.Handler {
	logger := dependencies.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", live)
	mux.Handle("GET /health/ready", ready(dependencies.Readiness))
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
