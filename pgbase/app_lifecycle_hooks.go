package pgbase

import (
	"context"
	"sync"
)

// ApplicationEvent describes pgBase runtime lifecycle state. Mode is
// "embedded" for Start and "server" for Run. Address is populated for Run.
// Unlike PocketBase's ServeEvent, pgBase doesn't expose a mutable router here;
// routes are sealed by New before startup.
type ApplicationEvent struct {
	Mode    string
	Address string
}

func (a *App) beginApplicationStart(ctx context.Context, serving bool) (func(error), error) {
	if a.options.Hooks == nil {
		return func(error) {}, nil
	}
	event := &ApplicationEvent{Mode: "embedded"}
	if serving {
		event.Mode, event.Address = "server", a.options.HTTPAddress
	}
	bootFinish, err := a.options.Hooks.Begin(ctx, &HookEvent{Kind: HookAppBoot, Data: event})
	if err != nil {
		return bootFinish, err
	}
	if !serving {
		return bootFinish, nil
	}
	serveFinish, err := a.options.Hooks.Begin(ctx, &HookEvent{Kind: HookAppServe, Data: event})
	var once sync.Once
	finish := func(operationErr error) {
		once.Do(func() {
			serveFinish(operationErr)
			bootFinish(operationErr)
		})
	}
	if err != nil {
		finish(err)
	}
	return finish, err
}

func (a *App) beginApplicationTerminate(ctx context.Context) (func(error), error) {
	if a.options.Hooks == nil {
		return func(error) {}, nil
	}
	return a.options.Hooks.Begin(ctx, &HookEvent{
		Kind: HookAppTerminate,
		Data: &ApplicationEvent{Mode: "shutdown", Address: a.options.HTTPAddress},
	})
}
