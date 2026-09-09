//go:build integration

package schema

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type testCollectionLifecycle struct {
	beforeErr error
	events    []*CollectionMutationEvent
	after     []error
}

type selectiveCollectionLifecycle struct {
	testCollectionLifecycle
	vetoName string
}

func (h *selectiveCollectionLifecycle) BeginCollectionMutation(ctx context.Context, event *CollectionMutationEvent) (func(error), error) {
	finish, _ := h.testCollectionLifecycle.BeginCollectionMutation(ctx, event)
	if event.Name == h.vetoName {
		return finish, errors.New("import veto")
	}
	return finish, nil
}

func TestSchemaImportCollectionHooksFanOutAndRollbackTogether(t *testing.T) {
	pool, service, actor := newSchemaServiceTest(t)
	rules := json.RawMessage(`{"list":{"op":"owner"},"view":{"op":"owner"},"create":{"op":"owner"},"update":{"op":"owner"},"delete":{"op":"owner"}}`)
	document := SchemaDocument{Format: SchemaDocumentFormat, Collections: []SchemaDocumentCollection{{Name: "hook_import_a", Rules: rules}, {Name: "hook_import_b", Rules: rules}}}
	plan, err := service.PreviewSchemaImport(context.Background(), document)
	if err != nil {
		t.Fatal(err)
	}
	hooks := &selectiveCollectionLifecycle{vetoName: "hook_import_b"}
	service.SetCollectionMutationLifecycle(hooks)
	intent := SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "hook-import-veto-0001"}
	if _, err := service.ApplySchemaImport(context.Background(), intent); err == nil {
		t.Fatal("vetoed import succeeded")
	}
	var count int
	if err := pool.QueryRow(context.Background(), `select count(*) from pgbase_meta.collections where name like 'hook_import_%'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled back collection count=%d err=%v", count, err)
	}
	if len(hooks.events) != 2 || len(hooks.after) != 2 || hooks.after[0] == nil || hooks.after[1] == nil {
		t.Fatalf("veto hooks events=%d after=%v", len(hooks.events), hooks.after)
	}

	hooks = &selectiveCollectionLifecycle{}
	service.SetCollectionMutationLifecycle(hooks)
	plan, err = service.PreviewSchemaImport(context.Background(), document)
	if err != nil {
		t.Fatal(err)
	}
	intent = SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: actor, OperationID: uuid.New(), IdempotencyKey: "hook-import-commit-0001"}
	if _, err := service.ApplySchemaImport(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	if len(hooks.events) != 2 || hooks.after[0] != nil || hooks.after[1] != nil {
		t.Fatalf("commit hooks events=%d after=%v", len(hooks.events), hooks.after)
	}
	if _, err := service.ApplySchemaImport(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	if len(hooks.events) != 2 {
		t.Fatalf("replay duplicated hooks=%d", len(hooks.events))
	}
}

func (h *testCollectionLifecycle) BeginCollectionMutation(_ context.Context, event *CollectionMutationEvent) (func(error), error) {
	h.events = append(h.events, event)
	return func(err error) { h.after = append(h.after, err) }, h.beforeErr
}

func TestCollectionLifecycleVetoRollsBackAndCommittedReplayDoesNotRepeatHooks(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	ctx := context.Background()
	veto := errors.New("extension veto")
	hooks := &testCollectionLifecycle{beforeErr: veto}
	service.SetCollectionMutationLifecycle(hooks)
	change := NewCreateChange(CreateCollectionInput{Name: "hook_veto"})
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := GuardedApply(change, plan, administratorID, uuid.New(), "schema-hook-veto-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(ctx, intent); !errors.Is(err, veto) {
		t.Fatalf("Apply() error = %v, want veto", err)
	}
	var table string
	if err := pool.QueryRow(ctx, `select coalesce(to_regclass('app.hook_veto')::text, '')`).Scan(&table); err != nil {
		t.Fatal(err)
	}
	if table != "" || len(hooks.events) != 1 || len(hooks.after) != 1 || hooks.after[0] == nil {
		t.Fatalf("veto state table=%q events=%d after=%v", table, len(hooks.events), hooks.after)
	}

	hooks = &testCollectionLifecycle{}
	service.SetCollectionMutationLifecycle(hooks)
	change = NewCreateChange(CreateCollectionInput{Name: "hook_committed"})
	plan, err = service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	intent, err = GuardedApply(change, plan, administratorID, uuid.New(), "schema-hook-commit-key")
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Apply(ctx, intent)
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks.events) != 1 || len(hooks.after) != 1 || hooks.after[0] != nil || hooks.events[0].Result.Collection.ID != result.Collection.ID {
		t.Fatalf("committed hooks events=%#v after=%v result=%#v", hooks.events, hooks.after, result)
	}
	if replay, err := service.Apply(ctx, intent); err != nil || !replay.Replayed {
		t.Fatalf("replay = %#v, %v", replay, err)
	}
	if len(hooks.events) != 1 {
		t.Fatalf("replay emitted hook; count=%d", len(hooks.events))
	}
}
