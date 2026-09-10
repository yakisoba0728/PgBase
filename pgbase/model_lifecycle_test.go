package pgbase

import (
	"context"
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
