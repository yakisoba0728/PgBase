package pgbase

import (
	"context"
	"sync"
	"time"

	"github.com/webisnoteasy/PgBase/internal/api"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/doctor"
	"github.com/webisnoteasy/PgBase/internal/migrate"
)

const systemComponentDoctor = "doctor"

const (
	// A diagnostic run scans every managed table and takes a shared lock on
	// each one, so the interval and the deadline are a single trade-off: a
	// concurrent schema apply waits behind the run, and the deadline is the
	// bound on that wait, while the interval is how often an operator pays it.
	// Sixty seconds is long enough that a large membership scan is not
	// reported as unavailable, and five minutes is short enough that drift
	// introduced outside pgBase is visible within one coffee break.
	defaultSchemaHealthInterval = 5 * time.Minute
	defaultSchemaHealthTimeout  = 60 * time.Second
)

// schemaHealthSampler runs the same diagnostics the `pgbase doctor` command
// runs, on its own bounded schedule, and serves every HTTP request from the
// cached result.
//
// It calls doctor.Run rather than restating any of its comparisons: the CLI
// and the administrator UI have to agree about what drift is, and a second
// diagnosis path is how they would stop agreeing.
type schemaHealthSampler struct {
	pool   *database.Pool
	runner *migrate.Runner

	interval time.Duration
	timeout  time.Duration
	now      func() time.Time
	sampled  chan struct{}

	mu       sync.RWMutex
	snapshot api.SchemaHealth
}

func newSchemaHealthSampler(pool *database.Pool, runner *migrate.Runner) *schemaHealthSampler {
	sampler := &schemaHealthSampler{
		pool:     pool,
		runner:   runner,
		interval: defaultSchemaHealthInterval,
		timeout:  defaultSchemaHealthTimeout,
		now:      time.Now,
		sampled:  make(chan struct{}),
	}
	// Nothing has been diagnosed yet, which an administrator must be able to
	// tell apart from a database that was diagnosed and found sound.
	sampler.snapshot = api.SchemaHealth{
		State:       api.SchemaHealthUnavailable,
		Findings:    []api.SchemaFinding{},
		Unavailable: []api.SystemStatusProbe{{Component: systemComponentDoctor, Code: systemProbeNotSampled}},
	}
	return sampler
}

// Health returns the most recent diagnosis. It touches no database.
func (s *schemaHealthSampler) Health(context.Context) api.SchemaHealth {
	if s == nil {
		return api.SchemaHealth{State: api.SchemaHealthUnavailable, Findings: []api.SchemaFinding{}}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot := s.snapshot
	snapshot.Findings = append([]api.SchemaFinding(nil), s.snapshot.Findings...)
	snapshot.Unavailable = append([]api.SystemStatusProbe(nil), s.snapshot.Unavailable...)
	return snapshot
}

// Run diagnoses immediately and then on the configured interval until the
// context is cancelled, which is the app's shutdown signal.
func (s *schemaHealthSampler) Run(ctx context.Context) error {
	s.sample(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			s.sample(ctx)
		}
	}
}

func (s *schemaHealthSampler) sample(ctx context.Context) {
	runCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	report, err := doctor.Run(runCtx, s.pool, s.runner)
	s.observe(report, err)
	s.signalSampled()
}

// observe turns one diagnostic result into the cached payload. A run that
// stopped early still contributes the findings it did gather — those are real
// — but it is not allowed to look like a completed diagnosis, so the state
// stays unavailable and the last completed run keeps its timestamp.
func (s *schemaHealthSampler) observe(report doctor.Report, runErr error) {
	previous := s.Health(context.Background())
	sampledAt := s.now().UTC().Format(time.RFC3339Nano)

	next := api.SchemaHealth{
		FindingCount:  len(report.Findings),
		Unavailable:   []api.SystemStatusProbe{},
		SampledAt:     sampledAt,
		LastSuccessAt: previous.LastSuccessAt,
		Retries:       previous.Retries,
	}
	next.Findings = make([]api.SchemaFinding, 0, min(len(report.Findings), api.MaximumSchemaFindings))
	for _, finding := range report.Findings {
		if len(next.Findings) == api.MaximumSchemaFindings {
			break
		}
		next.Findings = append(next.Findings, api.SchemaFinding{
			Severity: finding.Severity, Code: finding.Code, Message: finding.Message,
		})
	}

	switch {
	case runErr != nil:
		next.State = api.SchemaHealthUnavailable
		next.Unavailable = append(next.Unavailable, api.SystemStatusProbe{
			Component: systemComponentDoctor, Code: systemStatusCode(runErr),
		})
		next.Retries++
	default:
		// Drift is a completed diagnosis, not a failed one: the run answered
		// the question it was asked, so it resets the retry counter and marks
		// the moment the schema was last actually observed.
		next.State = api.SchemaHealthHealthy
		if len(report.Findings) > 0 {
			next.State = api.SchemaHealthDrift
		}
		next.LastSuccessAt = sampledAt
		next.Retries = 0
	}

	s.mu.Lock()
	s.snapshot = next
	s.mu.Unlock()
}

// signalSampled closes the first-diagnosis gate exactly once. Only tests wait
// on it; a request served before it closes sees the undiagnosed snapshot.
func (s *schemaHealthSampler) signalSampled() {
	select {
	case <-s.sampled:
	default:
		close(s.sampled)
	}
}
