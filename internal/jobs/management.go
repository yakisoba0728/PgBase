package jobs

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
)

var ErrNotManageable = errors.New("job is missing, unregistered, or currently running")

const ManagedBackupName = "pgbase.managed-backup"

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

	SettingsManaged bool `json:"settingsManaged,omitempty"`
}

// EnsureScheduled supplies deployment defaults once. Administrator edits must
// survive process restarts; explicit Schedule remains available to embedders.
func (s *Service) EnsureScheduled(ctx context.Context, name, schedule string) error {
	return s.EnsureScheduledInTimezone(ctx, name, schedule, "UTC")
}

func (s *Service) EnsureScheduledInTimezone(ctx context.Context, name, schedule, timezone string) error {
	if name == ManagedBackupName {
		return ErrNotManageable
	}
	if _, ok := s.registry.handler(name); !ok {
		return ErrNotManageable
	}
	return s.ensureScheduledInTimezone(ctx, name, schedule, timezone, true)
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
		item.SettingsManaged = item.Name == ManagedBackupName
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
	if err := ValidateExplicitScheduleLocation(schedule, timezone); err != nil {
		return err
	}
	return s.configureJob(ctx, &id, schedule, timezone, enabled, s.operatorJobNames())
}

// ConfigureManagedBackup applies the schedule controlled by the runtime settings.
func (s *Service) ConfigureManagedBackup(ctx context.Context, schedule, timezone string, enabled bool) error {
	if _, ok := s.registry.handler(ManagedBackupName); !ok {
		return ErrNotManageable
	}
	return s.configureJob(ctx, nil, schedule, timezone, enabled, []string{ManagedBackupName})
}

func (s *Service) configureJob(ctx context.Context, id *uuid.UUID, schedule, timezone string, enabled bool, names []string) error {
	next, err := s.firstRunAt(ctx, schedule, timezone)
	if err != nil {
		return err
	}
	// Expiry allows an operator edit, but the expired handler may still return.
	// Revoke its token and generation in the same update as the accepted edit.
	// Replacing the schedule also starts a new occurrence; a no-op save keeps
	// the pending retry identity and its already-completed idempotent effects.
	result, err := s.pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs SET schedule=$2,timezone=$3,enabled=$4,
	 lease_token=NULL,lease_until=NULL,fence_generation=fence_generation+1,
	 attempts=CASE WHEN schedule<>$2 OR timezone<>$3 THEN 0 ELSE attempts END,
	 last_scheduled_at=CASE WHEN schedule<>$2 OR timezone<>$3 THEN NULL ELSE last_scheduled_at END,
	 last_error=CASE WHEN schedule<>$2 OR timezone<>$3 THEN NULL ELSE last_error END,
	 next_run_at=CASE WHEN schedule<>$2 OR timezone<>$3 THEN COALESCE($5::timestamptz,statement_timestamp()) ELSE next_run_at END,updated_at=clock_timestamp()
	 WHERE ($1::uuid IS NULL OR id=$1) AND name=ANY($6::text[]) AND (lease_until IS NULL OR lease_until<clock_timestamp())`, id, schedule, timezone, enabled, next, names)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotManageable
	}
	return nil
}

// Register installs a handler and creates its durable row when missing. A row
// created disabled cannot run before the caller configures it.
func (s *Service) Register(ctx context.Context, name, schedule, timezone string, enabled bool, handler Handler) error {
	if _, err := ParseScheduleInLocation(schedule, timezone); err != nil {
		return err
	}
	if err := s.registry.Register(name, handler); err != nil {
		return err
	}
	if err := s.ensureScheduledInTimezone(ctx, name, schedule, timezone, enabled); err != nil {
		s.registry.Unregister(name)
		return err
	}
	return nil
}

func (s *Service) Unregister(ctx context.Context, name string) error {
	if name == ManagedBackupName {
		return ErrNotManageable
	}
	result, err := s.pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs SET enabled=false,
 lease_token=NULL,lease_until=NULL,fence_generation=fence_generation+1,updated_at=clock_timestamp()
 WHERE name=$1 AND (lease_until IS NULL OR lease_until<clock_timestamp())`, name)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotManageable
	}
	s.registry.Unregister(name)
	return nil
}

func (s *Service) ensureScheduledInTimezone(ctx context.Context, name, schedule, timezone string, enabled bool) error {
	if locationErr := ValidateExplicitScheduleLocation(schedule, timezone); locationErr != nil {
		// Upgraded embedders may still supply a legacy default. The durable
		// definition wins when it already exists, just as it does on conflict.
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pgbase_meta.scheduled_jobs WHERE name=$1)`, name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}
		return locationErr
	}
	next, err := s.firstRunAt(ctx, schedule, timezone)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO pgbase_meta.scheduled_jobs(id,name,schedule,timezone,next_run_at,enabled) VALUES($1,$2,$3,$4,COALESCE($5::timestamptz,statement_timestamp()),$6) ON CONFLICT (name) DO NOTHING`, uuid.New(), name, schedule, timezone, next, enabled)
	return err
}

// RequestRun queues the normal worker; it never runs a handler on an HTTP
// goroutine or steals a lease. Disabled and unregistered jobs stay disabled.
func (s *Service) RequestRun(ctx context.Context, id uuid.UUID) error {
	result, err := s.pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs SET next_run_at=clock_timestamp(),
 lease_token=NULL,lease_until=NULL,fence_generation=fence_generation+1,updated_at=clock_timestamp()
 WHERE id=$1 AND enabled AND name=ANY($2::text[]) AND (lease_until IS NULL OR lease_until<clock_timestamp())`, id, s.operatorJobNames())
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotManageable
	}
	return nil
}

func (s *Service) operatorJobNames() []string {
	return slices.DeleteFunc(s.registry.names(), func(name string) bool { return name == ManagedBackupName })
}
