package pgbase

import (
	"context"
	"time"
)

// OperationsOptions controls the durable HTTP request diagnostic window.
// Only method, registered route template, status, duration, and timestamp are
// retained; request headers, raw URLs, query strings, and bodies are excluded.
//
// Scheduled job run history is bounded the same way: JobRunRetention is the
// age limit (30 days when zero) and JobRunMaximumRows the number of newest runs
// kept per job (1,000 when zero).
type OperationsOptions struct {
	RequestLogRetention     time.Duration
	RequestLogMaximumRows   int
	RequestLogQueueCapacity int
	JobRunRetention         time.Duration
	JobRunMaximumRows       int
}

// JobRegistry is the public runtime surface for adding and removing handlers.
// Removing a handler first disables its durable definition. Definitions remain
// stored so administrator schedule edits survive process restarts.
type JobRegistry interface {
	RegisterJob(context.Context, ScheduledJob) error
	RemoveJob(context.Context, string) error
}
