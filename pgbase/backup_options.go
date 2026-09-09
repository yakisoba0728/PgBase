package pgbase

import (
	"context"
	"errors"

	"github.com/webisnoteasy/PgBase/internal/api"
	"github.com/webisnoteasy/PgBase/internal/backup"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/filestore"
)

// BackupOptions enables the web backup console in a private local directory.
// RunManaged supports restore activation while retaining the previous database.
// Embedders using New/Run may supply Activate to implement their own supervisor.
type BackupOptions struct {
	Directory  string
	S3         BackupS3Options
	Retention  int
	Activate   func(context.Context, RestoreTarget) error
	manager    *backup.Manager
	controller *backup.StorageController
}
type BackupS3Options struct {
	Bucket, Prefix, Region, Endpoint, AccessKey, SecretKey string
	UsePathStyle                                           bool
}
type RestoreTarget = backup.Target

// ErrActivationOutcomeUnknown tells custom activation code to retain a target
// when it cannot prove whether an external supervisor already activated it.
var ErrActivationOutcomeUnknown = backup.ErrActivationOutcomeUnknown

func withBackupServices(ctx context.Context, dependencies api.Dependencies, files *filestore.Service, pool *database.Pool, options Options) (api.Dependencies, *backup.Manager, error) {
	if options.Backups.manager != nil {
		dependencies.Backups = options.Backups.manager
		dependencies.BackupStorageAdministration = options.Backups.controller
		return dependencies, nil, nil
	}
	if err := validateBackupOptions(options.Backups); err != nil {
		return dependencies, nil, err
	}
	if options.Backups.Directory == "" {
		return dependencies, nil, nil
	}
	var objects backup.ObjectSource
	if files != nil {
		objects = files
	}
	localRepository := backup.NewLocalRepository(options.Backups.Directory)
	controller, err := backup.NewStorageController(ctx, pool, options.TokenSecret, localRepository, backup.StorageConfig{Driver: func() string {
		if options.Backups.S3.Bucket != "" {
			return "s3"
		}
		return "local"
	}(), Bucket: options.Backups.S3.Bucket, Prefix: options.Backups.S3.Prefix, Region: options.Backups.S3.Region, Endpoint: options.Backups.S3.Endpoint, AccessKey: options.Backups.S3.AccessKey, SecretKey: options.Backups.S3.SecretKey, UsePathStyle: options.Backups.S3.UsePathStyle, Retention: options.Backups.Retention})
	if err != nil {
		return dependencies, nil, err
	}
	dependencies.BackupStorageAdministration = controller
	manager, err := backup.NewManager(backup.ManagerOptions{
		Directory:  options.Backups.Directory,
		Repository: controller,
		Retention:  options.Backups.Retention,
		Lifecycle:  backupLifecycle(options.Hooks),
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

func backupLifecycle(registry *HookRegistry) backup.Lifecycle {
	if registry == nil {
		return nil
	}
	return backupLifecycleHooks{registry}
}

func validateBackupOptions(options BackupOptions) error {
	configured := options.S3.Bucket != "" || options.S3.Prefix != "" || options.S3.Region != "" || options.S3.Endpoint != "" || options.S3.AccessKey != "" || options.S3.SecretKey != ""
	if configured && options.Directory == "" {
		return errors.New("backup directory is required for S3 backup receipts and scratch data")
	}
	if configured && (options.S3.Bucket == "" || options.S3.Region == "" || (options.S3.AccessKey == "") != (options.S3.SecretKey == "")) {
		return errors.New("backup S3 bucket, region, and paired credentials are required")
	}
	return nil
}
