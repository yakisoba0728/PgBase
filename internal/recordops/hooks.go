package recordops

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Mutation is immutable lifecycle metadata. EventID is stable across retries.
type Mutation struct {
	EventID    uuid.UUID
	Collection string
	RecordID   uuid.UUID
	Operation  string
	Version    int64
	ActorID    uuid.UUID
}

type MutationHook interface {
	BeforeMutation(context.Context, Mutation) error
	AfterMutation(context.Context, Mutation)
}

type mutationHookKey struct{}
type mutationHookRun struct {
	hooks      []MutationHook
	events     []Mutation
	deferAfter bool
}

// SetHooks is a startup-only operation; a running service never changes hooks.
func (s *Service) SetHooks(hooks []MutationHook) { s.hooks = append([]MutationHook(nil), hooks...) }

func (s *Service) hookContext(ctx context.Context) (context.Context, *mutationHookRun) {
	if run, ok := ctx.Value(mutationHookKey{}).(*mutationHookRun); ok {
		return ctx, run
	}
	if len(s.hooks) == 0 {
		return ctx, nil
	}
	run := &mutationHookRun{hooks: s.hooks}
	return context.WithValue(ctx, mutationHookKey{}, run), run
}

func beforeMutationHooks(ctx context.Context, principal Principal, batch artifactBatch) error {
	run, _ := ctx.Value(mutationHookKey{}).(*mutationHookRun)
	if run == nil {
		return nil
	}
	for _, row := range batch.rows {
		event := Mutation{EventID: row.eventID, Collection: batch.collection.Name, RecordID: row.recordID, Operation: batch.operation, Version: row.newVersion, ActorID: principal.ID}
		for _, hook := range run.hooks {
			if err := callBeforeHook(ctx, hook, event); err != nil {
				return err
			}
		}
		run.events = append(run.events, event)
	}
	return nil
}

func callBeforeHook(ctx context.Context, hook MutationHook, event Mutation) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("before mutation hook panicked")
		}
	}()
	return hook.BeforeMutation(ctx, event)
}

func (run *mutationHookRun) after(ctx context.Context, operationErr *error) {
	if run != nil && run.deferAfter {
		return
	}
	run.afterCommitted(ctx, operationErr)
}

func (run *mutationHookRun) afterCommitted(ctx context.Context, operationErr *error) {
	if run == nil || *operationErr != nil {
		return
	}
	// Observers have a short independent deadline: a client cancelling after
	// commit must not suppress observation. Durable delivery belongs in outbox.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for _, event := range run.events {
		for _, hook := range run.hooks {
			callAfterHook(ctx, hook, event)
		}
	}
}

func callAfterHook(ctx context.Context, hook MutationHook, event Mutation) {
	defer func() { _ = recover() }()
	hook.AfterMutation(ctx, event)
}
