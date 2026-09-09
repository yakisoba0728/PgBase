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

func TestCalendarScheduleNextIsDeterministicAcrossDST(t *testing.T) {
	schedule, err := ParseScheduleInLocation("30 2 * * *", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// The spring-forward day has no 02:30, so the next occurrence is the
	// following civil day rather than an invented or duplicated instant.
	after := time.Date(2026, 3, 8, 6, 0, 0, 0, time.UTC)
	want := time.Date(2026, 3, 9, 6, 30, 0, 0, time.UTC)
	if got := schedule.Next(after); !got.Equal(want) {
		t.Fatalf("spring next = %s, want %s", got, want)
	}

	hourly, err := ParseScheduleInLocation("30 1 * * *", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	fall := time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC)
	first := hourly.Next(fall)
	if second := hourly.Next(first); !second.After(first) {
		t.Fatalf("fall next did not advance: %s then %s", first, second)
	}
}

func TestCalendarScheduleRejectsTimezoneAndExtendedCron(t *testing.T) {
	for _, test := range []struct{ expression, timezone string }{{"0 0 * * *", "Mars/Base"}, {"0 0 0 * * *", "UTC"}} {
		if _, err := ParseScheduleInLocation(test.expression, test.timezone); !errors.Is(err, ErrInvalidSchedule) {
			t.Fatalf("%q %q: %v", test.expression, test.timezone, err)
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
