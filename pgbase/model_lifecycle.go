package pgbase

import (
	"context"
	"errors"
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
	Model     any
	Result    any
	Err       error
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
		if err = registry.Run(ctx, &HookEvent{Kind: HookModelValidate, Data: event}, func() error { return nil }); err != nil {
			event.Err = err
			a.observeModel(ctx, HookModelError, event)
			return err
		}
		if err = registry.Run(ctx, &HookEvent{Kind: HookModelExecute, Data: event}, func() error { return execute(ctx, event) }); err != nil {
			event.Err = err
			a.observeModel(ctx, HookModelError, event)
			return err
		}
	} else {
		err = execute(ctx, event)
		if err != nil {
			event.Err = err
			return err
		}
	}
	a.observeModel(ctx, HookModelSuccess, event)
	return nil
}

func (a *App) observeModel(ctx context.Context, kind string, event *ModelMutationEvent) {
	if a != nil && a.options.Hooks != nil {
		_ = a.options.Hooks.Run(ctx, &HookEvent{Kind: kind, Data: event}, func() error { return nil })
	}
}
