//go:build integration

package jobs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

func TestRunnerClaimsOnceFencesStaleWorkersAndRecoversFailures(t *testing.T) {
	ctx := context.Background()
	pool := jobsTestPool(t)
	insertJob(t, pool, "once", "@every 1h")

	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	registry := NewRegistry()
	if err := registry.Register("once", func(context.Context, Job) error {
		calls.Add(1)
		close(started)
		<-release
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	first := NewService(pool, registry, nil, uuid.New())
	second := NewService(pool, registry, nil, uuid.New())
	done := make(chan error, 1)
	go func() { done <- first.RunOnce(ctx) }()
	<-started
	if err := second.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent claims invoked %d handlers, want 1", calls.Load())
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	insertJob(t, pool, "fenced", "@every 1h")
	if err := registry.Register("fenced", func(context.Context, Job) error { return nil }); err != nil {
		t.Fatal(err)
	}
	stale, err := first.claim(ctx)
	if err != nil || len(stale) != 1 {
		t.Fatalf("first claim = %#v, %v", stale, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, stale[0].ID); err != nil {
		t.Fatal(err)
	}
	fresh, err := second.claim(ctx)
	if err != nil || len(fresh) != 1 {
		t.Fatalf("reclaim = %#v, %v", fresh, err)
	}
	if stale[0].ScheduledAt.IsZero() || !fresh[0].ScheduledAt.Equal(stale[0].ScheduledAt) {
		t.Fatalf("retry scheduledAt changed: first=%s retry=%s", stale[0].ScheduledAt, fresh[0].ScheduledAt)
	}
	if err := first.complete(ctx, stale[0], errors.New("stale failure")); err != nil {
		t.Fatal(err)
	}
	var token uuid.UUID
	var fence int64
	if err := pool.QueryRow(ctx, `SELECT lease_token,fence_generation FROM pgbase_meta.scheduled_jobs WHERE id=$1`, fresh[0].ID).Scan(&token, &fence); err != nil {
		t.Fatal(err)
	}
	if token != fresh[0].token || fence != fresh[0].FenceGeneration {
		t.Fatalf("stale completion replaced current lease token=%s fence=%d", token, fence)
	}

	insertJob(t, pool, "panic", "@every 1h")
	if err := registry.Register("panic", func(context.Context, Job) error { panic("contained") }); err != nil {
		t.Fatal(err)
	}
	if err := first.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var attempts int
	var failure string
	if err := pool.QueryRow(ctx, `SELECT attempts,last_error FROM pgbase_meta.scheduled_jobs WHERE name='panic'`).Scan(&attempts, &failure); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || failure == "" {
		t.Fatalf("panic retry state attempts=%d failure=%q", attempts, failure)
	}
}

func TestRunnerTimeoutAndShutdown(t *testing.T) {
	ctx := context.Background()
	pool := jobsTestPool(t)
	insertJob(t, pool, "timeout", "@every 1h")
	registry := NewRegistry()
	canceled := make(chan struct{})
	if err := registry.Register("timeout", func(ctx context.Context, _ Job) error {
		<-ctx.Done()
		close(canceled)
		return ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	runner := NewService(pool, registry, nil, uuid.New())
	runner.timeout = 20 * time.Millisecond // execution stays below the fixed one-minute lease.
	if err := runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("handler did not receive its execution timeout")
	}

	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runner.Run(stopped); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run shutdown error = %v", err)
	}
}

func jobsTestPool(t *testing.T) *database.Pool {
	t.Helper()
	databaseURL := testutil.NewDatabase(t)
	migrations := system.Migrations()
	if len(migrations) < 15 || migrations[14].Name != "scheduled_jobs" {
		t.Fatal("scheduled jobs migration 15 must be embedded")
	}
	testutil.ApplySystemMigrationPrefix(t, databaseURL, migrations)
	pool, err := database.Open(context.Background(), databaseURL, 5*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func insertJob(t *testing.T, pool *database.Pool, name, schedule string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `INSERT INTO pgbase_meta.scheduled_jobs(id,name,schedule,next_run_at)
 VALUES($1,$2,$3,clock_timestamp()-interval '1 second')`, uuid.New(), name, schedule); err != nil {
		t.Fatal(err)
	}
}
