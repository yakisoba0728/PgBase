// Package jobs runs named, durable interval jobs. A migration stores only a
// handler name and schedule; application code registers the handler itself.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/robfig/cron/v3"
	"github.com/yakisoba0728/PgBase/internal/cursor"
	"github.com/yakisoba0728/PgBase/internal/database"
)

var ErrInvalidSchedule = errors.New("invalid job schedule")
var ErrDuplicateHandler = errors.New("duplicate job handler")

const (
	// A pass runs handlers sequentially. Claiming one at a time preserves the
	// 30-second execution deadline below the one-minute lease.
	claimBatch           = 1
	leaseDuration        = time.Minute
	jobTimeout           = 30 * time.Second
	backupTimeout        = 30 * time.Minute
	backupLeaseHeartbeat = 15 * time.Second
)

type Handler func(context.Context, Job) error

type Job struct {
	ID              uuid.UUID
	Name            string
	Schedule        string
	FenceGeneration int64
	ScheduledAt     time.Time
}

type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

func NewRegistry() *Registry { return &Registry{handlers: make(map[string]Handler)} }

func (r *Registry) Register(name string, handler Handler) error {
	if strings.TrimSpace(name) == "" || handler == nil {
		return fmt.Errorf("register job handler: %w", ErrInvalidSchedule)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.handlers[name]; exists {
		return fmt.Errorf("register %q: %w", name, ErrDuplicateHandler)
	}
	r.handlers[name] = handler
	return nil
}

func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.handlers[name]; !ok {
		return false
	}
	delete(r.handlers, name)
	return true
}

func (r *Registry) handler(name string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	handler, ok := r.handlers[name]
	return handler, ok
}

func (r *Registry) names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.handlers))
	for name := range r.handlers {
		names = append(names, name)
	}
	return names
}

type Service struct {
	pool            *database.Pool
	registry        *Registry
	logger          *slog.Logger
	timeout         time.Duration
	backupHeartbeat time.Duration
	history         Options
	cursors         *cursor.Codec
}

func NewService(pool *database.Pool, registry *Registry, logger *slog.Logger) *Service {
	return NewServiceWithOptions(pool, registry, logger, Options{})
}

func NewServiceWithOptions(pool *database.Pool, registry *Registry, logger *slog.Logger, options Options) *Service {
	if registry == nil {
		registry = NewRegistry()
	}
	if logger == nil {
		logger = slog.Default()
	}
	options = options.normalized()
	return &Service{pool: pool, registry: registry, logger: logger, timeout: jobTimeout, backupHeartbeat: backupLeaseHeartbeat, history: options, cursors: cursor.NewCodec(options.CursorSecret)}
}

// Schedule creates or enables a named durable job. Changing its schedule makes
// an interval job due immediately; a calendar job uses its next occurrence.
// A schedule or timezone change replaces the pending occurrence and its retries.
// An active claim must finish or expire before its configuration can change.
func (s *Service) Schedule(ctx context.Context, name, schedule string) error {
	return s.ScheduleInTimezone(ctx, name, schedule, "UTC")
}

func (s *Service) ScheduleInTimezone(ctx context.Context, name, schedule, timezone string) error {
	if name == ManagedBackupName {
		return ErrNotManageable
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("schedule job: name is required")
	}
	if err := ValidateExplicitScheduleLocation(schedule, timezone); err != nil {
		return err
	}
	next, err := s.firstRunAt(ctx, schedule, timezone)
	if err != nil {
		return err
	}
	result, err := s.pool.Exec(ctx, `INSERT INTO pgbase_meta.scheduled_jobs(id,name,schedule,timezone,next_run_at)
	 VALUES($1,$2,$3,$4,COALESCE($5::timestamptz,statement_timestamp())) ON CONFLICT (name) DO UPDATE SET
	 enabled=true,next_run_at=CASE WHEN pgbase_meta.scheduled_jobs.schedule=excluded.schedule
	 AND pgbase_meta.scheduled_jobs.timezone=$4 THEN pgbase_meta.scheduled_jobs.next_run_at ELSE excluded.next_run_at END,
	 attempts=CASE WHEN pgbase_meta.scheduled_jobs.schedule=excluded.schedule
	 AND pgbase_meta.scheduled_jobs.timezone=$4 THEN pgbase_meta.scheduled_jobs.attempts ELSE 0 END,
	 last_scheduled_at=CASE WHEN pgbase_meta.scheduled_jobs.schedule=excluded.schedule
	 AND pgbase_meta.scheduled_jobs.timezone=$4 THEN pgbase_meta.scheduled_jobs.last_scheduled_at ELSE NULL END,
	 last_error=CASE WHEN pgbase_meta.scheduled_jobs.schedule=excluded.schedule
	 AND pgbase_meta.scheduled_jobs.timezone=$4 THEN pgbase_meta.scheduled_jobs.last_error ELSE NULL END,
	 schedule=excluded.schedule,timezone=$4,lease_token=NULL,lease_until=NULL,
	 fence_generation=pgbase_meta.scheduled_jobs.fence_generation+1,updated_at=clock_timestamp()
	 WHERE pgbase_meta.scheduled_jobs.lease_until IS NULL OR pgbase_meta.scheduled_jobs.lease_until<clock_timestamp()`, uuid.New(), name, schedule, timezone, next)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotManageable
	}
	return nil
}

// Run polls with a bounded cadence. Claims survive restarts because stale
// leases are eligible again, and execution never occurs inside the claim tx.
// Handlers receive a deadline but are extension code: a handler that ignores
// cancellation may run again after lease expiry. FenceGeneration protects job
// metadata updates; handlers with external side effects must be idempotent or
// apply their own generation fence.
func (s *Service) Run(ctx context.Context) error {
	if _, ok := s.registry.handler(ManagedBackupName); ok {
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = s.run(ctx, func() []string { return []string{ManagedBackupName} })
		}()
		defer func() { <-done }()
	}
	return s.run(ctx, s.operatorJobNames)
}

func (s *Service) run(ctx context.Context, names func() []string) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := s.runOnce(ctx, names()); err != nil && ctx.Err() == nil {
			s.logger.WarnContext(ctx, "scheduled job pass deferred")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) RunOnce(ctx context.Context) error {
	return s.runOnce(ctx, s.registry.names())
}

func (s *Service) runOnce(ctx context.Context, names []string) error {
	claims, err := s.claimNamed(ctx, names)
	if err != nil {
		return err
	}
	for _, claim := range claims {
		handler, ok := s.registry.handler(claim.Name)
		if !ok {
			if err := s.complete(ctx, claim, fmt.Errorf("no handler registered for %q", claim.Name)); err != nil {
				return err
			}
			continue
		}
		if claim.Name == ManagedBackupName {
			runErr := s.runManagedBackup(ctx, claim, handler)
			// A graceful stop can release the claim for retry before the lease expires.
			completeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			completeErr := s.complete(completeCtx, claim, runErr)
			cancel()
			if completeErr != nil {
				return completeErr
			}
			continue
		}
		runCtx, cancel := context.WithTimeout(ctx, s.timeout)
		err := invoke(runCtx, handler, claim.Job)
		cancel()
		if completeErr := s.complete(ctx, claim, err); completeErr != nil {
			return completeErr
		}
	}
	return nil
}

func (s *Service) runManagedBackup(ctx context.Context, claim claimedJob, handler Handler) error {
	runCtx, cancel := context.WithTimeout(ctx, backupTimeout)
	defer cancel()
	runCtx, loseClaim := context.WithCancelCause(runCtx)
	defer loseClaim(nil)
	heartbeatCtx, stopHeartbeat := context.WithCancel(runCtx)
	defer stopHeartbeat()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(s.backupHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
			}
			renewCtx, cancel := context.WithTimeout(heartbeatCtx, 5*time.Second)
			err := s.renewBackupLease(renewCtx, claim)
			cancel()
			if err != nil {
				if heartbeatCtx.Err() == nil {
					loseClaim(err)
				}
				return
			}
		}
	}()
	err := invoke(runCtx, handler, claim.Job)
	stopHeartbeat()
	<-stopped
	return errors.Join(err, context.Cause(runCtx))
}

func (s *Service) renewBackupLease(ctx context.Context, claim claimedJob) error {
	result, err := s.pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs SET lease_until=clock_timestamp()+make_interval(secs=>$4)
 WHERE id=$1 AND lease_token=$2 AND fence_generation=$3 AND lease_until>clock_timestamp()`, claim.ID, claim.token, claim.FenceGeneration, leaseDuration.Seconds())
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("managed backup lost its scheduler lease")
	}
	return nil
}

type claimedJob struct {
	Job
	token    uuid.UUID
	timezone string
	runID    int64
}

func (s *Service) claimNamed(ctx context.Context, names []string) ([]claimedJob, error) {
	claims := make([]claimedJob, 0, claimBatch)
	if len(names) == 0 {
		return claims, nil
	}
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		token := uuid.New()
		// The claim writes the history row itself, so every lease that can run
		// a handler has exactly one run row to complete.
		rows, err := tx.Query(ctx, `WITH due AS (
 SELECT id FROM pgbase_meta.scheduled_jobs
 WHERE enabled AND name=ANY($3::text[]) AND next_run_at<=clock_timestamp()
 AND (lease_until IS NULL OR lease_until<clock_timestamp())
 ORDER BY next_run_at,id FOR UPDATE SKIP LOCKED LIMIT $1
), claimed AS (UPDATE pgbase_meta.scheduled_jobs j SET lease_token=$2,
 lease_until=clock_timestamp()+make_interval(secs=>$4), fence_generation=fence_generation+1,
 last_scheduled_at=CASE WHEN j.attempts=0 OR j.last_scheduled_at IS NULL THEN j.next_run_at ELSE j.last_scheduled_at END,
 attempts=j.attempts+1,last_started_at=clock_timestamp(),updated_at=clock_timestamp()
 FROM due WHERE j.id=due.id
 RETURNING j.id,j.name,j.schedule,j.fence_generation,j.timezone,j.last_scheduled_at,j.attempts,j.last_started_at
), run AS (INSERT INTO pgbase_meta.scheduled_job_runs(job_id,fence_generation,attempt,status,scheduled_at,started_at)
 SELECT id,fence_generation,attempts,'running',last_scheduled_at,last_started_at FROM claimed
 RETURNING id,job_id
) SELECT c.id,c.name,c.schedule,c.fence_generation,c.timezone,c.last_scheduled_at,run.id
 FROM claimed c JOIN run ON run.job_id=c.id`, claimBatch, token, names, leaseDuration.Seconds())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item claimedJob
			if err := rows.Scan(&item.ID, &item.Name, &item.Schedule, &item.FenceGeneration, &item.timezone, &item.ScheduledAt, &item.runID); err != nil {
				return err
			}
			item.token = token
			claims = append(claims, item)
		}
		return rows.Err()
	})
	return claims, err
}

func (s *Service) complete(ctx context.Context, claim claimedJob, runErr error) error {
	schedule, err := ParseScheduleInLocation(claim.Schedule, claim.timezone)
	if err != nil {
		runErr = errors.Join(runErr, err)
	}
	var next *time.Time
	if runErr == nil && schedule.interval == 0 {
		next, err = s.nextCalendarRun(ctx, schedule, claim.ScheduledAt)
		if err != nil && !errors.Is(err, ErrInvalidSchedule) {
			return err
		}
		runErr = err
	}
	// Each statement records the run outcome together with the fenced job
	// update. A worker whose lease was lost or replaced still finishes its run
	// row, as superseded, without touching the job's current state.
	if runErr == nil {
		_, err = s.pool.Exec(ctx, `WITH job AS (UPDATE pgbase_meta.scheduled_jobs SET lease_token=NULL,lease_until=NULL,
	 next_run_at=COALESCE($3::timestamptz,clock_timestamp()+make_interval(secs=>$4)),
	 last_scheduled_at=$5,attempts=0,last_error=NULL,last_finished_at=clock_timestamp(),updated_at=clock_timestamp()
	 WHERE id=$1 AND lease_token=$2 AND fence_generation=$6 AND lease_until>clock_timestamp() RETURNING id
	) UPDATE pgbase_meta.scheduled_job_runs SET finished_at=clock_timestamp(),
	 status=CASE WHEN EXISTS (SELECT 1 FROM job) THEN 'succeeded' ELSE 'superseded' END
	 WHERE id=$7 AND job_id=$1 AND status='running'`, claim.ID, claim.token, next, schedule.interval.Seconds(), claim.ScheduledAt, claim.FenceGeneration, claim.runID)
	} else {
		// The SQL bounds retries at five minutes, keeps failure text bounded, and
		// fences stale workers that lost a lease while their handler was running.
		_, err = s.pool.Exec(ctx, `WITH job AS (UPDATE pgbase_meta.scheduled_jobs SET lease_token=NULL,lease_until=NULL,
 next_run_at=clock_timestamp()+make_interval(secs=>LEAST(300,power(2,LEAST(attempts,8))::integer)),
 last_error=left($3,1024),last_finished_at=clock_timestamp(),updated_at=clock_timestamp()
 WHERE id=$1 AND lease_token=$2 AND fence_generation=$4 AND lease_until>clock_timestamp() RETURNING id
) UPDATE pgbase_meta.scheduled_job_runs SET finished_at=clock_timestamp(),error=left($3,1024),
 status=CASE WHEN EXISTS (SELECT 1 FROM job) THEN 'failed' ELSE 'superseded' END
 WHERE id=$5 AND job_id=$1 AND status='running'`, claim.ID, claim.token, runErr.Error(), claim.FenceGeneration, claim.runID)
	}
	if err != nil {
		return err
	}
	s.pruneRuns(ctx, claim.ID)
	return nil
}

func invoke(ctx context.Context, handler Handler, job Job) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("job handler panic: %v", recovered)
		}
	}()
	return handler(ctx, job)
}

// ParseSchedule deliberately supports only @every durations. Durations are
// evaluated from PostgreSQL's UTC clock, have second precision, and are
// bounded to prevent a busy loop or accidental multi-day silent schedule.
func ParseSchedule(schedule string) (time.Duration, error) {
	value, ok := strings.CutPrefix(strings.TrimSpace(schedule), "@every ")
	if !ok {
		return 0, fmt.Errorf("%w: expected @every <duration>", ErrInvalidSchedule)
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < time.Second || duration > 24*time.Hour || duration%time.Second != 0 {
		return 0, fmt.Errorf("%w: duration must be 1s through 24h at second precision", ErrInvalidSchedule)
	}
	return duration, nil
}

type Schedule struct {
	interval time.Duration
	calendar cron.Schedule
}

// ValidateExplicitScheduleLocation rejects ambiguous locations on new writes.
// ParseScheduleInLocation still reads locations accepted by older versions.
func ValidateExplicitScheduleLocation(value, timezone string) error {
	if strings.TrimSpace(timezone) == "Local" {
		return fmt.Errorf("%w: choose an explicit IANA timezone", ErrInvalidSchedule)
	}
	if hasCronTimezone(strings.TrimSpace(value)) {
		return fmt.Errorf("%w: set the timezone in its own field", ErrInvalidSchedule)
	}
	return nil
}

func hasCronTimezone(value string) bool {
	return strings.HasPrefix(value, "TZ=") || strings.HasPrefix(value, "CRON_TZ=")
}

func ParseScheduleInLocation(value, timezone string) (Schedule, error) {
	location, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil || strings.TrimSpace(timezone) == "" {
		return Schedule{}, fmt.Errorf("%w: unknown timezone", ErrInvalidSchedule)
	}
	if interval, err := ParseSchedule(value); err == nil {
		return Schedule{interval: interval}, nil
	}
	value = strings.TrimSpace(value)
	if hasCronTimezone(value) && !strings.ContainsRune(value, ' ') {
		return Schedule{}, fmt.Errorf("%w: a cron timezone prefix must be followed by a schedule", ErrInvalidSchedule)
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	calendar, err := parser.Parse(value)
	if err != nil {
		return Schedule{}, fmt.Errorf("%w: expected @every <duration> or five-field cron", ErrInvalidSchedule)
	}
	if calendar.Next(time.Now().In(location)).IsZero() {
		return Schedule{}, fmt.Errorf("%w: cron has no next occurrence", ErrInvalidSchedule)
	}
	return Schedule{calendar: locationSchedule{location, calendar}}, nil
}

func (s Schedule) Next(after time.Time) time.Time {
	if s.interval > 0 {
		return after.Add(s.interval)
	}
	return s.calendar.Next(after)
}

type locationSchedule struct {
	location *time.Location
	schedule cron.Schedule
}

func (s locationSchedule) Next(after time.Time) time.Time {
	return s.schedule.Next(after.In(s.location))
}

// A nil first run lets the write make an interval job immediately due on the database clock.
func (s *Service) firstRunAt(ctx context.Context, value, timezone string) (*time.Time, error) {
	schedule, err := ParseScheduleInLocation(value, timezone)
	if err != nil || schedule.interval > 0 {
		return nil, err
	}
	return s.nextCalendarRun(ctx, schedule, time.Time{})
}

func (s *Service) nextCalendarRun(ctx context.Context, schedule Schedule, lastOccurrence time.Time) (*time.Time, error) {
	var now time.Time
	if err := s.pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return nil, err
	}
	if lastOccurrence.After(now) {
		now = lastOccurrence
	}
	next := schedule.Next(now)
	if next.IsZero() {
		return nil, fmt.Errorf("%w: cron has no next occurrence", ErrInvalidSchedule)
	}
	return &next, nil
}
