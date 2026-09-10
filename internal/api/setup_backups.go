package api

import (
	"context"
	"errors"
	"github.com/yakisoba0728/PgBase/internal/backup"
	"mime"
	"net/http"
)

type setupAuthorizer interface {
	AuthorizeSetup(context.Context, string) error
}

func setupBackupRoutes(d Dependencies) []route {
	authorize := func(w http.ResponseWriter, r *http.Request) bool {
		auth, ok := d.AdminAuth.(setupAuthorizer)
		if !ok || auth.AuthorizeSetup(r.Context(), r.Header.Get("X-PgBase-Setup-Token")) != nil {
			writeAPIError(w, r, APIError{Status: http.StatusUnauthorized, Code: "setup_required", Message: "Setup authorization is required"})
			return false
		}
		return true
	}
	upload := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authorize(w, r) {
			return
		}
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
		if !authorize(w, r) {
			return
		}
		job, err := d.Backups.Restore(r.Context(), r.PathValue("id"))
		if err != nil {
			writeSetupBackupError(w, r, err)
			return
		}
		writeJSON(w, 202, job)
	})
	status := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authorize(w, r) {
			return
		}
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
