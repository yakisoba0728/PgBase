package jobs

import (
	"context"
	"crypto/rand"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrJobNotFound     = errors.New("scheduled job not found")
	ErrInvalidRunQuery = errors.New("invalid job run query")
)

const (
	defaultRunRetention   = 30 * 24 * time.Hour
	defaultRunMaximumRows = 1000
	defaultRunPageLimit   = 25
	MaximumRunPageLimit   = 100
	// MaximumRunErrorChars bounds the stored failure text of one run, matching
	// the job's own last_error bound.
	MaximumRunErrorChars = 1024
)

// Options bounds the durable run history. Retention follows the request log:
// an age limit plus a row cap, here applied per job.
type Options struct {
	// RunRetention removes runs that started longer ago. Zero selects 30 days.
	RunRetention time.Duration
	// RunMaximumRows keeps at most this many of the newest runs per job.
	// Zero selects 1,000.
	RunMaximumRows int
	// CursorSecret signs run-history continuations. Every instance serving the
	// same console must share it; empty selects a per-process random secret.
	CursorSecret []byte
}

func (o Options) normalized() Options {
	if o.RunRetention <= 0 {
		o.RunRetention = defaultRunRetention
	}
	if o.RunMaximumRows <= 0 {
		o.RunMaximumRows = defaultRunMaximumRows
	}
	if len(o.CursorSecret) == 0 {
		o.CursorSecret = make([]byte, 32)
		_, _ = rand.Read(o.CursorSecret)
	} else {
		o.CursorSecret = append([]byte(nil), o.CursorSecret...)
	}
	return o
}

// Run status values. Abandoned is derived on read: the row is still running
// but its lease expired or was replaced before the worker reported back.
const (
	RunRunning    = "running"
	RunSucceeded  = "succeeded"
	RunFailed     = "failed"
	RunSuperseded = "superseded"
	RunAbandoned  = "abandoned"
)

type Run struct {
	ID          int64      `json:"id"`
	Status      string     `json:"status"`
	Attempt     int        `json:"attempt"`
	ScheduledAt *time.Time `json:"scheduledAt,omitempty"`
	StartedAt   time.Time  `json:"startedAt"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
	DurationMS  *int64     `json:"durationMs,omitempty"`
	Error       string     `json:"error,omitempty"`
}

type RunRetention struct {
	MaximumRuns       int   `json:"maximumRuns"`
	MaximumAgeSeconds int64 `json:"maximumAgeSeconds"`
}

type RunPage struct {
	Items      []Run        `json:"items"`
	NextCursor string       `json:"nextCursor,omitempty"`
	Retention  RunRetention `json:"retention"`
}

// ListRuns returns one job's runs, newest first. A cursor continues strictly
// after the last run of the previous page and is valid only for this job.
func (s *Service) ListRuns(ctx context.Context, id uuid.UUID, limit int, token string) (RunPage, error) {
	if id == uuid.Nil || limit < 0 || limit > MaximumRunPageLimit {
		return RunPage{}, ErrInvalidRunQuery
	}
	if limit == 0 {
		limit = defaultRunPageLimit
	}
	scope := runCursorScope(id)
	var before int64
	if token != "" {
		var err error
		if before, err = s.cursors.DecodeKeyset(scope, token); err != nil {
			return RunPage{}, ErrInvalidRunQuery
		}
	}
	page := RunPage{Items: make([]Run, 0, limit), Retention: RunRetention{
		MaximumRuns: s.history.RunMaximumRows, MaximumAgeSeconds: int64(s.history.RunRetention / time.Second),
	}}
	rows, err := s.pool.Query(ctx, `SELECT r.id,
 CASE WHEN r.status='running' AND NOT COALESCE(j.lease_until>clock_timestamp() AND j.fence_generation=r.fence_generation,false)
 THEN 'abandoned' ELSE r.status END,
 r.attempt,r.scheduled_at,r.started_at,r.finished_at,COALESCE(r.error,'')
 FROM pgbase_meta.scheduled_jobs j JOIN pgbase_meta.scheduled_job_runs r ON r.job_id=j.id
 WHERE j.id=$1 AND ($2::bigint=0 OR r.id<$2) ORDER BY r.id DESC LIMIT $3`, id, before, limit+1)
	if err != nil {
		return RunPage{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item Run
		if err := rows.Scan(&item.ID, &item.Status, &item.Attempt, &item.ScheduledAt, &item.StartedAt, &item.FinishedAt, &item.Error); err != nil {
			return RunPage{}, err
		}
		if item.FinishedAt != nil {
			duration := max(item.FinishedAt.Sub(item.StartedAt).Milliseconds(), 0)
			item.DurationMS = &duration
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return RunPage{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		if page.NextCursor, err = s.cursors.EncodeKeyset(scope, page.Items[limit-1].ID); err != nil {
			return RunPage{}, err
		}
	}
	if len(page.Items) == 0 {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pgbase_meta.scheduled_jobs WHERE id=$1)`, id).Scan(&exists); err != nil {
			return RunPage{}, err
		}
		if !exists {
			return RunPage{}, ErrJobNotFound
		}
	}
	return page, nil
}

func runCursorScope(id uuid.UUID) string { return "jobs.runs:" + id.String() }

// pruneRuns applies the history bounds after a completion. It is best effort:
// a failure leaves extra rows for the next completion to remove and never
// changes the outcome already recorded for the run.
func (s *Service) pruneRuns(ctx context.Context, id uuid.UUID) {
	_, err := s.pool.Exec(ctx, `DELETE FROM pgbase_meta.scheduled_job_runs WHERE job_id=$1 AND id<=(
 SELECT id FROM pgbase_meta.scheduled_job_runs WHERE job_id=$1 ORDER BY id DESC OFFSET $2 LIMIT 1)`, id, s.history.RunMaximumRows)
	if err == nil {
		_, err = s.pool.Exec(ctx, `DELETE FROM pgbase_meta.scheduled_job_runs
 WHERE started_at<clock_timestamp()-make_interval(secs=>$1)`, s.history.RunRetention.Seconds())
	}
	if err != nil && ctx.Err() == nil {
		s.logger.WarnContext(ctx, "scheduled job run history retention deferred", "error", err)
	}
}
