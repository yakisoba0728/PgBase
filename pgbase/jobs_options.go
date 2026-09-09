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

// JobRun identifies one scheduled occurrence and one fenced execution attempt.
// OccurrenceKey is stable across retries of that occurrence and changes for the
// next schedule. RunKey changes for every claim attempt.
type JobRun struct {
	JobID           uuid.UUID
	Name            string
	FenceGeneration int64
	ScheduledAt     time.Time
}

func (r JobRun) RunKey() string {
	return fmt.Sprintf("%s:%d", r.JobID, r.FenceGeneration)
}

func (r JobRun) OccurrenceKey() string {
	return fmt.Sprintf("%s-%d", r.JobID, r.ScheduledAt.UTC().UnixMicro())
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
