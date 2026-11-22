package filestore

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"
)

// DefaultImageDecodeMemoryBytes bounds the estimated memory that every image
// decode and render in the process may hold at once. The pixel limit alone
// does not bound the sum: concurrent uploads of maximum-size images would
// each materialize hundreds of MiB.
const DefaultImageDecodeMemoryBytes int64 = 256 << 20

// lockedImageBudgetWait bounds how long a render waits for decode memory when
// its caller may hold database locks or pooled connections (the on-demand
// variant path renders under a record_files row lock and an advisory lock).
// Holders never block on anything but CPU, so a short bound cannot starve
// them; it only keeps waiters from pinning locks behind a long queue.
var lockedImageBudgetWait = 5 * time.Second

// ErrImageBusy reports that decode memory stayed exhausted for the bounded
// wait of a lock-holding render. Retrying later can succeed.
var ErrImageBusy = errors.New("image processing capacity exhausted")

var defaultImageMemoryBudget = newImageMemoryBudget(DefaultImageDecodeMemoryBytes)

// imageMemoryBudget is a FIFO weighted semaphore over estimated bytes. FIFO
// order keeps a large image from starving behind a stream of small ones.
type imageMemoryBudget struct {
	capacity int64

	mu      sync.Mutex
	held    int64
	peak    int64 // Highest held value observed; read by tests.
	waiters list.List
}

type imageBudgetWaiter struct {
	weight int64
	ready  chan struct{}
}

func newImageMemoryBudget(capacity int64) *imageMemoryBudget {
	return &imageMemoryBudget{capacity: capacity}
}

// acquire reserves weight bytes, waiting in FIFO order until ctx is done. A
// weight above the whole capacity reserves the whole capacity instead: that
// image runs alone, so no estimate is ever added to another holder's, and the
// pixel limit still bounds its own memory. Refusing it would turn ordinary
// large photos into permanent upload failures.
func (b *imageMemoryBudget) acquire(ctx context.Context, weight int64) (func(), error) {
	weight = min(max(weight, 1), b.capacity)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	if b.waiters.Len() == 0 && b.capacity-b.held >= weight {
		b.take(weight)
		b.mu.Unlock()
		return b.releaser(weight), nil
	}
	waiter := &imageBudgetWaiter{weight: weight, ready: make(chan struct{})}
	element := b.waiters.PushBack(waiter)
	b.mu.Unlock()

	select {
	case <-waiter.ready:
		return b.releaser(weight), nil
	case <-ctx.Done():
		b.mu.Lock()
		select {
		case <-waiter.ready:
			// Granted while cancellation raced in: hand the bytes back.
			b.held -= weight
			b.grantWaiters()
		default:
			front := b.waiters.Front() == element
			b.waiters.Remove(element)
			if front {
				// The next waiter may fit now that the head left.
				b.grantWaiters()
			}
		}
		b.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (b *imageMemoryBudget) take(weight int64) {
	b.held += weight
	if b.held > b.peak {
		b.peak = b.held
	}
}

func (b *imageMemoryBudget) releaser(weight int64) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.held -= weight
			b.grantWaiters()
			b.mu.Unlock()
		})
	}
}

// grantWaiters admits queued waiters in order while the head fits. Callers
// hold b.mu.
func (b *imageMemoryBudget) grantWaiters() {
	for {
		front := b.waiters.Front()
		if front == nil {
			return
		}
		waiter := front.Value.(*imageBudgetWaiter)
		if b.capacity-b.held < waiter.weight {
			return
		}
		b.take(waiter.weight)
		b.waiters.Remove(front)
		close(waiter.ready)
	}
}

func (s *Service) imageMemoryBudget() *imageMemoryBudget {
	if s.imageBudget != nil {
		return s.imageBudget
	}
	return defaultImageMemoryBudget
}

// acquireImageMemory reserves decode memory. bounded callers may hold
// database locks, so their wait is capped by lockedImageBudgetWait and a
// timeout that is not the caller's own reports ErrImageBusy.
func (s *Service) acquireImageMemory(ctx context.Context, weight int64, bounded bool) (func(), error) {
	budget := s.imageMemoryBudget()
	if !bounded {
		return budget.acquire(ctx, weight)
	}
	waitCtx, cancel := context.WithTimeout(ctx, lockedImageBudgetWait)
	defer cancel()
	release, err := budget.acquire(waitCtx, weight)
	if err != nil && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return nil, ErrImageBusy
	}
	return release, err
}
