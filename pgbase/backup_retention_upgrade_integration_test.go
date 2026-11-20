//go:build integration

package pgbase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/jobs"
	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
	"github.com/yakisoba0728/PgBase/internal/testutil"
	"github.com/yakisoba0728/PgBase/migrations/system"
)

type retentionInstallation struct {
	pool     *database.Pool
	settings *runtimesettings.Controller
	manager  *backup.Manager
	storage  *backup.StorageController
	worker   *jobs.Service
}

var retentionTestKey = []byte("retention-upgrade-runtime-settings-key")

// newRetentionInstallation reproduces what an installation upgraded from the
// previous release holds: archives, the seeded Backup storage row, stored
// runtime settings and, for `pgbase serve`, no managed-backup job row.
func newRetentionInstallation(t *testing.T, envRetention, pageRetention int, stored runtimesettings.Backups, jobRow bool) retentionInstallation {
	t.Helper()
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	testutil.ApplySystemMigrationPrefix(t, databaseURL, system.Migrations())
	pool, err := database.Open(ctx, databaseURL, 5*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	directory := t.TempDir()
	local := backup.NewLocalRepository(directory)
	for index := range 4 {
		entry := backup.Entry{ID: uuid.NewString(), CreatedAt: time.Now().Add(time.Duration(index-10) * time.Minute), Size: 1}
		if err = local.Publish(ctx, entry, bytes.NewReader([]byte("x"))); err != nil {
			t.Fatal(err)
		}
	}
	storage, err := backup.NewStorageController(ctx, pool, []byte("retention-upgrade-token-secret-32bytes"), local, backup.StorageConfig{Driver: "local", Retention: envRetention})
	if err != nil {
		t.Fatal(err)
	}
	if pageRetention >= 0 {
		current, settingsErr := storage.Settings(ctx)
		if settingsErr != nil {
			t.Fatal(settingsErr)
		}
		if _, err = storage.Update(ctx, backup.StorageUpdate{StorageConfig: backup.StorageConfig{Driver: "local", Retention: pageRetention}, Revision: current.Revision}); err != nil {
			t.Fatal(err)
		}
	}
	settings, err := runtimesettings.New(ctx, pool, retentionTestKey)
	if err != nil {
		t.Fatal(err)
	}
	value := settings.Get()
	value.Backups.Enabled, value.Backups.Keep = stored.Enabled, stored.Keep
	if _, err = settings.Update(ctx, value); err != nil {
		t.Fatal(err)
	}
	if jobRow {
		if _, err = pool.Exec(ctx, `INSERT INTO pgbase_meta.scheduled_jobs(id,name,schedule,next_run_at) VALUES($1,$2,'@every 24h',now())`, uuid.New(), managedBackupJobName); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := backup.NewManager(backup.ManagerOptions{Directory: directory, Repository: storage, Create: func(ctx context.Context, destination string) error {
		return backup.CreateOnline(ctx, destination, databaseURL, nil)
	}, Restore: func(context.Context, string) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	return retentionInstallation{pool: pool, settings: settings, manager: manager, storage: storage, worker: jobs.NewService(pool, jobs.NewRegistry(), slog.New(slog.NewTextHandler(io.Discard, nil)))}
}

func (i retentionInstallation) install(t *testing.T) func(context.Context) {
	t.Helper()
	reconcile, err := installManagedBackupSchedule(context.Background(), i.worker, i.manager, i.settings, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return reconcile
}

func (i retentionInstallation) backupAndCount(t *testing.T) int {
	t.Helper()
	ctx := context.Background()
	if err := i.manager.RunScheduled(ctx, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	entries, err := i.storage.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func (i retentionInstallation) jobEnabled(t *testing.T) bool {
	t.Helper()
	var enabled bool
	if err := i.pool.QueryRow(context.Background(), `SELECT enabled FROM pgbase_meta.scheduled_jobs WHERE name=$1`, managedBackupJobName).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	return enabled
}

func TestManagedBackupScheduleOnlyFollowsRuntimeSettings(t *testing.T) {
	ctx := t.Context()
	installation := newRetentionInstallation(t, 4, -1, runtimesettings.Backups{}, false)
	installation.install(t)
	items, err := installation.worker.ListJobs(ctx)
	if err != nil || len(items) != 1 || !items[0].SettingsManaged || items[0].Enabled {
		t.Fatalf("initial managed jobs = %#v, %v", items, err)
	}
	id := items[0].ID
	if err = installation.worker.ConfigureJobInTimezone(ctx, id, "@every 1h", "UTC", true); !errors.Is(err, jobs.ErrNotManageable) {
		t.Fatalf("generic configuration returned %v", err)
	}
	if installation.jobEnabled(t) {
		t.Fatal("generic configuration enabled scheduled backups")
	}
	settings := installation.settings.Get()
	settings.Backups.Enabled = true
	if _, err = installation.settings.Update(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if !installation.jobEnabled(t) {
		t.Fatal("runtime settings did not enable scheduled backups")
	}
	if err = installation.worker.RequestRun(ctx, id); !errors.Is(err, jobs.ErrNotManageable) {
		t.Fatalf("generic run returned %v", err)
	}
}

// Four archives exist before the upgrade; the next backup makes five. The
// previous `pgbase serve` pruned only with the Backup storage retention.
func TestManagedBackupUpgradeKeepsThePreviousRetention(t *testing.T) {
	for _, test := range []struct {
		name               string
		env, page          int
		stored             runtimesettings.Backups
		jobRow             bool
		wantKept, wantKeep int
	}{
		{name: "neither", env: 0, page: -1, wantKept: 5},
		{name: "environment only", env: 2, page: -1, wantKept: 2},
		{name: "stored keep only", env: 0, page: -1, stored: runtimesettings.Backups{Keep: 3}, wantKept: 5},
		{name: "environment and stored keep", env: 2, page: -1, stored: runtimesettings.Backups{Keep: 3}, wantKept: 2},
		{name: "storage page retention", env: 0, page: 2, wantKept: 2},
		{name: "stored schedule enabled", env: 0, page: -1, stored: runtimesettings.Backups{Enabled: true, Keep: 1}, wantKept: 5},
		// An embedder whose own manager already registered the job applied keep.
		{name: "job already registered", env: 2, page: -1, stored: runtimesettings.Backups{Keep: 3}, jobRow: true, wantKept: 3, wantKeep: 3},
		// Deliberate change for such an embedder: keep 0 is "not set" and no
		// longer overrides the Backup storage retention with "keep all".
		{name: "job already registered without keep", env: 2, page: -1, jobRow: true, wantKept: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			installation := newRetentionInstallation(t, test.env, test.page, test.stored, test.jobRow)
			installation.install(t)
			stored := installation.settings.Get().Backups
			if stored.Keep != test.wantKeep || stored.Enabled || installation.jobEnabled(t) {
				t.Fatalf("after upgrade backups=%#v job enabled=%t", stored, installation.jobEnabled(t))
			}
			if kept := installation.backupAndCount(t); kept != test.wantKept {
				t.Fatalf("kept %d archives, want %d", kept, test.wantKept)
			}
		})
	}
}

// After the upgrade a positive keep chosen by an administrator wins over the
// Backup storage retention, and the scheduled run sees a value that another
// instance saved after this process loaded its settings.
func TestManagedBackupScheduledRunUsesTheLatestKeep(t *testing.T) {
	ctx := context.Background()
	installation := newRetentionInstallation(t, 4, -1, runtimesettings.Backups{}, false)
	installation.install(t)
	other, err := runtimesettings.New(ctx, installation.pool, retentionTestKey)
	if err != nil {
		t.Fatal(err)
	}
	value := other.Get()
	value.Backups.Enabled, value.Backups.Keep = true, 1
	if _, err = other.Update(ctx, value); err != nil {
		t.Fatal(err)
	}
	// What the other instance's own subscription writes to the shared row.
	if _, err = installation.pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs SET enabled=true,next_run_at=now() WHERE name=$1`, managedBackupJobName); err != nil {
		t.Fatal(err)
	}
	second := jobs.NewService(installation.pool, jobs.NewRegistry(), nil)
	if err = second.Register(ctx, managedBackupJobName, "@every 24h", "UTC", false, func(context.Context, jobs.Job) error {
		t.Error("a second instance ran the same occurrence")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = installation.worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err = second.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	running, err := installation.manager.RunningJobs(ctx)
	if err != nil || len(running) != 0 {
		t.Fatalf("running = %#v %v", running, err)
	}
	entries, err := installation.storage.List(ctx)
	if err != nil || len(entries) != 1 {
		t.Fatalf("kept %d archives, want 1 (%v)", len(entries), err)
	}
}

func TestManagedBackupRetryReusesPublishedOccurrenceOnAnotherWorker(t *testing.T) {
	ctx := t.Context()
	installation := newRetentionInstallation(t, 0, -1, runtimesettings.Backups{Enabled: true}, true)
	installation.install(t)
	if err := installation.worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := installation.storage.List(ctx)
	if err != nil || len(before) != 5 {
		t.Fatalf("first worker archives = %#v, %v", before, err)
	}
	// Simulate an expired claim whose worker published the archive before it
	// could record success. The scheduler's prior occurrence stays the same.
	if _, err := installation.pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs
 SET attempts=1,next_run_at=clock_timestamp() WHERE name=$1`, managedBackupJobName); err != nil {
		t.Fatal(err)
	}
	replica, err := backup.NewManager(backup.ManagerOptions{
		Directory: t.TempDir(), Repository: installation.storage,
		Create:  func(context.Context, string) error { return errors.New("retry created a duplicate snapshot") },
		Restore: func(context.Context, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replica.Shutdown(context.Background()) })
	settings, err := runtimesettings.New(ctx, installation.pool, retentionTestKey)
	if err != nil {
		t.Fatal(err)
	}
	worker := jobs.NewService(installation.pool, jobs.NewRegistry(), nil)
	if _, err := installManagedBackupSchedule(ctx, worker, replica, settings, slog.Default()); err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := worker.ListJobs(ctx)
	if err != nil || len(items) != 1 || items[0].Attempts != 0 || items[0].Failed {
		t.Fatalf("replica did not successfully acknowledge the existing backup: %#v, %v", items, err)
	}
	after, err := installation.storage.List(ctx)
	if err != nil || len(after) != len(before) || after[0].ID != before[0].ID {
		t.Fatalf("replica changed the published archive: before=%#v after=%#v error=%v", before, after, err)
	}
}

// A stored document that predates the rate-limit label rules reloads but
// cannot be updated, so the inert values cannot be cleared. The job is then
// left unregistered and pruning keeps the Backup storage retention.
func TestManagedBackupUpgradeWithUnsavableSettingsKeepsPreviousBehaviour(t *testing.T) {
	ctx := context.Background()
	installation := newRetentionInstallation(t, 2, -1, runtimesettings.Backups{Enabled: true, Keep: 3}, false)
	legacy := installation.settings.Get()
	legacy.RateLimits.Rules = []runtimesettings.APIRateRule{{Label: "posts:list", Duration: 60, MaxRequests: 5}}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = installation.pool.Exec(ctx, `UPDATE pgbase_meta.runtime_settings SET document_ciphertext=NULL,document=$1::jsonb,revision=revision+1 WHERE singleton`, string(raw)); err != nil {
		t.Fatal(err)
	}
	if err = installation.settings.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	installation.install(t)
	items, err := installation.worker.ListJobs(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("jobs = %#v %v", items, err)
	}
	if kept := installation.backupAndCount(t); kept != 2 {
		t.Fatalf("kept %d archives, want 2", kept)
	}
}

func TestManagedBackupDisabledDuringLeaseSkipsNextRunAndReconciles(t *testing.T) {
	ctx := t.Context()
	installation := newRetentionInstallation(t, 4, -1, runtimesettings.Backups{Enabled: true}, true)
	reconcile := installation.install(t)
	if !installation.jobEnabled(t) {
		t.Fatal("job must start enabled")
	}
	if _, err := installation.pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs SET lease_token=$2,lease_until=clock_timestamp()+interval '1 minute' WHERE name=$1`, managedBackupJobName, uuid.New()); err != nil {
		t.Fatal(err)
	}
	document := installation.settings.Get()
	document.Backups.Enabled = false
	if _, err := installation.settings.Update(ctx, document); err != nil {
		t.Fatal(err)
	}
	if _, err := installation.pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs SET lease_token=NULL,lease_until=NULL,next_run_at=clock_timestamp() WHERE name=$1`, managedBackupJobName); err != nil {
		t.Fatal(err)
	}
	if err := installation.worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	running, err := installation.manager.RunningJobs(ctx)
	if err != nil || len(running) != 0 {
		t.Fatalf("disabled backup was launched: %v, %v", running, err)
	}
	reconcile(ctx)
	if installation.jobEnabled(t) {
		t.Fatal("durable job stayed enabled after the lease was released")
	}
}
