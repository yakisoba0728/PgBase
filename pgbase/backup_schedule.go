package pgbase

import (
	"context"
	"time"

	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/jobs"
	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
)

const managedBackupJobName = "pgbase.managed-backup"

// installManagedBackupSchedule gives the built-in backup lifecycle its own
// durable worker identity. It is registered once per process and configuration
// only updates the existing durable row, making repeated reloads idempotent.
func installManagedBackupSchedule(ctx context.Context, worker *jobs.Service, manager *backup.Manager, settings *runtimesettings.Controller, logError func(context.Context, error)) error {
	if worker == nil || manager == nil || settings == nil {
		return nil
	}
	if err := worker.Register(ctx, managedBackupJobName, "@every 24h", "UTC", func(ctx context.Context, _ jobs.Job) error { _, err := manager.Create(ctx); return err }); err != nil {
		return err
	}
	apply := func(value runtimesettings.Settings) {
		manager.SetRetention(value.Backups.Keep)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := configureManagedBackupSchedule(ctx, worker, value.Backups); err != nil && logError != nil {
			logError(ctx, err)
		}
	}
	apply(settings.Get())
	settings.Subscribe(apply)
	return nil
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
