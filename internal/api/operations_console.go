package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/jobs"
)

type ManagedJobs interface {
	ListJobs(context.Context) ([]jobs.Status, error)
	ConfigureJob(context.Context, uuid.UUID, string, bool) error
	RequestRun(context.Context, uuid.UUID) error
}

func operationsConsoleRoutes(d Dependencies) []route {
	routes := []route{}
	if d.RequestLog != nil {
		routes = append(routes, route{method: "GET", pattern: "/api/admin/logs", access: accessAdminSameOriginRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var before uint64
			var class int
			var err error
			if value := r.URL.Query().Get("before"); value != "" {
				before, err = strconv.ParseUint(value, 10, 64)
				if err != nil || before == 0 {
					writeOperationsError(w, r, jobs.ErrInvalidSchedule)
					return
				}
			}
			if value := r.URL.Query().Get("statusClass"); value != "" {
				class, err = strconv.Atoi(value)
				if err != nil || class < 1 || class > 5 {
					writeOperationsError(w, r, jobs.ErrInvalidSchedule)
					return
				}
			}
			writeJSON(w, http.StatusOK, d.RequestLog.Page(before, class))
		})})
	}
	// Listing is available even when this binary has no registered workers.
	routes = append(routes, route{method: "GET", pattern: "/api/admin/jobs", access: accessAdminSameOriginRead, response: atomicJSON(maximumCatalogResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items := []jobs.Status{}
		if d.Jobs != nil {
			var err error
			items, err = d.Jobs.ListJobs(r.Context())
			if err != nil {
				writeOperationsError(w, r, err)
				return
			}
		}
		writeJSON(w, http.StatusOK, struct {
			Items         []jobs.Status `json:"items"`
			WorkerEnabled bool          `json:"workerEnabled"`
		}{items, d.Jobs != nil})
	})})
	if d.Jobs != nil {
		for _, method := range []string{"PUT", "POST"} {
			pattern := "/api/admin/jobs/{id}"
			if method == "POST" {
				pattern += "/run"
			}
			routes = append(routes, route{method: method, pattern: pattern, access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				id, err := uuid.Parse(r.PathValue("id"))
				if err != nil {
					writeOperationsError(w, r, jobs.ErrInvalidSchedule)
					return
				}
				if r.Method == "PUT" {
					var input struct {
						Schedule string `json:"schedule"`
						Enabled  *bool  `json:"enabled"`
					}
					if decodeStrictJSON(r, &input) != nil || input.Enabled == nil {
						writeOperationsError(w, r, jobs.ErrInvalidSchedule)
						return
					}
					err = d.Jobs.ConfigureJob(r.Context(), id, input.Schedule, *input.Enabled)
				} else {
					err = d.Jobs.RequestRun(r.Context(), id)
				}
				if err != nil {
					writeOperationsError(w, r, err)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})})
		}
	}
	return routes
}

func writeOperationsError(w http.ResponseWriter, r *http.Request, err error) {
	value := APIError{Status: 500, Code: "operations_unavailable", Message: "Operations request failed"}
	if errors.Is(err, jobs.ErrInvalidSchedule) {
		value = APIError{Status: 400, Code: "invalid_request", Message: "Invalid operations request"}
	}
	if errors.Is(err, jobs.ErrNotManageable) {
		value = APIError{Status: 409, Code: "job_not_manageable", Message: "Job is missing, disabled, unregistered, or currently running"}
	}
	writeAPIError(w, r, value)
}
