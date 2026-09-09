package pgbase

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/jobs"
)

// ScheduledJob binds a durable database schedule to application code. Schedule
// accepts @every durations or standard five-field calendar cron expressions.
type ScheduledJob struct {
	Name     string
	Schedule string
	Timezone string
	Handler  func(context.Context, JobRun) error
}

// JobRun identifies one fenced execution attempt. A job is at-least-once:
// a process that ignores its context can outlive its lease and be retried.
// Use RunKey as the idempotency key for external side effects, or compare
// FenceGeneration while committing an effect in the same PostgreSQL database.
type JobRun struct {
	JobID           uuid.UUID
	Name            string
	FenceGeneration int64
}

func (r JobRun) RunKey() string {
	return fmt.Sprintf("%s:%d", r.JobID, r.FenceGeneration)
}

func validateScheduledJobs(configured []ScheduledJob) error {
	seen := make(map[string]struct{}, len(configured))
	for _, job := range configured {
		if strings.TrimSpace(job.Name) == "" || job.Handler == nil {
			return fmt.Errorf("Jobs: each job needs a name and handler")
		}
		if _, exists := seen[job.Name]; exists {
			return fmt.Errorf("Jobs: duplicate job name %q", job.Name)
		}
		timezone := job.Timezone
		if timezone == "" {
			timezone = "UTC"
		}
		if _, err := jobs.ParseScheduleInLocation(job.Schedule, timezone); err != nil {
			return fmt.Errorf("Jobs[%q]: %w", job.Name, err)
		}
		seen[job.Name] = struct{}{}
	}
	return nil
}

// JobSchedule documents the only schedule syntax accepted by ScheduledJob.
// It is kept public so callers can validate dynamically supplied intervals.
func JobSchedule(value time.Duration) string {
	return "@every " + value.String()
}
