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

// The three outcomes a diagnostic run can leave behind. They are not derivable
// from the finding list alone: a run that never completed and a run that found
// nothing both produce zero findings, and only one of those means the schema
// is known to be sound.
const (
	SchemaHealthHealthy     = "healthy"
	SchemaHealthDrift       = "drift"
	SchemaHealthUnavailable = "unavailable"
)

// SchemaHealth is the redacted result of the most recent background diagnostic
// run, in the vocabulary the system status already uses: Unavailable names the
// probe that could not be read, and the timestamps say when the value was last
// observed rather than leaving a stale reading to look fresh.
type SchemaHealth struct {
	State string `json:"state"`
	// Findings is the capped prefix of the run's findings and FindingCount is
	// what the run produced, so a client can tell that it is reading part of a
	// larger report instead of taking a truncated list for the whole one.
	Findings      []SchemaFinding     `json:"findings"`
	FindingCount  int                 `json:"findingCount"`
	Unavailable   []SystemStatusProbe `json:"unavailable"`
	SampledAt     string              `json:"sampledAt"`
	LastSuccessAt string              `json:"lastSuccessAt"`
	// Retries counts the consecutive attempts that could not complete since
	// the last one that did, which is what separates a diagnostic that has
	// missed a single sample from one that has not completed in hours.
	Retries int `json:"retries"`
}

// SchemaFinding is one detected problem named by a stable code. It carries no
// collection, field, object or row identifier and no database diagnostic:
// that is both what makes the report safe to render for an administrator and
// what bounds its size by the number of codes that exist rather than by the
// size of the database.
type SchemaFinding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// SchemaHealthProvider supplies the cached result of the most recent
// background diagnostic run. A run reads every managed table under a shared
// lock, so an implementation must answer from a cached sample: running it
// inside the request would hold those locks for the length of a page load and
// outlive any deadline a read route can carry.
type SchemaHealthProvider interface {
	Health(context.Context) SchemaHealth
}

func schemaHealth(provider SchemaHealthProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		health := provider.Health(r.Context())
		if health.Findings == nil {
			health.Findings = []SchemaFinding{}
		}
		if health.Unavailable == nil {
			health.Unavailable = []SystemStatusProbe{}
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, health)
	}
}
