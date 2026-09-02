package api

import (
	"context"
	"net/http"
)

// SystemStatus is the read-only operational state safe for administrator
// clients. Unavailable names every probe whose value could not be read for
// this sample, which is what separates a genuinely healthy zero from a value
// nobody could obtain: the numeric members keep their last successful reading
// while the probe that produced them is listed as unavailable.
type SystemStatus struct {
	Version           string              `json:"version"`
	DatabaseReachable bool                `json:"databaseReachable"`
	MigrationCurrent  int64               `json:"migrationCurrent"`
	MigrationLatest   int64               `json:"migrationLatest"`
	MigrationDirty    bool                `json:"migrationDirty"`
	CatalogVersion    int64               `json:"catalogVersion"`
	ListenerReady     bool                `json:"listenerReady"`
	Unavailable       []SystemStatusProbe `json:"unavailable"`
	SampledAt         string              `json:"sampledAt"`
	LastSuccessAt     string              `json:"lastSuccessAt"`
}

// SystemStatusProbe reports one component that could not be sampled. Code is a
// stable classification, never a database diagnostic, so a status page can be
// shown to an administrator without leaking connection or SQL detail.
type SystemStatusProbe struct {
	Component string `json:"component"`
	Code      string `json:"code"`
}

// SystemStatusProvider supplies a cached, read-only system status snapshot.
// Implementations must answer from a cached sample: a status request must not
// run database diagnostics of its own, or a slow database turns every status
// page load into a stalled request.
type SystemStatusProvider interface {
	Status(context.Context) SystemStatus
}

func systemStatus(provider SystemStatusProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := provider.Status(r.Context())
		if status.Unavailable == nil {
			status.Unavailable = []SystemStatusProbe{}
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, status)
	}
}
