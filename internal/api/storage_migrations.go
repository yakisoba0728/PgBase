package api

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/filestore"
)

type StorageMigrationAdministration interface {
	Preview(context.Context, filestore.StorageUpdate) (filestore.StorageMigrationStatus, error)
	Start(context.Context, uuid.UUID, filestore.StorageUpdate) (filestore.StorageMigrationStatus, error)
	Status(context.Context, uuid.UUID) (filestore.StorageMigrationStatus, error)
	Latest(context.Context) (filestore.StorageMigrationStatus, error)
	Resume(context.Context, uuid.UUID) error
	Cancel(context.Context, uuid.UUID) error
}

func storageMigrationRoutes(service StorageMigrationAdministration) []route {
	if service == nil {
		return nil
	}
	e := storageMigrationEndpoints{service: service}
	return []route{
		{method: http.MethodPost, pattern: "/api/admin/storage/files/migrations/preview", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: http.HandlerFunc(e.preview)},
		{method: http.MethodPost, pattern: "/api/admin/storage/files/migrations", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: http.HandlerFunc(e.start)},
		{method: http.MethodGet, pattern: "/api/admin/storage/files/migrations/latest", access: accessAdminSameOriginRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.latest)},
		{method: http.MethodGet, pattern: "/api/admin/storage/files/migrations/{id}", access: accessAdminSameOriginRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.status)},
		{method: http.MethodPost, pattern: "/api/admin/storage/files/migrations/{id}/resume", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.resume)},
		{method: http.MethodPost, pattern: "/api/admin/storage/files/migrations/{id}/cancel", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.cancel)},
	}
}
func (e storageMigrationEndpoints) latest(w http.ResponseWriter, r *http.Request) {
	value, err := e.service.Latest(r.Context())
	if err != nil {
		writeStorageError(w, r, err)
		return
	}
	writeJSON(w, 200, value)
}

type storageMigrationEndpoints struct {
	service StorageMigrationAdministration
}

type storageMigrationInput struct {
	Driver       string `json:"driver"`
	Bucket       string `json:"bucket"`
	Region       string `json:"region"`
	Endpoint     string `json:"endpoint"`
	AccessKey    string `json:"accessKey"`
	SecretKey    string `json:"secretKey"`
	UsePathStyle bool   `json:"usePathStyle"`
	ClearSecret  bool   `json:"clearSecret"`
}

func (v storageMigrationInput) update() filestore.StorageUpdate {
	return filestore.StorageUpdate{Driver: v.Driver, Bucket: v.Bucket, Region: v.Region, Endpoint: v.Endpoint, AccessKey: v.AccessKey, SecretKey: v.SecretKey, UsePathStyle: v.UsePathStyle, ClearSecret: v.ClearSecret}
}
func (e storageMigrationEndpoints) preview(w http.ResponseWriter, r *http.Request) {
	var in storageMigrationInput
	if decodeStrictJSON(r, &in) != nil {
		writeStorageError(w, r, filestore.ErrInvalid)
		return
	}
	value, err := e.service.Preview(r.Context(), in.update())
	if err != nil {
		writeStorageError(w, r, err)
		return
	}
	writeJSON(w, 200, value)
}
func (e storageMigrationEndpoints) start(w http.ResponseWriter, r *http.Request) {
	var in storageMigrationInput
	if decodeStrictJSON(r, &in) != nil {
		writeStorageError(w, r, filestore.ErrInvalid)
		return
	}
	operationID, parseErr := uuid.Parse(r.Header.Get("PgBase-Operation-ID"))
	if parseErr != nil {
		writeStorageError(w, r, filestore.ErrInvalid)
		return
	}
	value, err := e.service.Start(r.Context(), operationID, in.update())
	if err != nil {
		writeStorageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, value)
}
func migrationID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeStorageError(w, r, filestore.ErrInvalid)
		return uuid.Nil, false
	}
	return id, true
}
func (e storageMigrationEndpoints) status(w http.ResponseWriter, r *http.Request) {
	id, ok := migrationID(w, r)
	if !ok {
		return
	}
	value, err := e.service.Status(r.Context(), id)
	if err != nil {
		writeStorageError(w, r, err)
		return
	}
	writeJSON(w, 200, value)
}
func (e storageMigrationEndpoints) resume(w http.ResponseWriter, r *http.Request) {
	id, ok := migrationID(w, r)
	if !ok {
		return
	}
	if err := e.service.Resume(r.Context(), id); err != nil {
		writeStorageError(w, r, err)
		return
	}
	value, err := e.service.Status(r.Context(), id)
	if err != nil {
		writeStorageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, value)
}
func (e storageMigrationEndpoints) cancel(w http.ResponseWriter, r *http.Request) {
	id, ok := migrationID(w, r)
	if !ok {
		return
	}
	if err := e.service.Cancel(r.Context(), id); err != nil {
		writeStorageError(w, r, err)
		return
	}
	value, err := e.service.Status(r.Context(), id)
	if err != nil {
		writeStorageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, value)
}
