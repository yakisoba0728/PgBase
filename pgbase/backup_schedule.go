package pgbase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/jobs"
	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
)

const managedBackupJobName = "pgbase.managed-backup"

// installManagedBackupSchedule gives the built-in backup lifecycle its own
// durable worker identity. It is registered once per process and configuration
// only updates the existing durable row, making repeated reloads idempotent.
//
// Retention precedence: a positive backups.keep wins; zero falls back to the
// Backup storage retention (seeded once from PGBASE_BACKUPS_RETENTION), where
// zero keeps every archive.
func installManagedBackupSchedule(ctx context.Context, worker *jobs.Service, manager *backup.Manager, settings *runtimesettings.Controller, logger *slog.Logger) error {
	if worker == nil || manager == nil || settings == nil {
		return nil
	}
	if err := adoptInertBackupSettings(ctx, worker, settings, logger); err != nil {
		return err
	}
	if err := worker.Register(ctx, managedBackupJobName, "@every 24h", "UTC", false, func(ctx context.Context, _ jobs.Job) error {
		// Another instance may have changed keep since this process loaded it.
		if err := settings.Reload(ctx); err != nil {
			return err
		}
		_, err := manager.CreateManaged(ctx)
		return err
	}); err != nil {
		return err
	}
	var applied *runtimesettings.Backups
	apply := func(value runtimesettings.Settings) {
		manager.SetRetention(value.Backups.Keep)
		if applied != nil && *applied == value.Backups {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := configureManagedBackupSchedule(ctx, worker, value.Backups); err != nil {
			logger.ErrorContext(ctx, "reconfigure managed backup schedule", "error", err)
			return
		}
		applied = &value.Backups
	}
	apply(settings.Get())
	settings.Subscribe(apply)
	return nil
}

// adoptInertBackupSettings runs while the durable job row does not exist yet.
// Before this release `pgbase serve` never registered the job, so a stored
// backups.enabled or backups.keep had no effect there. Clearing them once
// keeps an upgrade from starting scheduled backups or pruning archives that
// the Backup storage retention would have kept.
func adoptInertBackupSettings(ctx context.Context, worker *jobs.Service, settings *runtimesettings.Controller, logger *slog.Logger) error {
	items, err := worker.ListJobs(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Name == managedBackupJobName {
			return nil
		}
	}
	for range 3 {
		value := settings.Get()
		if !value.Backups.Enabled && value.Backups.Keep == 0 {
			return nil
		}
		logger.WarnContext(ctx, "managed backup settings were stored but never applied; turning scheduled backups off and deferring retention to the Backup storage page", "enabled", value.Backups.Enabled, "keep", value.Backups.Keep)
		value.Backups.Enabled, value.Backups.Keep = false, 0
		if _, err = settings.Update(ctx, value); !errors.Is(err, runtimesettings.ErrConflict) {
			return err
		}
		if err = settings.Reload(ctx); err != nil {
			return err
		}
	}
	return runtimesettings.ErrConflict
}

func configureManagedBackupSchedule(ctx context.Context, worker *jobs.Service, value runtimesettings.Backups) error {
	items, err := worker.ListJobs(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Name == managedBackupJobName {
			return worker.ConfigureJobInTimezone(ctx, item.ID, value.Schedule, value.Timezone, value.Enabled)
		}
	}
	return jobs.ErrNotManageable
}
