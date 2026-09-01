//go:build integration

package migrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"strconv"
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

func TestUpThroughAppliesExactlyOneLatestMigrationAndReturnsReadback(t *testing.T) {
	pool, err := database.Open(context.Background(), testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	migrations := system.Migrations()
	if len(migrations) < 2 {
		t.Fatal("UpThrough test requires at least two embedded migrations")
	}
	var logs bytes.Buffer
	recorder := &migrationTelemetryRecorder{}
	runner := NewWithRecorder(pool, slog.New(slog.NewTextHandler(&logs, nil)), recorder)
	runner.migrations = migrations[:len(migrations)-1]
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	logs.Reset()
	runner.migrations = migrations
	target := migrations[len(migrations)-1]
	before := migrations[len(migrations)-2]

	receipt, err := runner.UpThrough(context.Background(), target.Version)
	if err != nil {
		t.Fatal(err)
	}
	want := UpThroughReceipt{
		Target: target.Version,
		Before: HistoryState{Version: before.Version, Checksum: before.SHA256},
		After:  HistoryState{Version: target.Version, Checksum: target.SHA256},
	}
	if receipt != want {
		t.Fatalf("UpThrough receipt = %#v, want %#v", receipt, want)
	}
	logLine := logs.String()
	if !strings.Contains(logLine, "version="+strconv.FormatInt(target.Version, 10)) || !strings.Contains(logLine, "name="+target.Name) || !strings.Contains(logLine, "execution_ms=") {
		t.Fatalf("UpThrough applied log = %q", logLine)
	}
	events := recorder.snapshot()
	if len(events) != 2 || events[1].Operation() != observability.OperationMigrationUp || events[1].Outcome() != observability.OutcomeSuccess {
		t.Fatalf("UpThrough telemetry = %#v", events)
	}
	status, err := runner.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status != (Status{Current: target.Version, Latest: target.Version, Compatible: true}) {
		t.Fatalf("Status after UpThrough = %#v", status)
	}
}

func TestUpThroughRejectsChecksumMismatchAsTypedIssueWithoutApplyingTarget(t *testing.T) {
	pool, err := database.Open(context.Background(), testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	migrations := system.Migrations()
	runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	runner.migrations = migrations[:len(migrations)-1]
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), "update pgbase_meta.system_migrations set checksum = decode(repeat('00', 32), 'hex') where version = 1"); err != nil {
		t.Fatal(err)
	}
	runner.migrations = migrations

	_, err = runner.UpThrough(context.Background(), migrations[len(migrations)-1].Version)
	var issue *HistoryIssue
	if !errors.As(err, &issue) || issue.Kind != HistoryIssueChecksumMismatch || issue.Version != 1 {
		t.Fatalf("UpThrough error = %v, issue = %#v", err, issue)
	}
	status, statusErr := runner.Status(context.Background())
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if status.Current != migrations[len(migrations)-2].Version || !status.Dirty || status.Issue == nil || status.Issue.Kind != HistoryIssueChecksumMismatch {
		t.Fatalf("Status after rejected UpThrough = %#v", status)
	}
}

func TestUpThroughClassifiesUnknownVersionAndGapWithoutApplyingTarget(t *testing.T) {
	migrations := system.Migrations()
	target := migrations[len(migrations)-1]
	previous := migrations[len(migrations)-2]
	tests := []struct {
		name       string
		kind       HistoryIssueKind
		version    int64
		breakSQL   string
		breakArgs  []any
		repairSQL  string
		repairArgs []any
	}{
		{
			name:      "unknown version",
			kind:      HistoryIssueUnknownVersion,
			version:   99,
			breakSQL:  "insert into pgbase_meta.system_migrations (version, name, checksum, execution_ms) values (99, 'future', decode(repeat('00', 32), 'hex'), 0)",
			repairSQL: "delete from pgbase_meta.system_migrations where version = 99",
		},
		{
			name:       "history gap",
			kind:       HistoryIssueGap,
			version:    migrations[1].Version,
			breakSQL:   "delete from pgbase_meta.system_migrations where version = $1",
			breakArgs:  []any{migrations[1].Version},
			repairSQL:  "insert into pgbase_meta.system_migrations (version, name, checksum, execution_ms) values ($1, $2, $3, 0)",
			repairArgs: []any{migrations[1].Version, migrations[1].Name, migrations[1].SHA256[:]},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool, err := database.Open(context.Background(), testutil.NewDatabase(t), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()

			runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
			runner.migrations = migrations[:len(migrations)-1]
			if err := runner.Up(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(context.Background(), test.breakSQL, test.breakArgs...); err != nil {
				t.Fatal(err)
			}
			runner.migrations = migrations

			_, err = runner.UpThrough(context.Background(), target.Version)
			var issue *HistoryIssue
			if !errors.As(err, &issue) || issue.Kind != test.kind || issue.Version != test.version {
				t.Fatalf("UpThrough error = %v, issue = %#v", err, issue)
			}
			status, statusErr := runner.Status(context.Background())
			if statusErr != nil {
				t.Fatal(statusErr)
			}
			if !status.Dirty || status.Issue == nil || status.Issue.Kind != test.kind {
				t.Fatalf("dirty Status = %#v", status)
			}

			if _, err := pool.Exec(context.Background(), test.repairSQL, test.repairArgs...); err != nil {
				t.Fatal(err)
			}
			status, statusErr = runner.Status(context.Background())
			if statusErr != nil {
				t.Fatal(statusErr)
			}
			if status != (Status{Current: previous.Version, Latest: target.Version}) {
				t.Fatalf("Status after history repair = %#v, target migration was not expected to run", status)
			}
		})
	}
}

func TestUpThroughRejectsFreshAndMultiplePendingHistoriesWithoutDDL(t *testing.T) {
	migrations := system.Migrations()
	target := migrations[len(migrations)-1]
	t.Run("fresh database", func(t *testing.T) {
		pool, err := database.Open(context.Background(), testutil.NewDatabase(t), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))

		receipt, err := runner.UpThrough(context.Background(), target.Version)
		if err == nil || !strings.Contains(err.Error(), "requires current version") {
			t.Fatalf("UpThrough error = %v", err)
		}
		if receipt.Before != (HistoryState{}) || receipt.After != (HistoryState{}) {
			t.Fatalf("UpThrough receipt = %#v", receipt)
		}
		var historyTable *string
		if err := pool.QueryRow(context.Background(), "select to_regclass('pgbase_meta.system_migrations')::text").Scan(&historyTable); err != nil {
			t.Fatal(err)
		}
		if historyTable != nil {
			t.Fatalf("fresh rejected UpThrough created history table %q", *historyTable)
		}
	})

	t.Run("more than one manifest entry pending", func(t *testing.T) {
		pool, err := database.Open(context.Background(), testutil.NewDatabase(t), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
		runner.migrations = migrations[:len(migrations)-2]
		if err := runner.Up(context.Background()); err != nil {
			t.Fatal(err)
		}
		current := migrations[len(migrations)-3]
		runner.migrations = migrations

		receipt, err := runner.UpThrough(context.Background(), target.Version)
		if err == nil || !strings.Contains(err.Error(), "requires current version") {
			t.Fatalf("UpThrough error = %v", err)
		}
		if receipt.Before.Version != current.Version || receipt.Before.Checksum != current.SHA256 || receipt.After != (HistoryState{}) {
			t.Fatalf("UpThrough receipt = %#v", receipt)
		}
		status, statusErr := runner.Status(context.Background())
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		if status.Current != current.Version || status.Dirty || status.Compatible {
			t.Fatalf("Status after rejected UpThrough = %#v", status)
		}
	})
}

func TestUpThroughRejectsNonLatestAndNonEmbeddedTargetsBeforeDDL(t *testing.T) {
	pool, err := database.Open(context.Background(), testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	migrations := system.Migrations()
	runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, target := range []int64{migrations[len(migrations)-2].Version, 999} {
		if _, err := runner.UpThrough(context.Background(), target); err == nil || !strings.Contains(err.Error(), "not the latest embedded version") {
			t.Fatalf("UpThrough(%d) error = %v", target, err)
		}
	}
	var historyTable *string
	if err := pool.QueryRow(context.Background(), "select to_regclass('pgbase_meta.system_migrations')::text").Scan(&historyTable); err != nil {
		t.Fatal(err)
	}
	if historyTable != nil {
		t.Fatalf("invalid targets created history table %q", *historyTable)
	}
}

func TestUpThroughRejectsMalformedEmbeddedManifestBeforeDDL(t *testing.T) {
	migrations := system.Migrations()
	tests := []struct {
		name   string
		mutate func([]system.Migration)
	}{
		{
			name: "duplicate version",
			mutate: func(manifest []system.Migration) {
				manifest[len(manifest)-1].Version = manifest[len(manifest)-2].Version
			},
		},
		{
			name: "out of order version",
			mutate: func(manifest []system.Migration) {
				manifest[len(manifest)-1].Version = manifest[len(manifest)-2].Version - 1
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool, err := database.Open(context.Background(), testutil.NewDatabase(t), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			manifest := append([]system.Migration(nil), migrations...)
			test.mutate(manifest)
			runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
			runner.migrations = manifest

			if _, err := runner.UpThrough(context.Background(), manifest[len(manifest)-1].Version); err == nil || !strings.Contains(err.Error(), "unique and strictly increasing") {
				t.Fatalf("UpThrough error = %v", err)
			}
			var historyTable *string
			if err := pool.QueryRow(context.Background(), "select to_regclass('pgbase_meta.system_migrations')::text").Scan(&historyTable); err != nil {
				t.Fatal(err)
			}
			if historyTable != nil {
				t.Fatalf("malformed manifest created history table %q", *historyTable)
			}
		})
	}
}

func TestUpThroughAlreadyAtTargetReturnsStableReceiptWithoutApplying(t *testing.T) {
	pool, err := database.Open(context.Background(), testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var logs bytes.Buffer
	runner := New(pool, slog.New(slog.NewTextHandler(&logs, nil)))
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	logs.Reset()
	target := system.Migrations()[len(system.Migrations())-1]

	receipt, err := runner.UpThrough(context.Background(), target.Version)
	if err != nil {
		t.Fatal(err)
	}
	state := HistoryState{Version: target.Version, Checksum: target.SHA256}
	if receipt != (UpThroughReceipt{Target: target.Version, Before: state, After: state}) {
		t.Fatalf("UpThrough receipt = %#v", receipt)
	}
	if logs.Len() != 0 {
		t.Fatalf("already-current UpThrough emitted applied log: %q", logs.String())
	}
}

func TestUpThroughFailsClosedWhenPostCommitChecksumReadbackDiffers(t *testing.T) {
	pool, err := database.Open(context.Background(), testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	migrations := system.Migrations()
	runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	runner.migrations = migrations[:len(migrations)-1]
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		create function pgbase_meta.corrupt_migration_checksum() returns trigger
		language plpgsql as $body$
		begin
			new.checksum = decode(repeat('00', 32), 'hex');
			return new;
		end
		$body$;
		create trigger corrupt_migration_checksum
		before insert on pgbase_meta.system_migrations
		for each row execute function pgbase_meta.corrupt_migration_checksum()`); err != nil {
		t.Fatal(err)
	}
	runner.migrations = migrations
	target := migrations[len(migrations)-1]

	receipt, err := runner.UpThrough(context.Background(), target.Version)
	var issue *HistoryIssue
	if !errors.As(err, &issue) || issue.Kind != HistoryIssueChecksumMismatch || issue.Version != target.Version {
		t.Fatalf("UpThrough error = %v, issue = %#v", err, issue)
	}
	if receipt.Before.Version != migrations[len(migrations)-2].Version || receipt.After.Version != target.Version || !receipt.After.Dirty {
		t.Fatalf("UpThrough receipt = %#v", receipt)
	}
	status, statusErr := runner.Status(context.Background())
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if status.Current != target.Version || !status.Dirty || status.Compatible {
		t.Fatalf("Status after readback mismatch = %#v", status)
	}
}

func TestStatusCompatibleAcceptsOnlyValidatedRequiredAndOptionalLatest(t *testing.T) {
	pool, err := database.Open(context.Background(), testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	migrations := system.Migrations()
	target := migrations[len(migrations)-1]
	required := migrations[len(migrations)-2]
	runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	runner.migrations = migrations[:len(migrations)-1]
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	runner.migrations = migrations
	runner.compatibility = nil
	status, err := runner.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Compatible {
		t.Fatalf("default Status unexpectedly accepted pending latest migration: %#v", status)
	}
	runner.compatibility = &historyCompatibilityPolicy{requiredVersion: required.Version, optionalVersion: target.Version}
	status, err = runner.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Compatible || status.Current != required.Version {
		t.Fatalf("required-history Status = %#v", status)
	}
	if _, err := runner.UpThrough(context.Background(), target.Version); err != nil {
		t.Fatal(err)
	}
	status, err = runner.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Compatible || status.Current != target.Version {
		t.Fatalf("optional-history Status = %#v", status)
	}
	runner.compatibility = &historyCompatibilityPolicy{requiredVersion: migrations[len(migrations)-3].Version, optionalVersion: target.Version}
	if _, err := runner.Status(context.Background()); err == nil || !strings.Contains(err.Error(), "latest manifest entry optional") {
		t.Fatalf("Status with invalid compatibility policy error = %v", err)
	}
}

func TestNewRunnerRequiresTheSchemaOperationsFeatureHistory(t *testing.T) {
	pool, err := database.Open(context.Background(), testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	migrations := system.Migrations()
	if len(migrations) != 6 || migrations[len(migrations)-2].Version != 5 || migrations[len(migrations)-1].Version != 6 {
		t.Fatalf("schema-operations feature manifest = %#v", migrations)
	}

	bootstrap := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	bootstrap.migrations = migrations[:len(migrations)-1]
	bootstrap.compatibility = nil
	if err := bootstrap.Up(context.Background()); err != nil {
		t.Fatal(err)
	}

	runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	status, err := runner.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status != (Status{Current: 5, Latest: 6}) {
		t.Fatalf("feature status before required migration = %#v", status)
	}

	if _, err := runner.UpThrough(context.Background(), 6); err != nil {
		t.Fatal(err)
	}
	status, err = runner.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status != (Status{Current: 6, Latest: 6, Compatible: true}) {
		t.Fatalf("feature status after required migration = %#v", status)
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
	err = runner.Up(context.Background())
	var issue *HistoryIssue
	if !errors.As(err, &issue) || issue.Kind != HistoryIssueChecksumMismatch || issue.Version != 1 {
		t.Fatalf("Up error = %v, issue = %#v", err, issue)
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
	if before.Current != 0 || before.Latest != latest || before.Dirty || before.Compatible {
		t.Fatalf("status before Up = %#v", before)
	}
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := runner.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.Current != latest || after.Latest != latest || after.Dirty || !after.Compatible {
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

func TestStatusAndExactTargetRejectDuplicateMigrationVersions(t *testing.T) {
	pool, err := database.Open(context.Background(), testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		alter table pgbase_meta.system_migrations drop constraint system_migrations_pkey;
		insert into pgbase_meta.system_migrations (version, name, checksum, applied_at, execution_ms)
		select version, name, checksum, applied_at, execution_ms
		from pgbase_meta.system_migrations where version = 1`); err != nil {
		t.Fatal(err)
	}

	status, err := runner.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	latest := latestSystemMigrationVersion(t)
	if !status.Dirty || status.Current != latest || status.Latest != latest || status.Compatible ||
		status.Issue == nil || status.Issue.Kind != HistoryIssueKind("duplicate_version") || status.Issue.Version != 1 {
		t.Fatalf("status with duplicate migration version = %#v", status)
	}
	_, err = runner.UpThrough(context.Background(), latest)
	var issue *HistoryIssue
	if !errors.As(err, &issue) || issue.Kind != HistoryIssueKind("duplicate_version") || issue.Version != 1 {
		t.Fatalf("UpThrough error = %v, issue = %#v", err, issue)
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
	var issue *HistoryIssue
	if !errors.As(err, &issue) || issue.Kind != HistoryIssueGap || issue.Version != 2 || !strings.Contains(err.Error(), "out of order") {
		t.Fatalf("Up error = %v, issue = %#v", err, issue)
	}
	status, statusErr := runner.Status(context.Background())
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if !status.Dirty {
		t.Fatalf("status after rejected Up = %#v, want gap to remain dirty", status)
	}
}
