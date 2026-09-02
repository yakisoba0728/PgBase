package api

import (
	"context"
	"net/http"
	"time"
)

// responseMargin is the time reserved between the domain deadline and the
// server's write deadline. A schema apply that finishes at the very edge of
// its window still has to marshal and deliver its response, and a write
// deadline that expires first would turn a committed apply into a transport
// failure the client cannot tell from a rollback.
const responseMargin = 5 * time.Second

// The domain context is what reaches the schema service and, through it, the
// pgx transaction, so the database stops working at the same moment the caller
// stops waiting.
func applyDeadline(policy deadlinePolicy, schemaApplyTimeout time.Duration, next http.Handler) http.Handler {
	if policy != deadlineSchemaApply {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), schemaApplyTimeout)
		defer cancel()
		deadline, _ := ctx.Deadline()
		// Set the write deadline before anything can be written. A failure
		// here means the response could be cut off mid-body later, which for
		// a committed DDL is worse than refusing the request outright, so it
		// ends the request before any header, body or domain call happens.
		if err := http.NewResponseController(w).SetWriteDeadline(deadline.Add(responseMargin)); err != nil {
			w.Header().Set("Retry-After", "1")
			writeAPIError(w, r, APIError{
				Status:  http.StatusServiceUnavailable,
				Code:    "schema_apply_unavailable",
				Message: "Schema apply is unavailable",
			})
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
