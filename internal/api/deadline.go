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
const backupTransferTimeout = 30 * time.Minute

// The domain context is what reaches the schema service and, through it, the
// pgx transaction, so the database stops working at the same moment the caller
// stops waiting.
func applyDeadline(policy deadlinePolicy, schemaApplyTimeout time.Duration, next http.Handler) http.Handler {
	if policy == deadlineDefault {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		timeout := schemaApplyTimeout
		if policy == deadlineBackupTransfer {
			timeout = backupTransferTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		deadline, _ := ctx.Deadline()
		controller := http.NewResponseController(w)
		if policy == deadlineBackupTransfer {
			if err := controller.SetReadDeadline(deadline); err != nil {
				writeDeadlineUnavailable(w, r, "backup_transfer_unavailable", "Backup transfer is unavailable")
				return
			}
		}
		// Set the write deadline before anything can be written. A failure
		// here means the response could be cut off mid-body later, which for
		// a committed DDL is worse than refusing the request outright, so it
		// ends the request before any header, body or domain call happens.
		if err := controller.SetWriteDeadline(deadline.Add(responseMargin)); err != nil {
			code, message := "schema_apply_unavailable", "Schema apply is unavailable"
			if policy == deadlineBackupTransfer {
				code, message = "backup_transfer_unavailable", "Backup transfer is unavailable"
			}
			writeDeadlineUnavailable(w, r, code, message)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeDeadlineUnavailable(w http.ResponseWriter, r *http.Request, code, message string) {
	w.Header().Set("Retry-After", "1")
	writeAPIError(w, r, APIError{Status: http.StatusServiceUnavailable, Code: code, Message: message})
}
