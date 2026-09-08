package pgbase

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/jobs"
)

// newJobService converts public option handlers into the private durable
// runner. It registers every handler before scheduling anything, so a process
// never claims work it cannot execute.
func newJobService(ctx context.Context, pool *database.Pool, options Options) (*jobs.Service, error) {
	if !options.JobsEnabled {
		return nil, nil
	}
	registry := jobs.NewRegistry()
	for _, configured := range options.Jobs {
		job := configured
		if err := registry.Register(job.Name, func(ctx context.Context, run jobs.Job) error {
			return job.Handler(ctx, JobRun{
				JobID:           run.ID,
				Name:            run.Name,
				FenceGeneration: run.FenceGeneration,
			})
		}); err != nil {
			return nil, fmt.Errorf("register scheduled job %q: %w", job.Name, err)
		}
	}
	service := jobs.NewService(pool, registry, options.Logger, uuid.New())
	for _, configured := range options.Jobs {
		if err := service.EnsureScheduled(ctx, configured.Name, configured.Schedule); err != nil {
			return nil, fmt.Errorf("schedule job %q: %w", configured.Name, err)
		}
	}
	return service, nil
}
