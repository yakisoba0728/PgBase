package pgbase

import (
	"context"
	"errors"
	"net"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestApplicationStartHooksPairBootAndServe(t *testing.T) {
	registry := &HookRegistry{}
	var calls []string
	for _, kind := range []string{HookAppBoot, HookAppServe} {
		kind := kind
		if _, err := registry.On(kind, Hook{
			Before: func(context.Context, *HookEvent) error { calls = append(calls, "before:"+kind); return nil },
			After:  func(_ context.Context, event *HookEvent) { calls = append(calls, "after:"+kind+":"+event.Err.Error()) },
		}); err != nil {
			t.Fatal(err)
		}
	}
	app := &App{options: Options{Hooks: registry, HTTPAddress: ":8090"}}
	finish, err := app.beginApplicationStart(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	operationErr := errors.New("listen failed")
	finish(operationErr)
	want := []string{"before:" + HookAppBoot, "before:" + HookAppServe, "after:" + HookAppServe + ":listen failed", "after:" + HookAppBoot + ":listen failed"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func minimalLifecycleApp(registry *HookRegistry) *App {
	return &App{options: Options{Hooks: registry, HTTPAddress: ":0"}, httpServer: &http.Server{}, readiness: &readinessState{}, lifecycleClosing: make(chan struct{})}
}

func TestStartDuplicateAndConcurrentAttemptsEmitBootOnce(t *testing.T) {
	registry := &HookRegistry{}
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	if _, err := registry.On(HookAppBoot, Hook{Before: func(context.Context, *HookEvent) error {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	app := minimalLifecycleApp(registry)
	first := make(chan error, 1)
	go func() { first <- app.Start(context.Background()) }()
	<-entered
	if err := app.Start(context.Background()); !errors.Is(err, errAppAlreadyStarted) {
		t.Fatalf("concurrent Start=%v", err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := app.Start(context.Background()); !errors.Is(err, errAppAlreadyStarted) {
		t.Fatalf("duplicate Start=%v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("boot calls=%d", calls.Load())
	}
	_ = app.Close(context.Background())
}

func TestRunServeVetoPairsBootAndServeWithoutListening(t *testing.T) {
	registry := &HookRegistry{}
	veto := errors.New("serve veto")
	var calls []string
	_, _ = registry.On(HookAppBoot, Hook{Before: func(context.Context, *HookEvent) error { calls = append(calls, "boot.before"); return nil }, After: func(_ context.Context, e *HookEvent) { calls = append(calls, "boot.after:"+e.Err.Error()) }})
	_, _ = registry.On(HookAppServe, Hook{Before: func(context.Context, *HookEvent) error { calls = append(calls, "serve.before"); return veto }, After: func(_ context.Context, e *HookEvent) { calls = append(calls, "serve.after:"+e.Err.Error()) }})
	app := minimalLifecycleApp(registry)
	listened := false
	app.listen = func(string, string) (net.Listener, error) { listened = true; return nil, errors.New("unexpected") }
	if err := app.Run(context.Background()); !errors.Is(err, veto) {
		t.Fatalf("Run=%v", err)
	}
	want := []string{"boot.before", "serve.before", "serve.after:serve veto", "boot.after:serve veto"}
	if listened || !reflect.DeepEqual(calls, want) || app.lifecycleState != appLifecycleInactive {
		t.Fatalf("listened=%v calls=%v state=%v", listened, calls, app.lifecycleState)
	}
}

func TestTerminateVetoIsReportedButDoesNotPreventCleanup(t *testing.T) {
	registry := &HookRegistry{}
	veto := errors.New("terminate observer failed")
	var afterErr error
	if _, err := registry.On(HookAppTerminate, Hook{
		Before: func(context.Context, *HookEvent) error { return veto },
		After:  func(_ context.Context, event *HookEvent) { afterErr = event.Err },
	}); err != nil {
		t.Fatal(err)
	}
	app := &App{options: Options{Hooks: registry}, lifecycleClosing: make(chan struct{})}
	err := app.Close(context.Background())
	if !errors.Is(err, veto) || !errors.Is(afterErr, veto) {
		t.Fatalf("Close()=%v after=%v, want veto", err, afterErr)
	}
	if app.lifecycleState != appLifecycleClosed {
		t.Fatalf("lifecycle state=%v, want closed", app.lifecycleState)
	}
}
