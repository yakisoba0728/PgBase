// Package lifecycle coordinates ordered, concurrent-safe application hooks.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Event payloads are owned by the operation. Before callbacks may change only
// fields documented as mutable by the payload type. After callbacks observe the
// result; their return cannot undo a completed operation.
type Event struct {
	Kind string
	Data any
	Err  error
}

type Hook struct {
	Priority int
	Before   func(context.Context, *Event) error
	After    func(context.Context, *Event)
}

type entry struct {
	id   uint64
	hook Hook
}
type Registry struct {
	mu      sync.RWMutex
	next    uint64
	entries map[string][]entry
}

type withoutHooksKey struct{}

// WithoutHooks suppresses lifecycle callbacks only for operations derived from
// this context. Authorization, validation, constraints, and auditing remain in
// their owning services.
func WithoutHooks(ctx context.Context) context.Context {
	return context.WithValue(ctx, withoutHooksKey{}, true)
}

// On registers a hook and returns an idempotent removal function. An operation
// snapshots its hooks at entry, so removal affects only subsequent operations.
func (r *Registry) On(kind string, hook Hook) (func(), error) {
	if r == nil || kind == "" || (hook.Before == nil && hook.After == nil) {
		return nil, errors.New("hook kind and callback are required")
	}
	r.mu.Lock()
	if r.entries == nil {
		r.entries = make(map[string][]entry)
	}
	r.next++
	id := r.next
	r.entries[kind] = append(r.entries[kind], entry{id, hook})
	sort.SliceStable(r.entries[kind], func(i, j int) bool { return r.entries[kind][i].hook.Priority < r.entries[kind][j].hook.Priority })
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			list := r.entries[kind]
			for i, v := range list {
				if v.id == id {
					r.entries[kind] = append(list[:i], list[i+1:]...)
					break
				}
			}
		})
	}, nil
}

func (r *Registry) snapshot(kind string) []entry {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]entry(nil), r.entries[kind]...)
}

// Begin runs Before hooks in ascending priority and registration order. The
// returned finish function is paired with this exact snapshot, runs once, and
// observes failures as well as success. Call finish only after transaction commit
// or rollback; retries must use a fresh Begin and must not cause external effects.
func (r *Registry) Begin(ctx context.Context, event *Event) (finish func(error), err error) {
	if skipped, _ := ctx.Value(withoutHooksKey{}).(bool); skipped {
		return func(error) {}, ctx.Err()
	}
	list := r.snapshot(event.Kind)
	var once sync.Once
	finish = func(operationErr error) {
		once.Do(func() {
			event.Err = operationErr
			afterCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			for _, item := range list {
				if afterCtx.Err() != nil {
					break
				}
				callAfter(afterCtx, item.hook, event)
			}
		})
	}
	if err = ctx.Err(); err != nil {
		return finish, err
	}
	for _, item := range list {
		if err = ctx.Err(); err != nil {
			return finish, err
		}
		if err = callBefore(ctx, item.hook, event); err != nil {
			return finish, err
		}
	}
	return finish, nil
}

func (r *Registry) Run(ctx context.Context, event *Event, operation func() error) (err error) {
	finish, err := r.Begin(ctx, event)
	defer func() { finish(err) }()
	if err != nil {
		return err
	}
	return operation()
}

func callBefore(ctx context.Context, hook Hook, event *Event) (err error) {
	if hook.Before == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("%s before hook panicked", event.Kind)
		}
	}()
	return hook.Before(ctx, event)
}
func callAfter(ctx context.Context, hook Hook, event *Event) {
	if hook.After == nil {
		return
	}
	defer func() { _ = recover() }()
	hook.After(ctx, event)
}
