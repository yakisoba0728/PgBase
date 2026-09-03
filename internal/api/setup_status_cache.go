package api

import (
	"context"
	"sync"
	"time"
)

const (
	publicProbeCacheTTL = 250 * time.Millisecond
	publicProbeTimeout  = time.Second
)

type setupStatusResult struct {
	required bool
	err      error
}

// setupStatusCache bounds repeated public status checks and coalesces an
// in-flight database probe. The probe runs with request values but without the
// initiating request's cancellation, so one disconnected client cannot poison
// the shared result observed by other callers.
type setupStatusCache struct {
	mu         sync.Mutex
	now        func() time.Time
	valid      bool
	result     setupStatusResult
	expiresAt  time.Time
	running    bool
	wait       chan struct{}
	generation uint64
}

func newSetupStatusCache() *setupStatusCache {
	return &setupStatusCache{now: time.Now}
}

func (c *setupStatusCache) get(
	ctx context.Context,
	probe func(context.Context) (bool, error),
) (bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		c.mu.Lock()
		if c.valid && c.now().Before(c.expiresAt) {
			result := c.result
			c.mu.Unlock()
			return result.required, result.err
		}
		if !c.running {
			c.running = true
			c.wait = make(chan struct{})
			generation := c.generation
			c.mu.Unlock()
			c.runProbe(ctx, generation, probe)
			continue
		}
		wait := c.wait
		c.mu.Unlock()

		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-wait:
		}
	}
}

func (c *setupStatusCache) runProbe(
	ctx context.Context,
	generation uint64,
	probe func(context.Context) (bool, error),
) {
	probeContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), publicProbeTimeout)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			c.abortProbe()
			panic(recovered)
		}
	}()
	required, err := probe(probeContext)
	c.finish(generation, setupStatusResult{required: required, err: err})
}

func (c *setupStatusCache) finish(generation uint64, result setupStatusResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation == c.generation {
		c.valid = true
		c.result = result
		c.expiresAt = c.now().Add(publicProbeCacheTTL)
	}
	c.running = false
	close(c.wait)
	c.wait = nil
}

func (c *setupStatusCache) abortProbe() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.valid = false
	c.running = false
	close(c.wait)
	c.wait = nil
}

// invalidate prevents a successful setup transition from being hidden behind
// a cached first-run response. The generation also discards an older probe
// that was already in flight when setup committed.
func (c *setupStatusCache) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.generation++
	c.valid = false
	c.mu.Unlock()
}
