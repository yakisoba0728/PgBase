package pgbase

import (
	"context"
	"time"
)

// OperationsOptions controls the durable HTTP request diagnostic window.
// Only method, registered route template, status, duration, and timestamp are
// retained; request headers, raw URLs, query strings, and bodies are excluded.
type OperationsOptions struct {
	RequestLogRetention     time.Duration
	RequestLogMaximumRows   int
	RequestLogQueueCapacity int
}

// JobRegistry is the public runtime surface for adding and removing handlers.
// Removing a handler first disables its durable definition. Definitions remain
// stored so administrator schedule edits survive process restarts.
type JobRegistry interface {
	RegisterJob(context.Context, ScheduledJob) error
	RemoveJob(context.Context, string) error
}
