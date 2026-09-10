package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/yakisoba0728/PgBase/internal/filestore"
)

type StorageAdministration interface {
	Settings(context.Context) (filestore.StorageSettings, error)
	Update(context.Context, filestore.StorageUpdate) (filestore.StorageSettings, error)
	Test(context.Context) error
}

func storageAdministrationRoutes(dependencies Dependencies) []route {
	if dependencies.StorageAdministration == nil {
		return nil
	}
	e := storageAdministrationEndpoints{service: dependencies.StorageAdministration}
	return []route{
		{method: http.MethodGet, pattern: "/api/admin/storage/files", access: accessAdminSameOriginRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.get)},
		{method: http.MethodPut, pattern: "/api/admin/storage/files", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: http.HandlerFunc(e.put)},
		{method: http.MethodPost, pattern: "/api/admin/storage/files/test", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.test)},
	}
}

type storageAdministrationEndpoints struct{ service StorageAdministration }

func (e storageAdministrationEndpoints) get(w http.ResponseWriter, r *http.Request) {
	v, err := e.service.Settings(r.Context())
	if err != nil {
		writeStorageError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, v)
}
func (e storageAdministrationEndpoints) put(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Driver       string `json:"driver"`
		Bucket       string `json:"bucket"`
		Region       string `json:"region"`
		Endpoint     string `json:"endpoint"`
		AccessKey    string `json:"accessKey"`
		SecretKey    string `json:"secretKey"`
		UsePathStyle bool   `json:"usePathStyle"`
		ClearSecret  bool   `json:"clearSecret"`
		Revision     int64  `json:"revision"`
	}
	if decodeStrictJSON(r, &in) != nil {
		writeStorageError(w, r, filestore.ErrInvalid)
		return
	}
	v, err := e.service.Update(r.Context(), filestore.StorageUpdate{Driver: in.Driver, Bucket: in.Bucket, Region: in.Region, Endpoint: in.Endpoint, AccessKey: in.AccessKey, SecretKey: in.SecretKey, UsePathStyle: in.UsePathStyle, ClearSecret: in.ClearSecret, Revision: in.Revision})
	if err != nil {
		writeStorageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (e storageAdministrationEndpoints) test(w http.ResponseWriter, r *http.Request) {
	if err := e.service.Test(r.Context()); err != nil {
		writeStorageError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func writeStorageError(w http.ResponseWriter, r *http.Request, err error) {
	response := APIError{Status: http.StatusServiceUnavailable, Code: "storage_unavailable", Message: "Storage settings are unavailable"}
	switch {
	case errors.Is(err, filestore.ErrInvalid):
		response = APIError{Status: 400, Code: "invalid_request", Message: "Invalid storage settings"}
	case errors.Is(err, filestore.ErrStorageConflict):
		response = APIError{Status: 409, Code: "storage_conflict", Message: "Storage settings changed or existing files prevent this destination change"}
	case errors.Is(err, filestore.ErrNotFound):
		response = APIError{Status: 404, Code: "not_found", Message: "Storage migration was not found"}
	}
	writeAPIError(w, r, response)
}
