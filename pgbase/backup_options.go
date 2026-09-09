package pgbase

import (
	"context"
	"errors"

	"github.com/webisnoteasy/PgBase/internal/api"
	"github.com/webisnoteasy/PgBase/internal/backup"
	"github.com/webisnoteasy/PgBase/internal/filestore"
)

// BackupOptions enables the web backup console in a private local directory.
// RunManaged supports restore activation while retaining the previous database.
// Embedders using New/Run may supply Activate to implement their own supervisor.
type BackupOptions struct {
	Directory string
	Activate  func(context.Context, RestoreTarget) error
	manager   *backup.Manager
}
type RestoreTarget = backup.Target

// ErrActivationOutcomeUnknown tells custom activation code to retain a target
// when it cannot prove whether an external supervisor already activated it.
var ErrActivationOutcomeUnknown = backup.ErrActivationOutcomeUnknown

func withBackupServices(dependencies api.Dependencies, files *filestore.Service, options Options) (api.Dependencies, *backup.Manager, error) {
	if options.Backups.manager != nil {
		dependencies.Backups = options.Backups.manager
		return dependencies, nil, nil
	}
	if options.Backups.Directory == "" {
		return dependencies, nil, nil
	}
	var objects backup.ObjectSource
	if files != nil {
		objects = files
	}
	manager, err := backup.NewManager(backup.ManagerOptions{
		Directory: options.Backups.Directory,
		LogError: func(ctx context.Context, err error) {
			options.Logger.ErrorContext(ctx, "backup operation failed", "error", err)
		},
		Create: func(ctx context.Context, destination string) error {
			return backup.CreateOnline(ctx, destination, options.DatabaseURL, objects)
		},
		Restore: func(ctx context.Context, directory string) error {
			if options.Backups.Activate == nil {
				return errors.New("restore activation requires RunManaged or BackupOptions.Activate")
			}
			target, err := backup.RestoreNew(ctx, directory, options.DatabaseURL, options.Backups.Directory+"/targets")
			if err != nil {
				return err
			}
			err = options.Backups.Activate(ctx, target)
			if err != nil && !errors.Is(err, ErrActivationOutcomeUnknown) {
				cleanup, cancel := context.WithTimeout(context.Background(), options.ShutdownTimeout)
				defer cancel()
				err = errors.Join(err, backup.DiscardTarget(cleanup, target))
			}
			return err
		},
	})
	if err != nil {
		return dependencies, nil, err
	}
	dependencies.Backups = manager
	return dependencies, manager, nil
}
