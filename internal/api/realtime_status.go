package api

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/realtime"
)

type realtimeOperations interface {
	Status(context.Context) (realtime.Status, error)
	Retry(context.Context, uuid.UUID) error
}

func realtimeOperationRoutes(dependencies Dependencies) []route {
	operations, ok := dependencies.Realtime.(realtimeOperations)
	if !ok {
		return nil
	}
	return []route{
		{method: "GET", pattern: "/api/admin/realtime/status", access: accessAdminSameOriginRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			status, err := operations.Status(r.Context())
			if err != nil {
				writeRealtimeError(w, r, err)
				return
			}
			writeJSON(w, http.StatusOK, status)
		})},
		{method: "POST", pattern: "/api/admin/realtime/{eventId}/retry", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := uuid.Parse(r.PathValue("eventId"))
			if err != nil || id == uuid.Nil {
				writeRealtimeError(w, r, realtime.ErrCursorInvalid)
				return
			}
			if err := operations.Retry(r.Context(), id); err != nil {
				writeRealtimeError(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})},
	}
}
