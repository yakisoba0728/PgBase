package recordops

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// MutationErrorHook observes a failed final transaction outcome. Implementing
// it is optional so existing MutationHook implementations remain compatible.
type MutationErrorHook interface {
	AfterMutationError(context.Context, Mutation, error)
}

type mutationHookKey struct{}
type transactionOutcomeKey struct{}
type mutationHookRun struct {
	hooks      []MutationHook
	events     []Mutation
	deferAfter int
	completed  bool
}

var ErrTransactionRolledBack = errors.New("record transaction rolled back")

// TransactionOutcome completes hooks for operations run in a caller-owned
// transaction. Finish must be called once with nil after commit or an error
// after rollback or commit failure.
type TransactionOutcome struct {
	mu        sync.Mutex
	finished  bool
	callbacks []func(context.Context, error)
}

func NewTransactionOutcome() *TransactionOutcome { return &TransactionOutcome{} }

// WithTransactionOutcome binds a service operation to a caller-owned
// transaction whose final commit or rollback will complete mutation hooks.
func WithTransactionOutcome(ctx context.Context, tx pgx.Tx, outcome *TransactionOutcome) context.Context {
	ctx = WithTransaction(ctx, tx)
	return context.WithValue(ctx, transactionOutcomeKey{}, outcome)
}

func (o *TransactionOutcome) register(callback func(context.Context, error)) bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.finished {
		return false
	}
	o.callbacks = append(o.callbacks, callback)
	return true
}

func (o *TransactionOutcome) Finish(ctx context.Context, outcomeErr error) {
	if o == nil {
		return
	}
	o.mu.Lock()
	if o.finished {
		o.mu.Unlock()
		return
	}
	o.finished = true
	callbacks := append([]func(context.Context, error){}, o.callbacks...)
	o.callbacks = nil
	o.mu.Unlock()
	for _, callback := range callbacks {
		callback(ctx, outcomeErr)
	}
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
	if outcome, _ := ctx.Value(transactionOutcomeKey{}).(*TransactionOutcome); outcome != nil {
		if outcome.register(func(finalCtx context.Context, outcomeErr error) { run.afterCommitted(finalCtx, &outcomeErr) }) {
			run.deferAfter++
		}
	}
	return context.WithValue(ctx, mutationHookKey{}, run), run
}

func beforeMutationHooks(ctx context.Context, principal Principal, batch artifactBatch) error {
	run, _ := ctx.Value(mutationHookKey{}).(*mutationHookRun)
	if run == nil {
		return nil
	}
	for _, row := range batch.rows {
		event := Mutation{EventID: row.eventID, Collection: batch.collection.Name, RecordID: row.recordID, Operation: batch.operation, Version: row.newVersion, ActorID: principal.ID}
		run.events = append(run.events, event)
		for _, hook := range run.hooks {
			if err := callBeforeHook(ctx, hook, event); err != nil {
				return err
			}
		}
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
	if run != nil && run.deferAfter > 0 {
		return
	}
	run.afterCommitted(ctx, operationErr)
}

func (run *mutationHookRun) afterCommitted(ctx context.Context, operationErr *error) {
	if run == nil || run.completed {
		return
	}
	run.completed = true
	// Observers have a short independent deadline: a client cancelling after
	// commit must not suppress observation. Durable delivery belongs in outbox.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for _, event := range run.events {
		for _, hook := range run.hooks {
			if *operationErr != nil {
				if errorHook, ok := hook.(MutationErrorHook); ok {
					callAfterErrorHook(ctx, errorHook, event, *operationErr)
				}
			} else {
				callAfterHook(ctx, hook, event)
			}
		}
	}
}

func callAfterErrorHook(ctx context.Context, hook MutationErrorHook, event Mutation, operationErr error) {
	defer func() { _ = recover() }()
	hook.AfterMutationError(ctx, event, operationErr)
}

func callAfterHook(ctx context.Context, hook MutationHook, event Mutation) {
	defer func() { _ = recover() }()
	hook.AfterMutation(ctx, event)
}
