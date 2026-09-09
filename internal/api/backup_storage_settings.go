package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/webisnoteasy/PgBase/internal/backup"
)

type BackupStorageAdministration interface {
	Settings(context.Context) (backup.StorageSettings, error)
	Update(context.Context, backup.StorageUpdate) (backup.StorageSettings, error)
	Test(context.Context) error
}

func backupStorageAdministrationRoutes(service BackupStorageAdministration) []route {
	if service == nil {
		return nil
	}
	e := backupStorageEndpoints{service}
	return []route{
		{method: http.MethodGet, pattern: "/api/admin/storage/backups", access: accessAdminSameOriginRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.get)},
		{method: http.MethodPut, pattern: "/api/admin/storage/backups", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: http.HandlerFunc(e.put)},
		{method: http.MethodPost, pattern: "/api/admin/storage/backups/test", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.test)},
	}
}

type backupStorageEndpoints struct{ service BackupStorageAdministration }

func (e backupStorageEndpoints) get(w http.ResponseWriter, r *http.Request) {
	settings, err := e.service.Settings(r.Context())
	if err != nil {
		writeBackupStorageError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, settings)
}
func (e backupStorageEndpoints) put(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Driver       string `json:"driver"`
		Bucket       string `json:"bucket"`
		Prefix       string `json:"prefix"`
		Region       string `json:"region"`
		Endpoint     string `json:"endpoint"`
		AccessKey    string `json:"accessKey"`
		SecretKey    string `json:"secretKey"`
		UsePathStyle bool   `json:"usePathStyle"`
		ClearSecret  bool   `json:"clearSecret"`
		Retention    int    `json:"retention"`
		Revision     int64  `json:"revision"`
	}
	if decodeStrictJSON(r, &in) != nil {
		writeBackupStorageError(w, r, backup.ErrInvalidStorage)
		return
	}
	settings, err := e.service.Update(r.Context(), backup.StorageUpdate{StorageConfig: backup.StorageConfig{Driver: in.Driver, Bucket: in.Bucket, Prefix: in.Prefix, Region: in.Region, Endpoint: in.Endpoint, AccessKey: in.AccessKey, SecretKey: in.SecretKey, UsePathStyle: in.UsePathStyle, Retention: in.Retention}, ClearSecret: in.ClearSecret, Revision: in.Revision})
	if err != nil {
		writeBackupStorageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}
func (e backupStorageEndpoints) test(w http.ResponseWriter, r *http.Request) {
	if err := e.service.Test(r.Context()); err != nil {
		writeBackupStorageError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func writeBackupStorageError(w http.ResponseWriter, r *http.Request, err error) {
	response := APIError{Status: 503, Code: "backup_storage_unavailable", Message: "Backup storage settings are unavailable"}
	if errors.Is(err, backup.ErrInvalidStorage) {
		response = APIError{Status: 400, Code: "invalid_request", Message: "Invalid backup storage settings"}
	} else if errors.Is(err, backup.ErrStorageConflict) {
		response = APIError{Status: 409, Code: "storage_conflict", Message: "Backup storage changed or existing backups prevent this destination change"}
	}
	writeAPIError(w, r, response)
}
