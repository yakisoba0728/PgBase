//go:build integration && load

package pgbase

// Optional test-only observations for a separately identified diagnostic run.
// The 100 ms sampler has its own DB connection and changes measured overhead;
// it is not enabled in the historical performance comparison or production.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const openLoopDiagnosticInterval = 100 * time.Millisecond

type openLoopDBSnapshot struct {
	ObservedUnixNS int64           `json:"observed_unix_ns"`
	Database       json.RawMessage `json:"database"`
	ClusterIO      json.RawMessage `json:"cluster_io"`
	ClusterWAL     json.RawMessage `json:"cluster_wal"`
	Settings       json.RawMessage `json:"settings"`
}

type openLoopDBActivity struct {
	PID          int32   `json:"pid"`
	Application  string  `json:"application"`
	State        string  `json:"state"`
	WaitType     string  `json:"wait_type"`
	Wait         string  `json:"wait"`
	ActiveMS     float64 `json:"active_ms"`
	QueryHash    string  `json:"query_hash"`
	QueryPrefix  string  `json:"query_prefix"`
	BlockingPIDs []int32 `json:"blocking_pids"`
}

type openLoopDBObservation struct {
	ScheduledUnixNS int64                `json:"scheduled_unix_ns"`
	StartedUnixNS   int64                `json:"started_unix_ns"`
	QueryNS         int64                `json:"query_ns"`
	Backends        []openLoopDBActivity `json:"backends"`
}

type openLoopDiagnostics struct {
	connection *pgx.Conn
	directory  string
	before     openLoopDBSnapshot
	after      openLoopDBSnapshot
	observed   []openLoopDBObservation
	loadStart  time.Time
	err        error
	cancel     context.CancelFunc
	done       chan struct{}
}

func newOpenLoopDiagnostics(t *testing.T, fixture *publicUsersFixture) *openLoopDiagnostics {
	t.Helper()
	flag := os.Getenv("PGBASE_OPEN_LOOP_DIAGNOSTICS")
	if flag == "" {
		return nil
	}
	if flag != "1" {
		t.Fatal("PGBASE_OPEN_LOOP_DIAGNOSTICS must be unset or 1")
	}
	directory := os.Getenv("PGBASE_LOAD_EVIDENCE_DIR")
	if directory == "" || !filepath.IsAbs(directory) {
		t.Fatal("open-loop diagnostics require an absolute PGBASE_LOAD_EVIDENCE_DIR")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(fixture.app.options.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Del("pool_max_conns")
	query.Set("application_name", "pgbase_open_loop_observer")
	parsed.RawQuery = query.Encode()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close open-loop observer: %v", err)
		}
	})
	result := &openLoopDiagnostics{connection: connection, directory: directory, done: make(chan struct{})}
	result.before, err = result.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (s *openLoopDiagnostics) snapshot(ctx context.Context) (openLoopDBSnapshot, error) {
	result := openLoopDBSnapshot{ObservedUnixNS: time.Now().UnixNano()}
	err := s.connection.QueryRow(ctx, `SELECT to_jsonb(d),
	 (SELECT jsonb_agg(to_jsonb(i)) FROM pg_stat_io i),
	 (SELECT to_jsonb(w) FROM pg_stat_wal w),
	 (SELECT jsonb_object_agg(name,setting) FROM pg_settings WHERE name IN
	 ('server_version','shared_buffers','max_connections','work_mem','maintenance_work_mem',
	 'effective_cache_size','max_parallel_workers_per_gather','fsync','synchronous_commit',
	 'track_io_timing','track_wal_io_timing','wal_sync_method','checkpoint_timeout',
	 'checkpoint_completion_target','max_wal_size','autovacuum_naptime','stats_fetch_consistency'))
	 FROM pg_stat_database d WHERE datname=current_database()`).Scan(
		&result.Database, &result.ClusterIO, &result.ClusterWAL, &result.Settings)
	return result, err
}

func (s *openLoopDiagnostics) start(t *testing.T, ctx context.Context, loadStart time.Time) {
	if s == nil {
		return
	}
	t.Helper()
	s.loadStart = loadStart
	ctx, s.cancel = context.WithCancel(ctx)
	t.Cleanup(func() { s.cancel(); <-s.done })
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(openLoopDiagnosticInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case scheduled := <-ticker.C:
				began := time.Now()
				sample := openLoopDBObservation{ScheduledUnixNS: scheduled.UnixNano(), StartedUnixNS: began.UnixNano()}
				// Stopping admission must not cancel the observer's in-flight
				// query and destroy its connection before the final snapshot.
				queryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
				rows, err := s.connection.Query(queryCtx, `SELECT pid,coalesce(application_name,''),
				 coalesce(state,''),coalesce(wait_event_type,''),coalesce(wait_event,''),
				 CASE WHEN state='active' THEN extract(epoch FROM clock_timestamp()-query_start)::double precision*1000 ELSE 0 END,
				 md5(coalesce(query,'')),regexp_replace(left(coalesce(query,''),240),'[[:space:]]+',' ','g'),
				 pg_blocking_pids(pid)
				 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() ORDER BY pid`)
				if err == nil {
					for rows.Next() {
						var activity openLoopDBActivity
						if err = rows.Scan(&activity.PID, &activity.Application, &activity.State, &activity.WaitType,
							&activity.Wait, &activity.ActiveMS, &activity.QueryHash, &activity.QueryPrefix, &activity.BlockingPIDs); err != nil {
							break
						}
						sample.Backends = append(sample.Backends, activity)
					}
					if err == nil {
						err = rows.Err()
					}
					rows.Close()
				}
				cancel()
				sample.QueryNS = time.Since(began).Nanoseconds()
				s.observed = append(s.observed, sample)
				if err != nil {
					s.err = err
					return
				}
			}
		}
	}()
}

func (s *openLoopDiagnostics) stop(t *testing.T, elapsed time.Duration) {
	if s == nil {
		return
	}
	t.Helper()
	s.cancel()
	<-s.done
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var err error
	s.after, err = s.snapshot(ctx)
	if err != nil {
		s.err = errors.Join(s.err, fmt.Errorf("final open-loop DB snapshot: %w", err))
	}
	if len(s.observed) == 0 {
		s.err = errors.Join(s.err, errors.New("open-loop observer recorded no samples"))
	}
	result := struct {
		StartUnixNS int64                   `json:"load_start_unix_ns"`
		ElapsedNS   int64                   `json:"elapsed_ns"`
		IntervalMS  int64                   `json:"interval_ms"`
		Scope       string                  `json:"scope"`
		Error       string                  `json:"diagnostic_error,omitempty"`
		Before      openLoopDBSnapshot      `json:"before"`
		After       openLoopDBSnapshot      `json:"after"`
		Observed    []openLoopDBObservation `json:"observations"`
	}{StartUnixNS: s.loadStart.UnixNano(), ElapsedNS: elapsed.Nanoseconds(), IntervalMS: openLoopDiagnosticInterval.Milliseconds(),
		Scope:  "Independent observer connection; per-database activity/statistics; cluster-wide IO/WAL; sampled waits and query ages are not exact durations or proof of a cause; instrumented stratum",
		Before: s.before, After: s.after, Observed: s.observed}
	if s.err != nil {
		result.Error = s.err.Error()
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(s.directory, "open-loop-database.json"), append(encoded, '\n'), 0600)
	}
	if err != nil {
		s.err = errors.Join(s.err, fmt.Errorf("write open-loop DB diagnostics: %w", err))
	}
	if s.err != nil {
		t.Errorf("open-loop DB diagnostics: %v", s.err)
	}
	t.Logf("model=open-loop db_observations=%d interval_ms=%d independent_connection=true instrumented=true", len(s.observed), openLoopDiagnosticInterval.Milliseconds())
}
