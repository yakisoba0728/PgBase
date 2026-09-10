package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/jobs"
	"github.com/yakisoba0728/PgBase/internal/observability"
)

type ManagedJobs interface {
	ListJobs(context.Context) ([]jobs.Status, error)
	ConfigureJob(context.Context, uuid.UUID, string, bool) error
	RequestRun(context.Context, uuid.UUID) error
}

type timezoneManagedJobs interface {
	ConfigureJobInTimezone(context.Context, uuid.UUID, string, string, bool) error
}

type ManagedBackups interface {
	List(context.Context) ([]backup.Entry, error)
	Create(context.Context) (backup.Job, error)
	Upload(context.Context, io.Reader) (backup.Entry, error)
	Open(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
	Restore(context.Context, string) (backup.Job, error)
	Job(context.Context, string) (backup.Job, error)
}

func operationsConsoleRoutes(d Dependencies) []route {
	routes := []route{}
	if d.Backups != nil {
		routes = append(routes, backupRoutes(d.Backups)...)
	}
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
			filter := observability.RequestFilter{Before: before, StatusClass: class, Method: r.URL.Query().Get("method"), Route: r.URL.Query().Get("route")}
			for key, destination := range map[string](**time.Time){"from": &filter.From, "to": &filter.To} {
				if value := r.URL.Query().Get(key); value != "" {
					parsed, parseErr := time.Parse(time.RFC3339, value)
					if parseErr != nil {
						writeOperationsError(w, r, jobs.ErrInvalidSchedule)
						return
					}
					*destination = &parsed
				}
			}
			page, searchErr := d.RequestLog.Search(r.Context(), filter)
			if searchErr != nil {
				writeOperationsError(w, r, searchErr)
				return
			}
			writeJSON(w, http.StatusOK, page)
		})})
		routes = append(routes,
			route{method: "GET", pattern: "/api/admin/logs/statistics", access: accessAdminSameOriginRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				stats, err := d.RequestLog.Statistics(r.Context())
				if err != nil {
					writeOperationsError(w, r, err)
					return
				}
				writeJSON(w, http.StatusOK, stats)
			})},
			route{method: "GET", pattern: "/api/admin/logs/{id}", access: accessAdminSameOriginRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
				if err != nil || id == 0 {
					writeOperationsError(w, r, jobs.ErrInvalidSchedule)
					return
				}
				entry, err := d.RequestLog.Detail(r.Context(), id)
				if err != nil {
					writeOperationsError(w, r, err)
					return
				}
				writeJSON(w, http.StatusOK, entry)
			})},
			route{method: "DELETE", pattern: "/api/admin/logs", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := d.RequestLog.Truncate(r.Context()); err != nil {
					writeOperationsError(w, r, err)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})},
		)
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
						Timezone string `json:"timezone"`
						Enabled  *bool  `json:"enabled"`
					}
					if decodeStrictJSON(r, &input) != nil || input.Enabled == nil {
						writeOperationsError(w, r, jobs.ErrInvalidSchedule)
						return
					}
					if strings.TrimSpace(input.Timezone) == "" {
						input.Timezone = "UTC"
					}
					if extended, ok := d.Jobs.(timezoneManagedJobs); ok {
						err = extended.ConfigureJobInTimezone(r.Context(), id, input.Schedule, input.Timezone, *input.Enabled)
					} else {
						err = d.Jobs.ConfigureJob(r.Context(), id, input.Schedule, *input.Enabled)
					}
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

func backupRoutes(manager ManagedBackups) []route {
	list := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items, err := manager.List(r.Context())
		if err != nil {
			writeBackupError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Items []backup.Entry `json:"items"`
		}{items})
	})
	create := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		job, err := manager.Create(r.Context())
		if err != nil {
			writeBackupError(w, r, err)
			return
		}
		writeJSON(w, http.StatusAccepted, job)
	})
	upload := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/gzip" {
			writeBackupError(w, r, jobs.ErrInvalidSchedule)
			return
		}
		entry, err := manager.Upload(r.Context(), r.Body)
		if err != nil {
			writeBackupError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, entry)
	})
	download := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		file, err := manager.Open(r.Context(), id)
		if err != nil {
			writeBackupError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="pgbase-%s.tar.gz"`, id))
		_, copyErr := io.Copy(w, contextBackupReader{ctx: r.Context(), reader: file})
		if errors.Join(copyErr, file.Close()) != nil {
			return
		}
	})
	remove := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := manager.Delete(r.Context(), r.PathValue("id")); err != nil {
			writeBackupError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	restore := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		job, err := manager.Restore(r.Context(), r.PathValue("id"))
		if err != nil {
			writeBackupError(w, r, err)
			return
		}
		writeJSON(w, http.StatusAccepted, job)
	})
	job := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		item, err := manager.Job(r.Context(), r.PathValue("id"))
		if err != nil {
			writeBackupError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	})
	return []route{
		{method: "GET", pattern: "/api/admin/backups", access: accessAdminSameOriginRead, response: atomicJSON(maximumCatalogResponseBytes), deadline: deadlineDefault, handler: list},
		{method: "POST", pattern: "/api/admin/backups", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: create},
		{method: "POST", pattern: "/api/admin/backups/upload", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineBackupTransfer, maximumRequestBytes: backup.MaximumArchiveBytes, streamedUpload: true, handler: upload},
		{method: "GET", pattern: "/api/admin/backups/{id}", access: accessAdminSameOriginRead, response: passthrough(), deadline: deadlineBackupTransfer, handler: download},
		{method: "DELETE", pattern: "/api/admin/backups/{id}", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: remove},
		{method: "POST", pattern: "/api/admin/backups/{id}/restore", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: restore},
		{method: "GET", pattern: "/api/admin/backup-jobs/{id}", access: accessAdminSameOriginRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: job},
	}
}

type contextBackupReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextBackupReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func writeBackupError(w http.ResponseWriter, r *http.Request, err error) {
	value := APIError{Status: 500, Code: "backup_unavailable", Message: "Backup operation failed"}
	if errors.Is(err, backup.ErrBusy) {
		value = APIError{Status: 409, Code: "backup_busy", Message: "Another backup operation is running"}
	}
	if errors.Is(err, backup.ErrNotFound) {
		value = APIError{Status: 404, Code: "backup_not_found", Message: "Backup or operation not found"}
	}
	if errors.Is(err, jobs.ErrInvalidSchedule) {
		value = APIError{Status: 415, Code: "invalid_backup_archive", Message: "A gzip backup archive is required"}
	}
	writeAPIError(w, r, value)
}

func writeOperationsError(w http.ResponseWriter, r *http.Request, err error) {
	value := APIError{Status: 500, Code: "operations_unavailable", Message: "Operations request failed"}
	if errors.Is(err, jobs.ErrInvalidSchedule) {
		value = APIError{Status: 400, Code: "invalid_request", Message: "Invalid operations request"}
	}
	if errors.Is(err, jobs.ErrNotManageable) {
		value = APIError{Status: 409, Code: "job_not_manageable", Message: "Job is missing, disabled, unregistered, or currently running"}
	}
	if errors.Is(err, observability.ErrRequestLogNotFound) {
		value = APIError{Status: 404, Code: "request_log_not_found", Message: "Request log entry not found"}
	}
	writeAPIError(w, r, value)
}
