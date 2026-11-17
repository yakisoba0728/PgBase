package lifecycle

import (
	"context"
	"strings"
	"testing"
)

func TestRunReportsOperationPanicToAfterWithoutSwallowingIt(t *testing.T) {
	var registry Registry
	var calls int
	_, err := registry.On("save", Hook{After: func(_ context.Context, event *Event) {
		calls++
		if event.Err == nil {
			t.Error("panicking operation was reported as successful")
		} else if strings.Contains(event.Err.Error(), "private failure") {
			t.Error("panic details escaped into hook outcome")
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	cause := "private failure"
	func() {
		defer func() {
			if got := recover(); got != cause {
				t.Errorf("panic = %v, want original cause", got)
			}
		}()
		_ = registry.Run(t.Context(), &Event{Kind: "save"}, func() error { panic(cause) })
		t.Error("operation panic was swallowed")
	}()
	if calls != 1 {
		t.Fatalf("After calls = %d, want exactly one", calls)
	}
}
