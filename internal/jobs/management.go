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
	if _, ok := s.registry.handler(name); !ok {
		return ErrNotManageable
	}
	if _, err := ParseSchedule(schedule); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO pgbase_meta.scheduled_jobs(id,name,schedule,next_run_at)
 VALUES($1,$2,$3,clock_timestamp()) ON CONFLICT (name) DO NOTHING`, uuid.New(), name, schedule)
	return err
}

func (s *Service) ListJobs(ctx context.Context) ([]Status, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,name,schedule,enabled,
 COALESCE(lease_until>clock_timestamp(),false),next_run_at,last_started_at,last_finished_at,
 attempts,last_error IS NOT NULL FROM pgbase_meta.scheduled_jobs ORDER BY name LIMIT 501`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Status, 0)
	for rows.Next() {
		var item Status
		if err := rows.Scan(&item.ID, &item.Name, &item.Schedule, &item.Enabled, &item.Running, &item.NextRunAt, &item.LastStartedAt, &item.LastFinishedAt, &item.Attempts, &item.Failed); err != nil {
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
	if _, err := ParseSchedule(schedule); err != nil {
		return err
	}
	result, err := s.pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs SET schedule=$2,enabled=$3,
 next_run_at=CASE WHEN schedule<>$2 THEN clock_timestamp() ELSE next_run_at END,updated_at=clock_timestamp()
 WHERE id=$1 AND name=ANY($4::text[]) AND (lease_until IS NULL OR lease_until<clock_timestamp())`, id, schedule, enabled, s.registry.names())
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotManageable
	}
	return nil
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
