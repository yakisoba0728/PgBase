package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrNotManageable = errors.New("job is missing, unregistered, or currently running")

type Status struct {
	ID             uuid.UUID  `json:"id"`
	Name           string     `json:"name"`
	Schedule       string     `json:"schedule"`
	Timezone       string     `json:"timezone"`
	Enabled        bool       `json:"enabled"`
	Registered     bool       `json:"registered"`
	Running        bool       `json:"running"`
	NextRunAt      time.Time  `json:"nextRunAt"`
	LastStartedAt  *time.Time `json:"lastStartedAt,omitempty"`
	LastFinishedAt *time.Time `json:"lastFinishedAt,omitempty"`
	Attempts       int        `json:"attempts"`
	Failed         bool       `json:"failed"`
}

// EnsureScheduled supplies deployment defaults once. Administrator edits must
// survive process restarts; explicit Schedule remains available to embedders.
func (s *Service) EnsureScheduled(ctx context.Context, name, schedule string) error {
	return s.EnsureScheduledInTimezone(ctx, name, schedule, "UTC")
}

func (s *Service) EnsureScheduledInTimezone(ctx context.Context, name, schedule, timezone string) error {
	if _, ok := s.registry.handler(name); !ok {
		return ErrNotManageable
	}
	if _, err := ParseScheduleInLocation(schedule, timezone); err != nil {
		return err
	}
	return s.ensureScheduledInTimezone(ctx, name, schedule, timezone)
}

func (s *Service) ListJobs(ctx context.Context) ([]Status, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,name,schedule,timezone,enabled,
 COALESCE(lease_until>clock_timestamp(),false),next_run_at,last_started_at,last_finished_at,
 attempts,last_error IS NOT NULL FROM pgbase_meta.scheduled_jobs ORDER BY name LIMIT 501`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Status, 0)
	for rows.Next() {
		var item Status
		if err := rows.Scan(&item.ID, &item.Name, &item.Schedule, &item.Timezone, &item.Enabled, &item.Running, &item.NextRunAt, &item.LastStartedAt, &item.LastFinishedAt, &item.Attempts, &item.Failed); err != nil {
			return nil, err
		}
		_, item.Registered = s.registry.handler(item.Name)
		items = append(items, item)
	}
	if len(items) > 500 {
		return nil, errors.New("too many scheduled jobs for console")
	}
	return items, rows.Err()
}

func (s *Service) ConfigureJob(ctx context.Context, id uuid.UUID, schedule string, enabled bool) error {
	return s.ConfigureJobInTimezone(ctx, id, schedule, "UTC", enabled)
}

func (s *Service) ConfigureJobInTimezone(ctx context.Context, id uuid.UUID, schedule, timezone string, enabled bool) error {
	parsed, err := ParseScheduleInLocation(schedule, timezone)
	if err != nil {
		return err
	}
	next := time.Now()
	if parsed.interval == 0 {
		next = parsed.Next(next)
	}
	result, err := s.pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs SET schedule=$2,timezone=$3,enabled=$4,
	 next_run_at=CASE WHEN schedule<>$2 OR timezone<>$3 THEN $5 ELSE next_run_at END,updated_at=clock_timestamp()
	 WHERE id=$1 AND name=ANY($6::text[]) AND (lease_until IS NULL OR lease_until<clock_timestamp())`, id, schedule, timezone, enabled, next, s.registry.names())
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotManageable
	}
	return nil
}

func (s *Service) Register(ctx context.Context, name, schedule, timezone string, handler Handler) error {
	if _, err := ParseScheduleInLocation(schedule, timezone); err != nil {
		return err
	}
	if err := s.registry.Register(name, handler); err != nil {
		return err
	}
	if err := s.ensureScheduledInTimezone(ctx, name, schedule, timezone); err != nil {
		s.registry.Unregister(name)
		return err
	}
	return nil
}

func (s *Service) Unregister(ctx context.Context, name string) error {
	result, err := s.pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs SET enabled=false,updated_at=clock_timestamp() WHERE name=$1 AND (lease_until IS NULL OR lease_until<clock_timestamp())`, name)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotManageable
	}
	s.registry.Unregister(name)
	return nil
}

func (s *Service) ensureScheduledInTimezone(ctx context.Context, name, schedule, timezone string) error {
	parsed, err := ParseScheduleInLocation(schedule, timezone)
	if err != nil {
		return err
	}
	next := time.Now()
	if parsed.interval == 0 {
		next = parsed.Next(next)
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO pgbase_meta.scheduled_jobs(id,name,schedule,timezone,next_run_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT (name) DO NOTHING`, uuid.New(), name, schedule, timezone, next)
	return err
}

// RequestRun queues the normal worker; it never runs a handler on an HTTP
// goroutine or steals a lease. Disabled and unregistered jobs stay disabled.
func (s *Service) RequestRun(ctx context.Context, id uuid.UUID) error {
	result, err := s.pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs SET next_run_at=clock_timestamp(),updated_at=clock_timestamp()
 WHERE id=$1 AND enabled AND name=ANY($2::text[]) AND (lease_until IS NULL OR lease_until<clock_timestamp())`, id, s.registry.names())
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotManageable
	}
	return nil
}
