package api

import (
	"context"
	"errors"
	"mime"
	"net/http"

	"github.com/yakisoba0728/PgBase/internal/backup"
)

type setupGuarder interface {
	BeginSetupGuard(context.Context, string) (func(), error)
}
type setupRestorer interface {
	RestoreWithGuard(context.Context, string, func()) (backup.Job, error)
}

func setupBackupRoutes(d Dependencies) []route {
	authorize := func(w http.ResponseWriter, r *http.Request) (func(), bool) {
		auth, ok := d.AdminAuth.(setupGuarder)
		if !ok {
			writeAPIError(w, r, APIError{Status: http.StatusUnauthorized, Code: "setup_required", Message: "Setup authorization is required"})
			return nil, false
		}
		release, err := auth.BeginSetupGuard(r.Context(), r.Header.Get("X-PgBase-Setup-Token"))
		if err != nil {
			writeAPIError(w, r, APIError{Status: http.StatusUnauthorized, Code: "setup_required", Message: "Setup authorization is required"})
			return nil, false
		}
		return release, true
	}
	upload := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		release, ok := authorize(w, r)
		if !ok {
			return
		}
		defer release()
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/gzip" {
			writeAPIError(w, r, APIError{Status: 415, Code: "invalid_backup_archive", Message: "A gzip backup archive is required"})
			return
		}
		entry, err := d.Backups.Upload(r.Context(), r.Body)
		if err != nil {
			writeSetupBackupError(w, r, err)
			return
		}
		writeJSON(w, 201, entry)
	})
	restore := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		release, ok := authorize(w, r)
		if !ok {
			return
		}
		transferred := false
		defer func() {
			if !transferred {
				release()
			}
		}()
		restorer, ok := d.Backups.(setupRestorer)
		if !ok {
			writeSetupBackupError(w, r, errors.New("guarded restore is unavailable"))
			return
		}
		job, err := restorer.RestoreWithGuard(r.Context(), r.PathValue("id"), release)
		if err != nil {
			writeSetupBackupError(w, r, err)
			return
		}
		transferred = true
		writeJSON(w, 202, job)
	})
	status := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		release, ok := authorize(w, r)
		if !ok {
			return
		}
		defer release()
		job, err := d.Backups.Job(r.Context(), r.PathValue("id"))
		if err != nil {
			writeSetupBackupError(w, r, err)
			return
		}
		writeJSON(w, 200, job)
	})
	return []route{{method: "POST", pattern: "/api/admin/setup/backups/upload", access: accessPublic, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineBackupTransfer, maximumRequestBytes: backup.MaximumArchiveBytes, streamedUpload: true, handler: upload}, {method: "POST", pattern: "/api/admin/setup/backups/{id}/restore", access: accessPublic, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: restore}, {method: "GET", pattern: "/api/admin/setup/backup-jobs/{id}", access: accessPublic, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: status}}
}
func writeSetupBackupError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, backup.ErrNotFound) {
		writeAPIError(w, r, APIError{Status: 404, Code: "backup_not_found", Message: "Backup not found"})
		return
	}
	writeAPIError(w, r, APIError{Status: 503, Code: "backup_unavailable", Message: "Backup operation failed"})
}
