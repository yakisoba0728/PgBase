package pgbase

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/webisnoteasy/PgBase/internal/api"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

// The status components an administrator sees. They are label constants, not
// free text: everything a probe failure can say about itself has to be one of
// these plus one of the codes below, so no database diagnostic can reach a
// client through this endpoint.
const (
	systemComponentDatabase  = "database"
	systemComponentMigration = "migration"
	systemComponentCatalog   = "catalog"
	systemComponentListener  = "listener"
)

const (
	systemProbeNotSampled  = "not_sampled"
	systemProbeUnavailable = "unavailable"
	systemProbeTimeout     = "timeout"
	systemProbeFailed      = "failed"
)

const (
	defaultSystemStatusInterval     = 10 * time.Second
	defaultSystemStatusProbeTimeout = 2 * time.Second
)

// systemStatusSampler collects the operational state an administrator status
// page shows, on its own schedule and under its own per-probe deadline, and
// serves every HTTP request from the cached result.
//
// A request must never run the diagnostics itself. The migration probe reads
// committed history and the database probe waits for a connection, so on a
// database that is slow or out of connections an inline implementation makes
// every status page load wait for the same thing that is already broken, and a
// failed probe there had nowhere to go but a healthy-looking zero.
type systemStatusSampler struct {
	pool     *database.Pool
	runner   *migrate.Runner
	cache    *schema.Cache
	listener api.Readiness

	interval     time.Duration
	probeTimeout time.Duration
	now          func() time.Time
	sampled      chan struct{}

	mu       sync.RWMutex
	snapshot api.SystemStatus
}

func newSystemStatusSampler(
	pool *database.Pool,
	runner *migrate.Runner,
	cache *schema.Cache,
	listener api.Readiness,
) *systemStatusSampler {
	sampler := &systemStatusSampler{
		pool:         pool,
		runner:       runner,
		cache:        cache,
		listener:     listener,
		interval:     defaultSystemStatusInterval,
		probeTimeout: defaultSystemStatusProbeTimeout,
		now:          time.Now,
		sampled:      make(chan struct{}),
	}
	// Until the first sample lands nothing has been observed, which is not the
	// same as everything being fine at zero.
	sampler.snapshot = api.SystemStatus{
		Version: Version,
		Unavailable: []api.SystemStatusProbe{
			{Component: systemComponentDatabase, Code: systemProbeNotSampled},
			{Component: systemComponentMigration, Code: systemProbeNotSampled},
			{Component: systemComponentCatalog, Code: systemProbeNotSampled},
			{Component: systemComponentListener, Code: systemProbeNotSampled},
		},
	}
	return sampler
}

// Status returns the most recent sample. It touches no database.
func (s *systemStatusSampler) Status(context.Context) api.SystemStatus {
	if s == nil {
		return api.SystemStatus{Version: Version}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot := s.snapshot
	snapshot.Unavailable = append([]api.SystemStatusProbe(nil), s.snapshot.Unavailable...)
	return snapshot
}

func (s *systemStatusSampler) Run(ctx context.Context) error {
	return sampleLoop(ctx, s.interval, s.sample)
}

func (s *systemStatusSampler) sample(ctx context.Context) {
	previous := s.Status(ctx)
	next := api.SystemStatus{Version: Version}
	failures := make([]api.SystemStatusProbe, 0, 4)

	if err := s.probeDatabase(ctx); err != nil {
		failures = append(failures, api.SystemStatusProbe{Component: systemComponentDatabase, Code: systemStatusCode(err)})
	} else {
		next.DatabaseReachable = true
	}

	status, err := s.probeMigration(ctx)
	if err != nil {
		// The last successful reading is kept so an operator can still see which
		// version the database reached; the probe entry is what says the value
		// is no longer being confirmed.
		next.MigrationCurrent = previous.MigrationCurrent
		next.MigrationLatest = previous.MigrationLatest
		next.MigrationDirty = previous.MigrationDirty
		failures = append(failures, api.SystemStatusProbe{Component: systemComponentMigration, Code: systemStatusCode(err)})
	} else {
		next.MigrationCurrent = status.Current
		next.MigrationLatest = status.Latest
		next.MigrationDirty = status.Dirty
	}

	if s.cache == nil {
		next.CatalogVersion = previous.CatalogVersion
		failures = append(failures, api.SystemStatusProbe{Component: systemComponentCatalog, Code: systemProbeUnavailable})
	} else {
		next.CatalogVersion = s.cache.Load().Version
	}

	if err := s.probeListener(ctx); err != nil {
		failures = append(failures, api.SystemStatusProbe{Component: systemComponentListener, Code: systemStatusCode(err)})
	} else {
		next.ListenerReady = true
	}

	sampledAt := s.now().UTC()
	next.SampledAt = sampledAt.Format(time.RFC3339Nano)
	next.Unavailable = failures
	next.LastSuccessAt = previous.LastSuccessAt
	if len(failures) == 0 {
		next.LastSuccessAt = next.SampledAt
	}

	s.mu.Lock()
	s.snapshot = next
	s.mu.Unlock()
	closeGate(s.sampled)
}

func (s *systemStatusSampler) probeDatabase(ctx context.Context) error {
	if s.pool == nil {
		return errSystemProbeUnavailable
	}
	probeCtx, cancel := context.WithTimeout(ctx, s.probeTimeout)
	defer cancel()
	return s.pool.Ping(probeCtx)
}

func (s *systemStatusSampler) probeMigration(ctx context.Context) (migrate.Status, error) {
	if s.runner == nil {
		return migrate.Status{}, errSystemProbeUnavailable
	}
	probeCtx, cancel := context.WithTimeout(ctx, s.probeTimeout)
	defer cancel()
	return s.runner.Status(probeCtx)
}

func (s *systemStatusSampler) probeListener(ctx context.Context) error {
	if s.listener == nil {
		return errSystemProbeUnavailable
	}
	probeCtx, cancel := context.WithTimeout(ctx, s.probeTimeout)
	defer cancel()
	return s.listener.Ready(probeCtx)
}

var errSystemProbeUnavailable = errors.New("system status probe is unavailable")

// systemStatusCode turns any probe failure into one of the closed codes above.
// A raw PostgreSQL error never reaches a client: it can carry the connection
// string, a SQL fragment or a server diagnostic, none of which an
// administrator status page needs in order to act.
func systemStatusCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errSystemProbeUnavailable):
		return systemProbeUnavailable
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return systemProbeTimeout
	}
	switch database.Classify(err) {
	case database.KindUnavailable:
		return systemProbeUnavailable
	case database.KindCanceled:
		return systemProbeTimeout
	default:
		return systemProbeFailed
	}
}
