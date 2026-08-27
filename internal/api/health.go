package api

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Readiness reports whether a required application component is ready.
type Readiness interface {
	Ready(context.Context) error
}

type readinessFunc func(context.Context) error

func (f readinessFunc) Ready(ctx context.Context) error { return f(ctx) }

func live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func ready(dependency Readiness) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if dependency != nil {
			ctx, cancel := context.WithTimeout(r.Context(), time.Second)
			defer cancel()
			if err := dependency.Ready(ctx); err != nil {
				details := map[string][]string{}
				var components interface{ Components() []string }
				if errors.As(err, &components) {
					details["components"] = components.Components()
				}
				writeAPIError(w, r, APIError{Status: http.StatusServiceUnavailable, Code: "not_ready", Message: "Service is not ready", Details: details})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
