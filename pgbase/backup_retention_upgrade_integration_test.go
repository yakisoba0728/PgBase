//go:build integration

package pgbase

import (
	"bytes"
	"context"
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
	return retentionInstallation{pool: pool, settings: settings, manager: manager, storage: storage, worker: jobs.NewService(pool, jobs.NewRegistry(), slog.New(slog.NewTextHandler(io.Discard, nil)), uuid.New())}
}

func (i retentionInstallation) install(t *testing.T) {
	t.Helper()
	if err := installManagedBackupSchedule(context.Background(), i.worker, i.manager, i.settings, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
}

func (i retentionInstallation) backupAndCount(t *testing.T) int {
	t.Helper()
	ctx := context.Background()
	job, err := i.manager.CreateManaged(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, i.manager, job.ID)
	entries, err := i.storage.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func waitBackupJob(t *testing.T, manager *backup.Manager, id string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		job, err := manager.Job(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status != "running" {
			if job.Status != "completed" {
				t.Fatalf("backup job = %#v", job)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("backup job never finished")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (i retentionInstallation) jobEnabled(t *testing.T) bool {
	t.Helper()
	var enabled bool
	if err := i.pool.QueryRow(context.Background(), `SELECT enabled FROM pgbase_meta.scheduled_jobs WHERE name=$1`, managedBackupJobName).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	return enabled
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
	second := jobs.NewService(installation.pool, jobs.NewRegistry(), nil, uuid.New())
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
	if err != nil || len(running) != 1 {
		t.Fatalf("running = %#v %v", running, err)
	}
	waitBackupJob(t, installation.manager, running[0].ID)
	entries, err := installation.storage.List(ctx)
	if err != nil || len(entries) != 1 {
		t.Fatalf("kept %d archives, want 1 (%v)", len(entries), err)
	}
}
