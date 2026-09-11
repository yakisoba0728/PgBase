package pgbase

import (
	"context"
	"errors"
	"strings"
)

const (
	HookModelValidate = "model.validate"
	HookModelExecute  = "model.execute"
	HookModelSuccess  = "model.success"
	HookModelError    = "model.error"
)

// ModelMutationEvent describes a custom Go model write. Model is mutable during
// validation and before execute; Result and Err are observations after execute.
type ModelMutationEvent struct {
	Operation string
	// Family optionally adds the corresponding record or collection phases.
	Family string
	Model  any
	Result any
	Err    error
}

// RunModelMutation applies the full lifecycle to a custom repository write.
// It is the concrete mutation seam: validate and execute hooks can reject the
// callback, while success/error hooks observe its final outcome.
func (a *App) RunModelMutation(ctx context.Context, event *ModelMutationEvent, execute func(context.Context, *ModelMutationEvent) error) (err error) {
	if event == nil || event.Model == nil {
		return errors.New("model mutation event and model are required")
	}
	switch event.Operation {
	case "create", "update", "delete":
	default:
		return errors.New("model operation must be create, update, or delete")
	}
	if execute == nil {
		return errors.New("model mutation callback is required")
	}
	if a == nil {
		return errors.New("application is unavailable")
	}
	registry := a.options.Hooks
	if registry != nil {
		if err = runMutationPhases(ctx, registry, event, "validate"); err != nil {
			event.Err = err
			a.observeMutation(ctx, event, "error")
			return err
		}
		if err = runMutationPhases(ctx, registry, event, event.Operation); err == nil {
			err = runMutationPhases(ctx, registry, event, event.Operation+".execute", func() error { return execute(ctx, event) })
		}
		if err != nil {
			event.Err = err
			a.observeMutation(ctx, event, "error")
			return err
		}
	} else {
		err = execute(ctx, event)
		if err != nil {
			event.Err = err
			return err
		}
	}
	a.observeMutation(ctx, event, "success")
	return nil
}

func runMutationPhases(ctx context.Context, registry *HookRegistry, event *ModelMutationEvent, phase string, operation ...func() error) error {
	kinds := []string{"model." + phase}
	if strings.HasSuffix(phase, ".execute") {
		kinds = append([]string{HookModelExecute}, kinds...)
	}
	if event.Family == "record" || event.Family == "collection" {
		kinds = append(kinds, event.Family+".model."+phase)
	}
	var run func(int) error
	run = func(i int) error {
		if i == len(kinds) {
			if len(operation) != 0 {
				return operation[0]()
			}
			return nil
		}
		return registry.Run(ctx, &HookEvent{Kind: kinds[i], Data: event}, func() error { return run(i + 1) })
	}
	return run(0)
}

func (a *App) observeMutation(ctx context.Context, event *ModelMutationEvent, phase string) {
	if a == nil || a.options.Hooks == nil {
		return
	}
	for _, kind := range []string{"model." + phase, "model." + event.Operation + "." + phase, event.Family + ".model." + event.Operation + "." + phase} {
		if strings.HasPrefix(kind, ".") {
			continue
		}
		_ = a.options.Hooks.Run(ctx, &HookEvent{Kind: kind, Data: event, Err: event.Err}, func() error { return nil })
	}
}
