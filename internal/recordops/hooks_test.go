package recordops

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type countingHook struct {
	before     int
	after      int
	afterError int
	fail       error
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

func (h *countingHook) AfterMutation(context.Context, Mutation)             { h.after++ }
func (h *countingHook) AfterMutationError(context.Context, Mutation, error) { h.afterError++ }

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
	if hook.before != 1 || hook.after != 0 || hook.afterError != 1 {
		t.Fatalf("rollback hooks before=%d after=%d afterError=%d", hook.before, hook.after, hook.afterError)
	}
	run.after(ctx, &failed)
	if hook.afterError != 1 {
		t.Fatalf("duplicate rollback afterError calls=%d", hook.afterError)
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
	run.after(ctx, &err)
	if hook.after != 0 || hook.afterError != 1 {
		t.Fatalf("failed before outcome calls after=%d afterError=%d", hook.after, hook.afterError)
	}
}

func TestExternalTransactionOutcomeDefersHooksUntilCommitOrRollback(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		outcomeErr error
		wantAfter  int
		wantError  int
	}{
		{name: "commit", wantAfter: 1},
		{name: "rollback", outcomeErr: ErrTransactionRolledBack, wantError: 1},
		{name: "commit_failure", outcomeErr: errors.New("commit failure"), wantError: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			hook := &countingHook{}
			service := &Service{hooks: []MutationHook{hook}}
			outcome := NewTransactionOutcome()
			ctx := context.WithValue(context.Background(), transactionOutcomeKey{}, outcome)
			ctx, run := service.hookContext(ctx)
			batch := artifactBatch{collection: schema.Collection{Name: "notes"}, operation: "create", rows: []artifactRow{{eventID: uuid.New(), recordID: uuid.New(), newVersion: 1}}}
			if err := beforeMutationHooks(ctx, Principal{ID: uuid.New()}, batch); err != nil {
				t.Fatal(err)
			}
			innerErr := error(nil)
			run.after(ctx, &innerErr)
			if hook.after != 0 || hook.afterError != 0 {
				t.Fatalf("hooks ran before transaction outcome: %+v", hook)
			}
			outcome.Finish(context.Background(), testCase.outcomeErr)
			outcome.Finish(context.Background(), testCase.outcomeErr)
			if hook.after != testCase.wantAfter || hook.afterError != testCase.wantError {
				t.Fatalf("outcome hooks after=%d error=%d", hook.after, hook.afterError)
			}
		})
	}
}
