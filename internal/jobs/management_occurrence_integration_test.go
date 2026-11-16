//go:build integration

package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A changed schedule replaces the pending occurrence. Reusing its old identity
// would make an idempotent handler discard the first run of the new schedule.
func TestJobRescheduleStartsNewOccurrenceAfterFailureOrExpiry(t *testing.T) {
	ctx := t.Context()
	pool := jobsTestPool(t)
	for _, operation := range []struct {
		name string
		run  func(*Service, uuid.UUID, string) error
	}{
		{"schedule", func(s *Service, _ uuid.UUID, name string) error {
			return s.Schedule(ctx, name, "@every 2h")
		}},
		{"schedule_timezone", func(s *Service, _ uuid.UUID, name string) error {
			return s.ScheduleInTimezone(ctx, name, "@every 1h", "Asia/Seoul")
		}},
		{"configure", func(s *Service, id uuid.UUID, _ string) error {
			return s.ConfigureJob(ctx, id, "@every 2h", true)
		}},
		{"configure_timezone", func(s *Service, id uuid.UUID, _ string) error {
			return s.ConfigureJobInTimezone(ctx, id, "@every 1h", "Asia/Seoul", true)
		}},
	} {
		for _, pending := range []string{"failed", "expired"} {
			t.Run(operation.name+"/"+pending, func(t *testing.T) {
				registry := NewRegistry()
				if err := registry.Register(t.Name(), func(context.Context, Job) error { return nil }); err != nil {
					t.Fatal(err)
				}
				insertJob(t, pool, t.Name(), "@every 1h")
				service := NewService(pool, registry, nil)
				original, err := service.claim(ctx)
				if err != nil || len(original) != 1 {
					t.Fatalf("initial claim = %v, %v", original, err)
				}
				old := original[0]
				if pending == "failed" {
					if err := service.complete(ctx, old, errors.New("effect completed but acknowledgement failed")); err != nil {
						t.Fatal(err)
					}
				} else if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, old.ID); err != nil {
					t.Fatal(err)
				}
				if err := operation.run(service, old.ID, old.Name); err != nil {
					t.Fatal(err)
				}
				var scheduled time.Time
				if err := pool.QueryRow(ctx, `SELECT next_run_at FROM pgbase_meta.scheduled_jobs WHERE id=$1`, old.ID).Scan(&scheduled); err != nil {
					t.Fatal(err)
				}
				replacement, err := service.claim(ctx)
				if err != nil || len(replacement) != 1 {
					t.Fatalf("replacement claim = %v, %v", replacement, err)
				}
				fresh := replacement[0]
				if fresh.ScheduledAt.Equal(old.ScheduledAt) || !fresh.ScheduledAt.Equal(scheduled) {
					t.Fatalf("rescheduled job reused old occurrence: old=%s new=%s want=%s", old.ScheduledAt, fresh.ScheduledAt, scheduled)
				}
				if fresh.FenceGeneration <= old.FenceGeneration {
					t.Fatalf("reschedule did not advance attempt fence: %d <= %d", fresh.FenceGeneration, old.FenceGeneration)
				}
				var attempts int
				var noPriorFailure bool
				if err := pool.QueryRow(ctx, `SELECT attempts,last_error IS NULL FROM pgbase_meta.scheduled_jobs WHERE id=$1`, fresh.ID).Scan(&attempts, &noPriorFailure); err != nil {
					t.Fatal(err)
				}
				if attempts != 1 || !noPriorFailure {
					t.Fatalf("new occurrence inherited retry state: attempts=%d noPriorFailure=%t", attempts, noPriorFailure)
				}
			})
		}
	}
}

// Re-saving the existing schedule and requesting an immediate retry must retain
// the pending occurrence, so already-completed effects are not applied twice.
func TestUnchangedJobSchedulePreservesPendingOccurrence(t *testing.T) {
	ctx := t.Context()
	pool := jobsTestPool(t)
	registry := NewRegistry()
	if err := registry.Register("unchanged", func(context.Context, Job) error { return nil }); err != nil {
		t.Fatal(err)
	}
	insertJob(t, pool, "unchanged", "@every 1h")
	service := NewService(pool, registry, nil)
	claims, err := service.claim(ctx)
	if err != nil || len(claims) != 1 {
		t.Fatalf("initial claim = %v, %v", claims, err)
	}
	old := claims[0]
	if err := service.complete(ctx, old, errors.New("retry this occurrence")); err != nil {
		t.Fatal(err)
	}
	if err := service.Schedule(ctx, old.Name, old.Schedule); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureJob(ctx, old.ID, old.Schedule, true); err != nil {
		t.Fatal(err)
	}
	if err := service.RequestRun(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	retry, err := service.claim(ctx)
	if err != nil || len(retry) != 1 || !retry[0].ScheduledAt.Equal(old.ScheduledAt) {
		t.Fatalf("unchanged schedule replaced pending occurrence: old=%s retry=%v err=%v", old.ScheduledAt, retry, err)
	}
}
