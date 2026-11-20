//go:build integration

package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestImmediateJobUsesPostgresStatementTime(t *testing.T) {
	ctx := t.Context()
	pool := jobsTestPool(t)
	// Compare inside the write statement, independent of app/DB clock skew.
	if _, err := pool.Exec(ctx, `CREATE FUNCTION pgbase_meta.check_job_due_time() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		IF NEW.next_run_at IS DISTINCT FROM statement_timestamp() THEN
			RAISE EXCEPTION 'immediate occurrence % did not use its database statement time', NEW.name;
		END IF;
		RETURN NEW;
	END
	$$;
	CREATE TRIGGER check_job_due_time BEFORE INSERT OR UPDATE OF schedule ON pgbase_meta.scheduled_jobs
	FOR EACH ROW EXECUTE FUNCTION pgbase_meta.check_job_due_time()`); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, NewRegistry(), nil)
	if err := service.Register(ctx, "registered", "@every 1h", "UTC", true, func(context.Context, Job) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := service.Schedule(ctx, "scheduled", "@every 1h"); err != nil {
		t.Fatal(err)
	}
	if err := service.Schedule(ctx, "scheduled", "@every 2h"); err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM pgbase_meta.scheduled_jobs WHERE name='registered'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureJob(ctx, id, "@every 2h", true); err != nil {
		t.Fatal(err)
	}
}

func TestNewJobScheduleRequiresExplicitTimezoneButLoadsLegacyDefaults(t *testing.T) {
	ctx := t.Context()
	pool := jobsTestPool(t)
	service := NewService(pool, NewRegistry(), nil)
	noop := func(context.Context, Job) error { return nil }
	const expression = "15 4 * * *"
	const prefixed = "CRON_TZ=America/New_York " + expression
	if err := service.Register(ctx, "ambiguous", expression, "Local", true, noop); !errors.Is(err, ErrInvalidSchedule) {
		t.Fatalf("register new Local schedule: %v", err)
	}
	if err := service.ScheduleInTimezone(ctx, "ambiguous", prefixed, "UTC"); !errors.Is(err, ErrInvalidSchedule) {
		t.Fatalf("write inline timezone: %v", err)
	}
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO pgbase_meta.scheduled_jobs(id,name,schedule,timezone,next_run_at)
		VALUES($1,'legacy',$2,'Local',clock_timestamp() + interval '1 hour') RETURNING id`, uuid.New(), expression).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := service.Register(ctx, "legacy", expression, "Local", true, noop); err != nil {
		t.Fatalf("register existing legacy job: %v", err)
	}
	if err := service.ConfigureJobInTimezone(ctx, id, prefixed, "UTC", true); !errors.Is(err, ErrInvalidSchedule) {
		t.Fatalf("configure inline timezone: %v", err)
	}
	items, err := service.ListJobs(ctx)
	if err != nil || len(items) != 1 || items[0].Timezone != "Local" || !items[0].Registered {
		t.Fatalf("jobs=%+v, err=%v", items, err)
	}
	if err := service.ConfigureJobInTimezone(ctx, id, expression, "America/New_York", true); err != nil {
		t.Fatalf("repair legacy job: %v", err)
	}
}

func TestCalendarJobSkipsMissedOccurrences(t *testing.T) {
	ctx := t.Context()
	pool := jobsTestPool(t)
	const expression, zone = "15 4 * * *", "Asia/Seoul"
	schedule, err := ParseScheduleInLocation(expression, zone)
	if err != nil {
		t.Fatal(err)
	}
	databaseTime := func() time.Time {
		t.Helper()
		var now time.Time
		if err := pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			t.Fatal(err)
		}
		return now
	}
	checkNextRun := func(before time.Time) {
		t.Helper()
		var next time.Time
		if err := pool.QueryRow(ctx, `SELECT next_run_at FROM pgbase_meta.scheduled_jobs WHERE name='calendar'`).Scan(&next); err != nil {
			t.Fatal(err)
		}
		earliest, latest := schedule.Next(before), schedule.Next(databaseTime())
		if next.Before(earliest) || next.After(latest) {
			t.Fatalf("next run %s outside expected calendar window [%s, %s]", next, earliest, latest)
		}
	}
	service := NewService(pool, NewRegistry(), nil)
	calls := 0
	before := databaseTime()
	if err := service.Register(ctx, "calendar", expression, zone, true, func(context.Context, Job) error {
		calls++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	checkNextRun(before)

	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs SET next_run_at=clock_timestamp()-interval '3 days' WHERE name='calendar'`); err != nil {
		t.Fatal(err)
	}
	before = databaseTime()
	if err := service.RunOnce(ctx); err != nil || calls != 1 {
		t.Fatalf("calendar run: calls=%d err=%v", calls, err)
	}
	checkNextRun(before)
}

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
