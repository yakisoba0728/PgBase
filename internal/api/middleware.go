package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"github.com/yakisoba0728/PgBase/internal/apiratelimit"
	"github.com/yakisoba0728/PgBase/internal/observability"
	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
	"log/slog"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

func apiRateLimitMiddleware(limiter *apiratelimit.Service, trusted func() []netip.Prefix, pattern string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		address, ok := clientAddress(r, currentTrustedProxies(trusted))
		if !ok {
			address = "unknown"
		}
		actor := ""
		if p, ok := AdminPrincipalFromContext(r.Context()); ok {
			actor = "admin:" + p.Administrator.ID.String()
		}
		if p, ok := r.Context().Value(userPrincipalKey{}).(authenticatedUser); ok {
			actor = "user:" + p.principal.User.ID.String()
		}
		decision, err := limiter.Admit(r.Context(), r.Method, pattern, actor, address)
		if err != nil {
			writeAPIError(w, r, APIError{Status: http.StatusServiceUnavailable, Code: "rate_limit_unavailable", Message: "Rate limit unavailable"})
			return
		}
		if !decision.Allowed {
			w.Header().Set("Retry-After", strconv.FormatInt(int64(math.Ceil(decision.RetryAfter.Seconds())), 10))
			writeAPIError(w, r, APIError{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "Too many requests"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

const (
	maxJSONBodyBytes              = 1 << 20
	maximumCredentialRequestBytes = 16 << 10
)

type requestIDKey struct{}
type requestLogContextKey struct{}
type requestLogContext struct {
	clientIP, authKind, authID string
	includeAuth                bool
	skipSuccessActivityLog     bool
}

// SkipSuccessActivityLog records a request-local opt-out that the access log
// reads after its nested handler returns. Only successful responses are
// skipped; failures remain part of the diagnostic and security trail.
func SkipSuccessActivityLog(ctx context.Context) {
	if metadata, ok := ctx.Value(requestLogContextKey{}).(*requestLogContext); ok && metadata != nil {
		metadata.skipSuccessActivityLog = true
	}
}

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

// recoveryMiddleware answers a panic with the stable internal error. It does
// not buffer: an atomic route is buffered by its own response policy, so a
// panic there unwinds past a buffer that is never committed and this handler
// still writes onto a connection nothing has reached. A passthrough route has
// already streamed whatever it wrote, which is the cost of not buffering it.
func recoveryMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() == nil {
				return
			}
			id := w.Header().Get("X-Request-ID")
			if id == "" {
				id = r.Header.Get("X-Request-ID")
				if !validRequestID(id) {
					id = newRequestID()
				}
				w.Header().Set("X-Request-ID", id)
			}
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Content-Security-Policy", "default-src 'none'")
			r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))
			logger.Error("http handler panicked", "request_id", id)
			writeAPIError(w, r, APIError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Internal server error"})
		}()
		next.ServeHTTP(w, r)
	})
}

// errResponseTooLarge reaches the handler that overflowed its cap, so a
// handler learns its write failed at the same moment the router does instead
// of continuing to produce a body nobody will send.
var errResponseTooLarge = errors.New("response exceeds the route response cap")

// applyResponseMode installs the route's response policy. An atomic route is
// buffered whole so its cap is decided before a single byte is committed; a
// passthrough route is handed the connection because its body is a stream
// whose size the router does not own and must not hold.
func applyResponseMode(mode responseMode, next http.Handler) http.Handler {
	if !mode.atomic {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buffered := &bufferedResponseWriter{
			destination: w, header: make(http.Header), maximumBytes: mode.maximumBytes,
		}
		// Deliberately not deferred: a panic must leave the buffer
		// uncommitted so recoveryMiddleware answers on a clean connection.
		next.ServeHTTP(buffered, r)
		if buffered.overflowed {
			writeAPIError(w, r, APIError{
				Status:  http.StatusInternalServerError,
				Code:    "response_too_large",
				Message: "Response exceeds the transport limit",
			})
			return
		}
		buffered.commit(w)
	})
}

type bufferedResponseWriter struct {
	// destination is the writer this buffer will be committed to. It is held
	// so http.ResponseController can still reach the real connection: a
	// handler that needs a write deadline has to set it on the connection,
	// not on the buffer that will later be copied to it.
	destination http.ResponseWriter
	header      http.Header
	body        bytes.Buffer
	status      int
	// maximumBytes is the cap the route declared. Zero leaves the buffer
	// unbounded, which only a caller that already owns the body may ask for.
	maximumBytes int
	overflowed   bool
}

func (w *bufferedResponseWriter) Header() http.Header { return w.header }

func (w *bufferedResponseWriter) Unwrap() http.ResponseWriter { return w.destination }

// Flush is a no-op so http.ResponseController.Flush cannot walk Unwrap to the
// real connection and commit a header for a body this buffer has not yet
// decided to send.
func (w *bufferedResponseWriter) Flush() {}

func (w *bufferedResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *bufferedResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.overflowed {
		return 0, errResponseTooLarge
	}
	// Measured before appending, so the buffer never grows past the cap. A cap
	// checked afterwards would already have paid the memory it exists to
	// refuse.
	if w.maximumBytes > 0 && w.body.Len()+len(body) > w.maximumBytes {
		w.overflowed = true
		w.body.Reset()
		return 0, errResponseTooLarge
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

// Unwrap exposes the real connection writer so http.ResponseController can
// still reach capabilities this wrapper does not implement, such as the write
// deadline the schema apply route sets. Without it every wrapped route loses
// those capabilities silently.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

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

func accessLogMiddleware(logger *slog.Logger, settings *runtimesettings.Controller, trustedProxies func() []netip.Prefix, next http.Handler, logs ...*observability.RequestLog) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		configured := runtimesettings.Default().Logs
		if settings != nil {
			configured = settings.Get().Logs
		}
		metadata := &requestLogContext{includeAuth: configured.IncludeAuth}
		if configured.IncludeIP {
			if address, ok := clientIP(r, trustedProxies()); ok {
				metadata.clientIP = address
			}
		}
		r = r.WithContext(context.WithValue(r.Context(), requestLogContextKey{}, metadata))
		started := time.Now()
		response := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(response, r)
		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		if metadata.skipSuccessActivityLog && status >= http.StatusOK && status < http.StatusBadRequest {
			return
		}
		for _, log := range logs {
			log.RecordContext(r.Method, requestRoute(r), status, time.Since(started), metadata.clientIP, metadata.authKind, metadata.authID)
		}
		attrs := []any{"request_id", requestID(r.Context()), "method", r.Method, "route", requestRoute(r), "status", status, "duration_ms", time.Since(started).Milliseconds()}
		if metadata.clientIP != "" {
			attrs = append(attrs, "client_ip", metadata.clientIP)
		}
		if metadata.authID != "" {
			attrs = append(attrs, "auth_kind", metadata.authKind, "auth_id", metadata.authID)
		}
		switch configured.Level {
		case "debug":
			logger.Debug("http request", attrs...)
		case "warn":
			logger.Warn("http request", attrs...)
		case "error":
			logger.Error("http request", attrs...)
		default:
			logger.Info("http request", attrs...)
		}
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

// requestBodyLimitMiddleware caps every request body, not only the ones whose
// Content-Type looks like JSON. The decoder that actually reads a body accepts
// a media type through mime.ParseMediaType, which trims Unicode spaces such as
// U+00A0 that a prefix match keeps, so any Content-Type gate here can disagree
// with the decoder and let an unauthenticated request stream an unbounded body
// into memory. Every route applies its declared cap unconditionally; file uploads have a
// separate bounded streaming cap.
func requestBodyLimitMiddleware(maximumBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maximumBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		// API errors are as sensitive and time-dependent as successful bodies.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
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
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token, X-PgBase-CSRF, Idempotency-Key, If-Match, Last-Event-ID, X-PgBase-File-Size, X-PgBase-File-SHA256, PgBase-Operation-ID")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		setCORSHeaders(w, origin)
		next.ServeHTTP(w, r)
	})
}

func setCORSHeaders(w http.ResponseWriter, origin string) {
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Expose-Headers", "ETag, Location, Idempotency-Replayed, PgBase-Compatibility, PgBase-Operation-ID, Retry-After")
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
		case "content-type", "authorization", "x-csrf-token", "x-pgbase-csrf", "idempotency-key", "if-match", "last-event-id", "x-pgbase-file-size", "x-pgbase-file-sha256",
			"pgbase-operation-id":
		default:
			return false
		}
	}
	return true
}
