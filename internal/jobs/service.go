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
	"github.com/webisnoteasy/PgBase/internal/database"
)

var ErrInvalidSchedule = errors.New("invalid job schedule")
var ErrDuplicateHandler = errors.New("duplicate job handler")

const (
	// A pass runs handlers sequentially. Claiming one at a time preserves the
	// 30-second execution deadline below the one-minute lease.
	claimBatch    = 1
	leaseDuration = time.Minute
	jobTimeout    = 30 * time.Second
)

type Handler func(context.Context, Job) error

type Job struct {
	ID              uuid.UUID
	Name            string
	Schedule        string
	FenceGeneration int64
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
	pool     *database.Pool
	registry *Registry
	logger   *slog.Logger
	instance uuid.UUID
	timeout  time.Duration
}

func NewService(pool *database.Pool, registry *Registry, logger *slog.Logger, instance uuid.UUID) *Service {
	if registry == nil {
		registry = NewRegistry()
	}
	if logger == nil {
		logger = slog.Default()
	}
	if instance == uuid.Nil {
		instance = uuid.New()
	}
	return &Service{pool: pool, registry: registry, logger: logger, instance: instance, timeout: jobTimeout}
}

// Schedule creates or enables a named durable job. Changing its schedule makes
// it due immediately so a deployment does not wait for an obsolete interval.
func (s *Service) Schedule(ctx context.Context, name, schedule string) error {
	return s.ScheduleInTimezone(ctx, name, schedule, "UTC")
}

func (s *Service) ScheduleInTimezone(ctx context.Context, name, schedule, timezone string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("schedule job: name is required")
	}
	parsed, err := ParseScheduleInLocation(schedule, timezone)
	if err != nil {
		return err
	}
	next := time.Now()
	if parsed.interval == 0 {
		next = parsed.Next(next)
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO pgbase_meta.scheduled_jobs(id,name,schedule,timezone,next_run_at)
	 VALUES($1,$2,$3,$4,$5) ON CONFLICT (name) DO UPDATE SET
	 enabled=true,next_run_at=CASE WHEN pgbase_meta.scheduled_jobs.schedule=excluded.schedule
	 AND pgbase_meta.scheduled_jobs.timezone=$4 THEN pgbase_meta.scheduled_jobs.next_run_at ELSE $5 END,
	 schedule=excluded.schedule,timezone=$4,updated_at=clock_timestamp()`, uuid.New(), name, schedule, timezone, next)
	return err
}

// Run polls with a bounded cadence. Claims survive restarts because stale
// leases are eligible again, and execution never occurs inside the claim tx.
func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := s.RunOnce(ctx); err != nil && ctx.Err() == nil {
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
	claims, err := s.claim(ctx)
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
		runCtx, cancel := context.WithTimeout(ctx, s.timeout)
		err := invoke(runCtx, handler, claim.Job)
		cancel()
		if completeErr := s.complete(ctx, claim, err); completeErr != nil {
			return completeErr
		}
	}
	return nil
}

type claimedJob struct {
	Job
	token       uuid.UUID
	timezone    string
	scheduledAt time.Time
}

func (s *Service) claim(ctx context.Context) ([]claimedJob, error) {
	claims := make([]claimedJob, 0, claimBatch)
	names := s.registry.names()
	if len(names) == 0 {
		return claims, nil
	}
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		token := uuid.New()
		rows, err := tx.Query(ctx, `WITH due AS (
 SELECT id FROM pgbase_meta.scheduled_jobs
 WHERE enabled AND name=ANY($3::text[]) AND next_run_at<=clock_timestamp()
 AND (lease_until IS NULL OR lease_until<clock_timestamp())
 ORDER BY next_run_at,id FOR UPDATE SKIP LOCKED LIMIT $1
) UPDATE pgbase_meta.scheduled_jobs j SET lease_token=$2,
 lease_until=clock_timestamp()+interval '1 minute', fence_generation=fence_generation+1,
 attempts=attempts+1,last_started_at=clock_timestamp(),updated_at=clock_timestamp()
 FROM due WHERE j.id=due.id
	 RETURNING j.id,j.name,j.schedule,j.fence_generation,j.timezone,j.next_run_at`, claimBatch, token, names)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item claimedJob
			if err := rows.Scan(&item.ID, &item.Name, &item.Schedule, &item.FenceGeneration, &item.timezone, &item.scheduledAt); err != nil {
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
	if runErr == nil {
		if schedule.interval == 0 {
			next := schedule.Next(claim.scheduledAt)
			now := time.Now()
			if !next.After(now) {
				next = schedule.Next(now)
			}
			_, err = s.pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs SET lease_token=NULL,lease_until=NULL,
	 next_run_at=$3,last_scheduled_at=$4,attempts=0,last_error=NULL,last_finished_at=clock_timestamp(),updated_at=clock_timestamp()
	 WHERE id=$1 AND lease_token=$2 AND fence_generation=$5`, claim.ID, claim.token, next, claim.scheduledAt, claim.FenceGeneration)
			return err
		}
		_, err = s.pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs SET lease_token=NULL,lease_until=NULL,
	 next_run_at=clock_timestamp()+make_interval(secs=>$3),attempts=0,last_error=NULL,
	 last_finished_at=clock_timestamp(),updated_at=clock_timestamp()
	 WHERE id=$1 AND lease_token=$2 AND fence_generation=$4`, claim.ID, claim.token, schedule.interval.Seconds(), claim.FenceGeneration)
		return err
	}
	// The SQL bounds retries at five minutes, keeps failure text bounded, and
	// fences stale workers that lost a lease while their handler was running.
	_, err = s.pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs SET lease_token=NULL,lease_until=NULL,
 next_run_at=clock_timestamp()+make_interval(secs=>LEAST(300,power(2,LEAST(attempts,8))::integer)),
 last_error=left($3,1024),last_finished_at=clock_timestamp(),updated_at=clock_timestamp()
 WHERE id=$1 AND lease_token=$2 AND fence_generation=$4`, claim.ID, claim.token, runErr.Error(), claim.FenceGeneration)
	return err
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

func ParseScheduleInLocation(value, timezone string) (Schedule, error) {
	if interval, err := ParseSchedule(value); err == nil {
		return Schedule{interval: interval}, nil
	}
	location, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil {
		return Schedule{}, fmt.Errorf("%w: unknown timezone", ErrInvalidSchedule)
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	calendar, err := parser.Parse(strings.TrimSpace(value))
	if err != nil {
		return Schedule{}, fmt.Errorf("%w: expected @every <duration> or five-field cron", ErrInvalidSchedule)
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
