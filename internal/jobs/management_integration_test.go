//go:build integration

package jobs

import (
	"context"
	"errors"
	"testing"
)

func TestImpossibleCalendarScheduleCannotBeSavedOrHotLoop(t *testing.T) {
	ctx := context.Background()
	pool := jobsTestPool(t)
	registry := NewRegistry()
	calls := 0
	if err := registry.Register("impossible", func(context.Context, Job) error { calls++; return nil }); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, registry, nil)
	const impossible = "0 0 31 2 *"
	if err := service.Schedule(ctx, "impossible", impossible); !errors.Is(err, ErrInvalidSchedule) {
		t.Fatalf("Schedule = %v, want invalid schedule", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pgbase_meta.scheduled_jobs`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid schedule persisted: count=%d err=%v", count, err)
	}
	// Older releases accepted this expression; existing rows must back off.
	insertJob(t, pool, "impossible", impossible)
	for range 2 {
		if err := service.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var attempts int
	var waiting bool
	if err := pool.QueryRow(ctx, `SELECT attempts,next_run_at>clock_timestamp() FROM pgbase_meta.scheduled_jobs WHERE name='impossible'`).Scan(&attempts, &waiting); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || attempts != 1 || !waiting {
		t.Fatalf("legacy row did not back off: calls=%d attempts=%d waiting=%t", calls, attempts, waiting)
	}
}

func TestAdministratorJobChangesPersistAndRespectLease(t *testing.T) {
	ctx := context.Background()
	pool := jobsTestPool(t)
	registry := NewRegistry()
	calls := 0
	if err := registry.Register("managed", func(context.Context, Job) error { calls++; return nil }); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, registry, nil)
	if err := service.EnsureScheduled(ctx, "managed", "@every 1h"); err != nil {
		t.Fatal(err)
	}
	items, err := service.ListJobs(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("list %v %v", items, err)
	}
	id := items[0].ID
	if err := service.ConfigureJob(ctx, id, "@every 2h", false); err != nil {
		t.Fatal(err)
	}
	if err := service.EnsureScheduled(ctx, "managed", "@every 1h"); err != nil {
		t.Fatal(err)
	}
	items, err = service.ListJobs(ctx)
	if err != nil || items[0].Enabled || items[0].Schedule != "@every 2h" {
		t.Fatalf("deployment overwrote settings: %v %v", items, err)
	}
	if err := service.RequestRun(ctx, id); !errors.Is(err, ErrNotManageable) {
		t.Fatalf("disabled run: %v", err)
	}
	if err := service.ConfigureJob(ctx, id, "@every 2h", true); err != nil {
		t.Fatal(err)
	}
	if err := service.RequestRun(ctx, id); err != nil {
		t.Fatal(err)
	}
	claims, err := service.claim(ctx)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim %v %v", claims, err)
	}
	if err := service.RequestRun(ctx, id); !errors.Is(err, ErrNotManageable) {
		t.Fatalf("running run: %v", err)
	}
	if err := service.ConfigureJob(ctx, id, "@every 3h", false); !errors.Is(err, ErrNotManageable) {
		t.Fatalf("running configure: %v", err)
	}
	if err := service.complete(ctx, claims[0], nil); err != nil {
		t.Fatal(err)
	}
	if err := service.RequestRun(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := service.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	if err := service.ConfigureJob(ctx, id, "* * * *", true); !errors.Is(err, ErrInvalidSchedule) {
		t.Fatalf("invalid schedule: %v", err)
	}
	unknown := NewService(pool, NewRegistry(), nil)
	if err := unknown.RequestRun(ctx, id); !errors.Is(err, ErrNotManageable) {
		t.Fatalf("unregistered run: %v", err)
	}
}
