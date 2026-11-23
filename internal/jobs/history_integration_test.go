//go:build integration

package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRunHistoryRecordsOutcomesNewestFirstWithBoundedPages(t *testing.T) {
	ctx := context.Background()
	pool := jobsTestPool(t)
	insertJob(t, pool, "history", "@every 1h")
	insertJob(t, pool, "other", "@every 1h")
	registry := NewRegistry()
	fail := true
	if err := registry.Register("history", func(context.Context, Job) error {
		if fail {
			return errors.New(strings.Repeat("é", MaximumRunErrorChars+10))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithOptions(pool, registry, nil, Options{CursorSecret: []byte(strings.Repeat("k", 32))})
	id := jobID(t, service, "history")
	otherID := jobID(t, service, "other")

	if err := service.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	fail = false
	makeDue(t, service, id)
	if err := service.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}

	page, err := service.ListRuns(ctx, id, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.NextCursor != "" {
		t.Fatalf("page = %+v", page)
	}
	newest, oldest := page.Items[0], page.Items[1]
	if newest.Status != RunSucceeded || newest.Attempt != 2 || newest.Error != "" || newest.FinishedAt == nil || newest.DurationMS == nil || newest.ScheduledAt == nil {
		t.Fatalf("newest run = %+v", newest)
	}
	if oldest.Status != RunFailed || oldest.Attempt != 1 || len([]rune(oldest.Error)) != MaximumRunErrorChars || oldest.ID >= newest.ID {
		t.Fatalf("oldest run = %+v (error %d runes)", oldest, len([]rune(oldest.Error)))
	}
	if page.Retention.MaximumRuns != defaultRunMaximumRows || page.Retention.MaximumAgeSeconds != int64(defaultRunRetention/time.Second) {
		t.Fatalf("retention = %+v", page.Retention)
	}

	first, err := service.ListRuns(ctx, id, 1, "")
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != newest.ID || first.NextCursor == "" {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	second, err := service.ListRuns(ctx, id, 1, first.NextCursor)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != oldest.ID || second.NextCursor != "" {
		t.Fatalf("second page = %+v, %v", second, err)
	}
	if _, err := service.ListRuns(ctx, otherID, 1, first.NextCursor); !errors.Is(err, ErrInvalidRunQuery) {
		t.Fatalf("cursor replayed for another job = %v", err)
	}
	foreign := NewServiceWithOptions(pool, registry, nil, Options{CursorSecret: []byte(strings.Repeat("x", 32))})
	if _, err := foreign.ListRuns(ctx, id, 1, first.NextCursor); !errors.Is(err, ErrInvalidRunQuery) {
		t.Fatalf("cursor under another secret = %v", err)
	}
	for _, limit := range []int{-1, MaximumRunPageLimit + 1} {
		if _, err := service.ListRuns(ctx, id, limit, ""); !errors.Is(err, ErrInvalidRunQuery) {
			t.Fatalf("limit %d = %v", limit, err)
		}
	}
	if empty, err := service.ListRuns(ctx, otherID, 0, ""); err != nil || len(empty.Items) != 0 {
		t.Fatalf("job without runs = %+v, %v", empty, err)
	}
	if _, err := service.ListRuns(ctx, uuid.New(), 0, ""); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("unknown job = %v", err)
	}
}

func TestRunHistoryReportsAbandonedAndSupersededRuns(t *testing.T) {
	ctx := context.Background()
	pool := jobsTestPool(t)
	insertJob(t, pool, "lease", "@every 1h")
	registry := NewRegistry()
	if err := registry.Register("lease", func(context.Context, Job) error { return nil }); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, registry, nil)
	stale, err := service.claim(ctx)
	if err != nil || len(stale) != 1 {
		t.Fatalf("claim = %+v, %v", stale, err)
	}
	if status := runStatuses(t, service, stale[0].ID); status != "running" {
		t.Fatalf("claimed run = %s", status)
	}
	if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, stale[0].ID); err != nil {
		t.Fatal(err)
	}
	if status := runStatuses(t, service, stale[0].ID); status != "abandoned" {
		t.Fatalf("expired run = %s", status)
	}
	fresh, err := service.claim(ctx)
	if err != nil || len(fresh) != 1 {
		t.Fatalf("reclaim = %+v, %v", fresh, err)
	}
	if status := runStatuses(t, service, stale[0].ID); status != "running,abandoned" {
		t.Fatalf("after reclaim = %s", status)
	}
	if err := service.complete(ctx, stale[0], nil); err != nil {
		t.Fatal(err)
	}
	if err := service.complete(ctx, fresh[0], nil); err != nil {
		t.Fatal(err)
	}
	if status := runStatuses(t, service, stale[0].ID); status != "succeeded,superseded" {
		t.Fatalf("after completions = %s", status)
	}
	// A repeated acknowledgement cannot rewrite a recorded outcome.
	if err := service.complete(ctx, fresh[0], errors.New("late duplicate")); err != nil {
		t.Fatal(err)
	}
	if status := runStatuses(t, service, stale[0].ID); status != "succeeded,superseded" {
		t.Fatalf("after duplicate completion = %s", status)
	}
}

func TestRunHistoryRetentionBoundsRowsPerJobAndAge(t *testing.T) {
	ctx := context.Background()
	pool := jobsTestPool(t)
	insertJob(t, pool, "bounded", "@every 1h")
	insertJob(t, pool, "aged", "@every 1h")
	registry := NewRegistry()
	for _, name := range []string{"bounded", "aged"} {
		if err := registry.Register(name, func(context.Context, Job) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	service := NewServiceWithOptions(pool, registry, nil, Options{RunMaximumRows: 2, RunRetention: 48 * time.Hour})
	bounded := jobID(t, service, "bounded")
	aged := jobID(t, service, "aged")
	// The aged job never runs here: age retention is not tied to the job.
	if _, err := pool.Exec(ctx, `WITH idle AS (UPDATE pgbase_meta.scheduled_jobs SET next_run_at=clock_timestamp()+interval '1 day' WHERE id=$1)
 INSERT INTO pgbase_meta.scheduled_job_runs(job_id,fence_generation,attempt,status,started_at,finished_at)
 VALUES($1,0,1,'succeeded',clock_timestamp()-interval '3 days',clock_timestamp()-interval '3 days')`, aged); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for range 3 {
		makeDue(t, service, bounded)
		if err := service.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		page, err := service.ListRuns(ctx, bounded, 0, "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, page.Items[0].ID)
	}
	page, err := service.ListRuns(ctx, bounded, 0, "")
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != ids[2] || page.Items[1].ID != ids[1] {
		t.Fatalf("bounded history = %+v, %v (ids %v)", page.Items, err, ids)
	}
	if page.Retention.MaximumRuns != 2 || page.Retention.MaximumAgeSeconds != 48*3600 {
		t.Fatalf("retention = %+v", page.Retention)
	}
	// A completion of any job removes runs older than the age limit.
	if old, err := service.ListRuns(ctx, aged, 0, ""); err != nil || len(old.Items) != 0 {
		t.Fatalf("aged history = %+v, %v", old, err)
	}
}

func jobID(t *testing.T, service *Service, name string) uuid.UUID {
	t.Helper()
	items, err := service.ListJobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Name == name {
			return item.ID
		}
	}
	t.Fatalf("job %q missing", name)
	return uuid.Nil
}

func makeDue(t *testing.T, service *Service, id uuid.UUID) {
	t.Helper()
	if _, err := service.pool.Exec(context.Background(), `UPDATE pgbase_meta.scheduled_jobs SET next_run_at=clock_timestamp()-interval '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

func runStatuses(t *testing.T, service *Service, id uuid.UUID) string {
	t.Helper()
	page, err := service.ListRuns(context.Background(), id, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	statuses := make([]string, len(page.Items))
	for index, item := range page.Items {
		statuses[index] = item.Status
	}
	return strings.Join(statuses, ",")
}
