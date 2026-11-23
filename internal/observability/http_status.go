package observability

import (
	"context"
	"errors"
	"net/http"
)

// StatusClientClosedRequest is recorded, never sent, for a request whose
// client went away before the response was complete. It follows the nginx
// convention so log and metric consumers already know it, and it sorts into
// the 4xx class: a disconnecting client is not a server error.
const StatusClientClosedRequest = 499

// RecordedHTTPStatus is the status the request log, process log and HTTP
// metrics report. written is the status the handler wrote (0 when it wrote
// nothing) and aborted reports a handler that unwound with
// http.ErrAbortHandler.
//
// The request context is canceled with context.Canceled when the client
// disconnects. The server installs no base context, so nothing else cancels
// it while the handler runs. A handler that fails because its client left
// usually answers 500 on a connection nobody reads; that answer, an abort, or
// no answer at all is recorded as 499 so client disconnects do not count as
// server errors. A status below 500 that was already written stays, because
// the handler did produce it.
func RecordedHTTPStatus(ctx context.Context, written int, aborted bool) int {
	if (aborted || written == 0 || written >= http.StatusInternalServerError) && errors.Is(ctx.Err(), context.Canceled) {
		return StatusClientClosedRequest
	}
	if aborted {
		return http.StatusInternalServerError
	}
	if written == 0 {
		return http.StatusOK
	}
	return written
}
