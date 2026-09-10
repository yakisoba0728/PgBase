package pgbase

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestRunModelMutationUsesAllLifecyclePhases(t *testing.T) {
	registry := &HookRegistry{}
	order := []string{}
	for _, kind := range []string{HookModelValidate, HookModelExecute, HookModelSuccess, HookModelError} {
		captured := kind
		_, err := registry.On(kind, Hook{Before: func(_ context.Context, raw *HookEvent) error { order = append(order, captured); return nil }})
		if err != nil {
			t.Fatal(err)
		}
	}
	app := &App{options: Options{Hooks: registry}}
	event := &ModelMutationEvent{Operation: "create", Model: &struct{ Name string }{"before"}}
	err := app.RunModelMutation(context.Background(), event, func(_ context.Context, event *ModelMutationEvent) error { event.Result = "created"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{HookModelValidate, HookModelExecute, HookModelSuccess}) || event.Result != "created" {
		t.Fatalf("order=%v event=%+v", order, event)
	}
}

func TestRunModelMutationPublishesPocketBaseOperationPhasesAndRollbackError(t *testing.T) {
	registry := &HookRegistry{}
	kinds := []string{"model.validate", "record.model.validate", "model.create", "record.model.create", "model.execute", "model.create.execute", "record.model.create.execute", "model.create.success", "record.model.create.success"}
	var seen []string
	for _, kind := range kinds {
		k := kind
		_, _ = registry.On(k, Hook{Before: func(context.Context, *HookEvent) error { seen = append(seen, k); return nil }})
	}
	app := &App{options: Options{Hooks: registry}}
	event := &ModelMutationEvent{Operation: "create", Family: "record", Model: map[string]any{"collection": "notes"}}
	if err := app.RunModelMutation(context.Background(), event, func(context.Context, *ModelMutationEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(seen, kinds) {
		t.Fatalf("phases=%v", seen)
	}
	veto := errors.New("veto")
	_, _ = registry.On("record.model.update.execute", Hook{Before: func(context.Context, *HookEvent) error { return veto }})
	executed := false
	err := app.RunModelMutation(context.Background(), &ModelMutationEvent{Operation: "update", Family: "record", Model: event.Model}, func(context.Context, *ModelMutationEvent) error { executed = true; return nil })
	if !errors.Is(err, veto) || executed {
		t.Fatalf("err=%v executed=%v", err, executed)
	}
}
