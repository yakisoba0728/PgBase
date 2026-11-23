package mailer

import (
	"context"
	"sync"
	"time"
)

// Queue runs mail work off the request path. A request that may or may not
// lead to mail, such as a password reset for an address that may be unknown,
// hands the whole lookup-and-send to the queue, so its response time does not
// depend on the account or on the mail server. Work is bounded: at most
// workers jobs run at once and at most capacity are accepted but unfinished;
// beyond that Go refuses the job rather than blocking the caller. Jobs live
// only in memory and are lost if the process stops before they run.
type Queue struct {
	pending chan struct{}
	running chan struct{}
	timeout time.Duration

	mu     sync.Mutex
	closed bool
	active int
	// idle is closed while no accepted job is unfinished.
	idle chan struct{}
}

// NewQueue returns a queue that runs at most workers jobs at once, holds at
// most capacity unfinished jobs and gives each one timeout to complete.
func NewQueue(workers, capacity int, timeout time.Duration) *Queue {
	workers = max(workers, 1)
	capacity = max(capacity, workers)
	if timeout <= 0 {
		timeout = time.Minute
	}
	idle := make(chan struct{})
	close(idle)
	return &Queue{pending: make(chan struct{}, capacity), running: make(chan struct{}, workers), timeout: timeout, idle: idle}
}

// Go accepts job without waiting for it and reports whether it was accepted.
// The job's context keeps the values of ctx but not its cancellation, so it
// outlives the request that queued it, and it is cancelled after the queue's
// timeout. A panicking job is contained.
func (q *Queue) Go(ctx context.Context, job func(context.Context)) bool {
	if q == nil || job == nil {
		return false
	}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return false
	}
	select {
	case q.pending <- struct{}{}:
	default:
		q.mu.Unlock()
		return false
	}
	if q.active == 0 {
		q.idle = make(chan struct{})
	}
	q.active++
	q.mu.Unlock()
	detached := context.WithoutCancel(ctx)
	go func() {
		defer q.finish()
		defer func() { <-q.pending }()
		q.running <- struct{}{}
		defer func() { <-q.running }()
		jobCtx, cancel := context.WithTimeout(detached, q.timeout)
		defer cancel()
		defer func() { _ = recover() }()
		job(jobCtx)
	}()
	return true
}

func (q *Queue) finish() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.active--
	if q.active == 0 {
		close(q.idle)
	}
}

// Wait blocks until every accepted job has finished or ctx ends.
func (q *Queue) Wait(ctx context.Context) error {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	idle := q.idle
	q.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops accepting jobs and waits like Wait for the accepted ones.
func (q *Queue) Close(ctx context.Context) error {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	return q.Wait(ctx)
}
