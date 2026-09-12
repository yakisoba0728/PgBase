package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestSnapshotRemovalOrderingAndFailure(t *testing.T) {
	var registry Registry
	var order []int
	failure := errors.New("veto")
	remove, _ := registry.On("save", Hook{Priority: 10, Before: func(context.Context, *Event) error { order = append(order, 2); return failure }, After: func(_ context.Context, e *Event) {
		if !errors.Is(e.Err, failure) {
			t.Error(e.Err)
		}
		order = append(order, 4)
	}})
	_, _ = registry.On("save", Hook{Before: func(context.Context, *Event) error { order = append(order, 1); remove(); return nil }, After: func(context.Context, *Event) { order = append(order, 3) }})
	called := false
	if err := registry.Run(context.Background(), &Event{Kind: "save"}, func() error { called = true; return nil }); !errors.Is(err, failure) || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
	if !reflect.DeepEqual(order, []int{1, 2, 3, 4}) {
		t.Fatal(order)
	}
	if err := registry.Run(context.Background(), &Event{Kind: "save"}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentRegistrationAndCancellation(t *testing.T) {
	var registry Registry
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			for range 100 {
				remove, err := registry.On("read", Hook{Before: func(context.Context, *Event) error { return nil }})
				if err != nil {
					t.Error(err)
				}
				_ = registry.Run(context.Background(), &Event{Kind: "read"}, func() error { return nil })
				remove()
				remove()
			}
		})
	}
	wg.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = registry.On("read", Hook{Before: func(context.Context, *Event) error { t.Error("called after cancellation"); return nil }})
	if err := registry.Run(ctx, &Event{Kind: "read"}, func() error { t.Error("operation after cancellation"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestPanicContainmentAndFinishOnce(t *testing.T) {
	var registry Registry
	n := 0
	_, _ = registry.On("test", Hook{Before: func(context.Context, *Event) error { panic("private") }, After: func(context.Context, *Event) { n++; panic("private") }})
	finish, err := registry.Begin(context.Background(), &Event{Kind: "test"})
	if err == nil {
		t.Fatal("missing panic error")
	}
	finish(err)
	finish(err)
	if n != 1 {
		t.Fatal(n)
	}
}

func TestWithoutHooksSkipsCallbacksButRunsOperation(t *testing.T) {
	var registry Registry
	callbacks := 0
	_, _ = registry.On("save", Hook{
		Before: func(context.Context, *Event) error { callbacks++; return nil },
		After:  func(context.Context, *Event) { callbacks++ },
	})
	operations := 0
	if err := registry.Run(WithoutHooks(context.Background()), &Event{Kind: "save"}, func() error {
		operations++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if callbacks != 0 || operations != 1 {
		t.Fatalf("callbacks=%d operations=%d", callbacks, operations)
	}
	if err := registry.Run(context.Background(), &Event{Kind: "save"}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if callbacks != 2 {
		t.Fatalf("normal callbacks=%d", callbacks)
	}
}
