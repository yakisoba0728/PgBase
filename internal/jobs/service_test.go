package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestParseScheduleAcceptsOnlyBoundedSecondIntervals(t *testing.T) {
	for _, test := range []struct {
		schedule string
		want     time.Duration
		valid    bool
	}{
		{"@every 1s", time.Second, true},
		{"@every 24h", 24 * time.Hour, true},
		{"@every 500ms", 0, false},
		{"@every 25h", 0, false},
		{"0 * * * *", 0, false},
	} {
		got, err := ParseSchedule(test.schedule)
		if test.valid && (err != nil || got != test.want) {
			t.Fatalf("ParseSchedule(%q) = %v, %v", test.schedule, got, err)
		}
		if !test.valid && !errors.Is(err, ErrInvalidSchedule) {
			t.Fatalf("ParseSchedule(%q) error = %v", test.schedule, err)
		}
	}
}

func TestInvokeContainsPanicAndPreservesHandlerError(t *testing.T) {
	job := Job{ID: uuid.New(), Name: "test"}
	want := errors.New("handler failed")
	if err := invoke(context.Background(), func(context.Context, Job) error { return want }, job); !errors.Is(err, want) {
		t.Fatalf("handler error = %v", err)
	}
	if err := invoke(context.Background(), func(context.Context, Job) error { panic("boom") }, job); err == nil {
		t.Fatal("panic was not contained")
	}
}

func TestRegistryRejectsDuplicateNames(t *testing.T) {
	registry := NewRegistry()
	handler := func(context.Context, Job) error { return nil }
	if err := registry.Register("cleanup", handler); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("cleanup", handler); !errors.Is(err, ErrDuplicateHandler) {
		t.Fatalf("duplicate registration error = %v", err)
	}
}
