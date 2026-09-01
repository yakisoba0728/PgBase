package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const maxJSONBodyBytes = 1 << 20

type requestIDKey struct{}

func requestID(ctx context.Context) string {
	if value, ok := ctx.Value(requestIDKey{}).(string); ok {
		return value
	}
	return ""
}

func validRequestID(value string) bool {
	if len(value) < 8 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func newRequestID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "req_unavailable"
	}
	return "req_" + base64.RawURLEncoding.EncodeToString(bytes[:])
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validRequestID(id) {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

func recoveryMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buffered := &bufferedResponseWriter{header: make(http.Header)}
		defer func() {
			if recover() != nil {
				id := buffered.Header().Get("X-Request-ID")
				if id == "" {
					id = r.Header.Get("X-Request-ID")
					if !validRequestID(id) {
						id = newRequestID()
					}
				}
				w.Header().Set("X-Request-ID", id)
				w.Header().Set("X-Content-Type-Options", "nosniff")
				w.Header().Set("Referrer-Policy", "no-referrer")
				w.Header().Set("Content-Security-Policy", "default-src 'none'")
				r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))
				logger.Error("http handler panicked", "request_id", id)
				writeAPIError(w, r, APIError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Internal server error"})
				return
			}
			buffered.commit(w)
		}()
		next.ServeHTTP(buffered, r)
	})
}

type bufferedResponseWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *bufferedResponseWriter) Header() http.Header { return w.header }

func (w *bufferedResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *bufferedResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(body)
}

func (w *bufferedResponseWriter) commit(destination http.ResponseWriter) {
	for name, values := range w.header {
		for _, value := range values {
			destination.Header().Add(name, value)
		}
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	destination.WriteHeader(status)
	_, _ = destination.Write(w.body.Bytes())
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func accessLogMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		response := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(response, r)
		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		logger.Info("http request", "request_id", requestID(r.Context()), "method", r.Method, "route", requestRoute(r), "status", status, "duration_ms", time.Since(started).Milliseconds())
	})
}

// requestRoute returns only a registered ServeMux template. Raw request paths
// can contain collection names, record IDs, tokens, or other private values and
// must never be copied into access logs.
func requestRoute(request *http.Request) string {
	if request == nil || request.Pattern == "" {
		return "unmatched"
	}
	pattern := request.Pattern
	if _, route, found := strings.Cut(pattern, " "); found {
		pattern = route
	}
	if pattern == "" {
		return "unmatched"
	}
	return pattern
}

// bodyLimitMiddleware caps every request body, not only the ones whose
// Content-Type looks like JSON. The decoder that actually reads a body accepts
// a media type through mime.ParseMediaType, which trims Unicode spaces such as
// U+00A0 that a prefix match keeps, so any Content-Type gate here can disagree
// with the decoder and let an unauthenticated request stream an unbounded body
// into memory. No route accepts a body larger than maxJSONBodyBytes, so one
// unconditional limit keeps the two decisions from drifting apart again.
func bodyLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		next.ServeHTTP(w, r)
	})
}

func corsMiddleware(origins []string, next http.Handler) http.Handler {
	configured := make(map[string]struct{}, len(origins))
	invalid := false
	for _, origin := range origins {
		if origin == "*" {
			invalid = true
			continue
		}
		configured[origin] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" || len(origins) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Origin")
		if invalid {
			writeAPIError(w, r, APIError{Status: http.StatusForbidden, Code: "cors_forbidden", Message: "Origin is not allowed"})
			return
		}
		if _, ok := configured[origin]; !ok {
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				writeAPIError(w, r, APIError{Status: http.StatusForbidden, Code: "cors_forbidden", Message: "Origin is not allowed"})
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			method := r.Header.Get("Access-Control-Request-Method")
			if !allowedCORSMethod(method) || !allowedCORSHeaders(r.Header.Get("Access-Control-Request-Headers")) {
				writeAPIError(w, r, APIError{Status: http.StatusForbidden, Code: "cors_forbidden", Message: "Origin is not allowed"})
				return
			}
			setCORSHeaders(w, origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token, X-PgBase-CSRF, Idempotency-Key, If-Match")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		setCORSHeaders(w, origin)
		next.ServeHTTP(w, r)
	})
}

func setCORSHeaders(w http.ResponseWriter, origin string) {
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Access-Control-Expose-Headers", "ETag, Location, Idempotency-Replayed, PgBase-Compatibility")
}

func allowedCORSMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func allowedCORSHeaders(raw string) bool {
	if raw == "" {
		return true
	}
	for _, header := range strings.Split(raw, ",") {
		switch strings.ToLower(strings.TrimSpace(header)) {
		case "content-type", "authorization", "x-csrf-token", "x-pgbase-csrf", "idempotency-key", "if-match":
		default:
			return false
		}
	}
	return true
}
