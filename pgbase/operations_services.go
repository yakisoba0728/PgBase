package pgbase

import (
	"context"
	"errors"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/jobs"
	"github.com/webisnoteasy/PgBase/internal/observability"
)

func newRequestLog(pool *database.Pool, options Options) *observability.RequestLog {
	return observability.NewRequestLog(pool, observability.RequestLogOptions{
		Retention:     options.Operations.RequestLogRetention,
		MaximumRows:   options.Operations.RequestLogMaximumRows,
		QueueCapacity: options.Operations.RequestLogQueueCapacity,
	})
}

// RegisterJob adds a runtime handler and a durable schedule. Handlers must be
// registered again after process restart; persisted administrator edits survive.
func (a *App) RegisterJob(ctx context.Context, job ScheduledJob) error {
	service, ok := a.jobWorker.(*jobs.Service)
	if !ok {
		return errors.New("scheduled jobs are disabled")
	}
	return registerRuntimeJob(ctx, service, job)
}

// RemoveJob disables the durable schedule and unregisters its runtime handler.
func (a *App) RemoveJob(ctx context.Context, name string) error {
	service, ok := a.jobWorker.(*jobs.Service)
	if !ok {
		return errors.New("scheduled jobs are disabled")
	}
	return service.Unregister(ctx, name)
}
