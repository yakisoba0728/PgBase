package recordops

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

type countingHook struct {
	before int
	after  int
	fail   error
}

type contextHook struct {
	canceled   bool
	panicAfter bool
}

func (*contextHook) BeforeMutation(context.Context, Mutation) error { return nil }
func (h *contextHook) AfterMutation(ctx context.Context, _ Mutation) {
	h.canceled = ctx.Err() != nil
	if h.panicAfter {
		panic("observer")
	}
}

func (h *countingHook) BeforeMutation(context.Context, Mutation) error {
	h.before++
	return h.fail
}

func TestAfterHookSurvivesCallerCancellationAndPanic(t *testing.T) {
	observer := &contextHook{panicAfter: true}
	run := &mutationHookRun{hooks: []MutationHook{observer}, events: []Mutation{{EventID: uuid.New()}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	committed := error(nil)
	run.afterCommitted(ctx, &committed)
	if observer.canceled {
		t.Fatal("committed observer inherited caller cancellation")
	}
}

func (h *countingHook) AfterMutation(context.Context, Mutation) { h.after++ }

func TestHooksAfterOnlyRunsForCommittedEvents(t *testing.T) {
	hook := &countingHook{}
	run := &mutationHookRun{hooks: []MutationHook{hook}}
	ctx := context.WithValue(context.Background(), mutationHookKey{}, run)
	batch := artifactBatch{
		collection: schema.Collection{Name: "notes"},
		operation:  "create",
		rows:       []artifactRow{{eventID: uuid.New(), recordID: uuid.New(), newVersion: 1}},
	}
	if err := beforeMutationHooks(ctx, Principal{ID: uuid.New()}, batch); err != nil {
		t.Fatal(err)
	}
	failed := errors.New("rollback")
	run.after(ctx, &failed)
	if hook.before != 1 || hook.after != 0 {
		t.Fatalf("rollback hooks before=%d after=%d", hook.before, hook.after)
	}
	committed := error(nil)
	run.after(ctx, &committed)
	if hook.after != 1 {
		t.Fatalf("committed after calls=%d", hook.after)
	}
}

func TestBeforeHookFailureDoesNotQueueAfter(t *testing.T) {
	hook := &countingHook{fail: errors.New("reject")}
	run := &mutationHookRun{hooks: []MutationHook{hook}}
	ctx := context.WithValue(context.Background(), mutationHookKey{}, run)
	err := beforeMutationHooks(ctx, Principal{ID: uuid.New()}, artifactBatch{
		collection: schema.Collection{Name: "notes"}, operation: "update",
		rows: []artifactRow{{eventID: uuid.New(), recordID: uuid.New(), newVersion: 2}},
	})
	if err == nil {
		t.Fatal("before hook failure was accepted")
	}
	committed := error(nil)
	run.after(ctx, &committed)
	if hook.after != 0 {
		t.Fatalf("failed before queued after calls=%d", hook.after)
	}
}
