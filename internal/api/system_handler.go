package api

import (
	"context"
	"net/http"
)

// SystemStatus is the read-only operational state safe for administrator clients.
type SystemStatus struct {
	Version           string `json:"version"`
	DatabaseReachable bool   `json:"databaseReachable"`
	MigrationCurrent  int64  `json:"migrationCurrent"`
	MigrationLatest   int64  `json:"migrationLatest"`
	CatalogVersion    int64  `json:"catalogVersion"`
	ListenerReady     bool   `json:"listenerReady"`
}

// SystemStatusProvider supplies a cached, read-only system status snapshot.
type SystemStatusProvider interface {
	Status(context.Context) SystemStatus
}

func systemStatus(provider SystemStatusProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if provider == nil {
			writeAPIError(w, r, APIError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Internal server error"})
			return
		}
		status := provider.Status(r.Context())
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, status)
	}
}
