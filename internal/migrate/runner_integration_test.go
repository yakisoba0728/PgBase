//go:build integration

package migrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

func TestUpLogsAppliedMigrationOnlyAfterCommitSucceeds(t *testing.T) {
	pool, err := database.Open(context.Background(), testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const migrationSQL = `
		create schema if not exists app;
		create table app.commit_parent (id integer primary key);
		create table app.commit_child (
			parent_id integer references app.commit_parent(id) deferrable initially deferred
		);
		insert into app.commit_child (parent_id) values (42);`
	var logs bytes.Buffer
	recorder := &migrationTelemetryRecorder{}
	runner := NewWithRecorder(pool, slog.New(slog.NewTextHandler(&logs, nil)), recorder)
	runner.migrations = []system.Migration{{
		Version: 1,
		Name:    "deferred_commit_failure",
		SQL:     migrationSQL,
		SHA256:  sha256.Sum256([]byte(migrationSQL)),
	}}
	if err := runner.Up(context.Background()); err == nil {
		t.Fatal("migration unexpectedly committed")
	}
	if logs.Len() != 0 {
		t.Fatalf("failed commit emitted applied log: %q", logs.String())
	}
	events := recorder.snapshot()
	if len(events) != 1 || events[0].Component() != observability.ComponentMigration || events[0].Operation() != observability.OperationMigrationUp || events[0].Outcome() != observability.OutcomeFailure || events[0].Duration() < 0 {
		t.Fatalf("failed migration telemetry = %#v", events)
	}
}

func TestUpRecordsSuccessAndCancellation(t *testing.T) {
	pool, err := database.Open(context.Background(), testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	recorder := &migrationTelemetryRecorder{}
	runner := NewWithRecorder(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), recorder)
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runner.Up(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Up(canceled) error = %v", err)
	}
	events := recorder.snapshot()
	if len(events) != 2 || events[0].Outcome() != observability.OutcomeSuccess || events[1].Outcome() != observability.OutcomeCanceled {
		t.Fatalf("migration telemetry = %#v", events)
	}
	for index, event := range events {
		if event.Kind() != observability.EventKindOperation || event.Component() != observability.ComponentMigration || event.Operation() != observability.OperationMigrationUp || event.Duration() < 0 {
			t.Fatalf("migration telemetry[%d] = %#v", index, event)
		}
	}
}

func TestStatusRecordsSuccessAndCancellation(t *testing.T) {
	pool, err := database.Open(context.Background(), testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	recorder := &migrationTelemetryRecorder{}
	runner := NewWithRecorder(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), recorder)
	if _, err := runner.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runner.Status(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Status(canceled) error = %v", err)
	}
	events := recorder.snapshot()
	if len(events) != 2 || events[0].Outcome() != observability.OutcomeSuccess || events[1].Outcome() != observability.OutcomeCanceled {
		t.Fatalf("migration status telemetry = %#v", events)
	}
	for index, event := range events {
		if event.Kind() != observability.EventKindOperation || event.Component() != observability.ComponentMigration || event.Operation() != observability.OperationMigrationStatus || event.Duration() < 0 {
			t.Fatalf("migration status telemetry[%d] = %#v", index, event)
		}
	}
}

func TestUpDoesNotApplyRequestStatementTimeoutToMigrationStatements(t *testing.T) {
	pool, err := database.Open(context.Background(), testutil.NewDatabase(t), 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	const migrationSQL = `select pg_sleep(0.25)`
	runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	runner.migrations = []system.Migration{{
		Version: 1,
		Name:    "long_running_ddl",
		SQL:     migrationSQL,
		SHA256:  sha256.Sum256([]byte(migrationSQL)),
	}}
	if err := runner.Up(context.Background()); err != nil {
		t.Fatalf("Up() applied the request statement timeout to a migration: %v", err)
	}
}

func TestUpIsIdempotentAndCreatesSchemas(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	pool, err := database.Open(context.Background(), databaseURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}

	var count int
	err = pool.QueryRow(context.Background(),
		"select count(*) from pgbase_meta.system_migrations where version = 1").Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}

	var appSchema bool
	if err := pool.QueryRow(context.Background(), "select exists(select 1 from pg_namespace where nspname = 'app')").Scan(&appSchema); err != nil {
		t.Fatal(err)
	}
	if !appSchema {
		t.Fatal("app schema was not created")
	}
}

func TestUpRejectsChangedChecksum(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	pool, err := database.Open(context.Background(), databaseURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), "update pgbase_meta.system_migrations set checksum = decode(repeat('00', 32), 'hex') where version = 1"); err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(context.Background()); err == nil {
		t.Fatal("Up succeeded after a checksum change")
	}
}

func TestUpSerializesConcurrentRunners(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	pool, err := database.Open(context.Background(), databaseURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	runners := []*Runner{
		New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))),
		New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	start := make(chan struct{})
	errs := make(chan error, len(runners))
	var wg sync.WaitGroup
	for _, runner := range runners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- runner.Up(context.Background())
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	var count int
	if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.system_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	wantCount := len(system.Migrations())
	if count != wantCount {
		t.Fatalf("history row count = %d, want %d", count, wantCount)
	}
}

func TestUpWaitsForMigrationAdvisoryLock(t *testing.T) {
	pool, err := database.Open(context.Background(), testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	connection, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	lock, err := connection.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec(context.Background(), `select pg_advisory_xact_lock($1)`, advisoryLockKey); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Up(context.Background()) }()
	select {
	case err := <-done:
		t.Fatalf("migration returned before lock release: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := lock.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("migration did not continue after lock release")
	}
}

func TestRecordPageIndexMigrationBackfillsExistingCollections(t *testing.T) {
	pool, err := database.Open(context.Background(), testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	runner.migrations = system.Migrations()[:1]
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		create table app.existing_records (
			id uuid primary key,
			created_at timestamptz not null,
			updated_at timestamptz not null
		);
		insert into pgbase_meta.collections (id, name, table_name, status, created_at, updated_at)
		values ('019551d0-e9c0-7000-8000-000000000099', 'existing_records', 'existing_records', 'active', now(), now())`); err != nil {
		t.Fatal(err)
	}
	runner.migrations = system.Migrations()
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(context.Background(), `
		select count(*) from pg_indexes
		where schemaname = 'app' and tablename = 'existing_records'
		  and indexdef like '%(created_at, id)%'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("pagination index count = %d, want 1", count)
	}
}

func TestStatusReportsAppliedAndLatestVersions(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	pool, err := database.Open(context.Background(), databaseURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	before, err := runner.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	latest := latestSystemMigrationVersion(t)
	if before.Current != 0 || before.Latest != latest || before.Dirty {
		t.Fatalf("status before Up = %#v", before)
	}
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := runner.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.Current != latest || after.Latest != latest || after.Dirty {
		t.Fatalf("status after Up = %#v", after)
	}
}

func TestStatusMarksOutOfOrderMigrationHistoryDirty(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	pool, err := database.Open(context.Background(), databaseURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), "delete from pgbase_meta.system_migrations where version = 2"); err != nil {
		t.Fatal(err)
	}

	status, err := runner.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	latest := latestSystemMigrationVersion(t)
	if !status.Dirty || status.Current != latest || status.Latest != latest {
		t.Fatalf("status with migration gap = %#v, want current=%d latest=%d dirty=true", status, latest, latest)
	}
}

func TestUpRejectsOutOfOrderMigrationHistoryWithoutFillingGap(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	pool, err := database.Open(context.Background(), databaseURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), "delete from pgbase_meta.system_migrations where version = 2"); err != nil {
		t.Fatal(err)
	}

	err = runner.Up(context.Background())
	if err == nil || !strings.Contains(err.Error(), "out of order") {
		t.Fatalf("Up error = %v, want out-of-order history rejection", err)
	}
	status, statusErr := runner.Status(context.Background())
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if !status.Dirty {
		t.Fatalf("status after rejected Up = %#v, want gap to remain dirty", status)
	}
}
