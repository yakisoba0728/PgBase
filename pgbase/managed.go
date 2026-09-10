package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/migrate"
)

// RunManaged hosts an application behind a stable HTTP listener. Web restore
// builds and checks a fresh target, drains requests, swaps the app, and retains
// the previous target for recovery. An activation receipt survives restart.
func RunManaged(ctx context.Context, options Options) error {
	if err := validateBackupOptions(options.Backups); err != nil {
		return err
	}
	if options.Backups.Directory == "" {
		app, err := New(ctx, options)
		if err != nil {
			return err
		}
		return app.Run(ctx)
	}
	normalized, err := options.Normalized()
	if err != nil {
		return err
	}
	h := &managedHost{options: normalized}
	if err = h.loadTarget(); err != nil {
		return err
	}
	controllerPool, err := database.Open(ctx, h.options.DatabaseURL, h.options.StatementTimeout, h.options.AllowInsecureDatabase)
	if err != nil {
		return err
	}
	if normalized.AutoMigrate {
		runner := migrate.New(controllerPool, normalized.Logger)
		status, statusErr := runner.Status(ctx)
		if statusErr != nil {
			controllerPool.Close()
			return statusErr
		}
		if status.Current == 0 {
			if err = runner.Up(ctx); err != nil {
				controllerPool.Close()
				return err
			}
		}
	}
	controller, err := backup.NewStorageController(ctx, controllerPool, h.options.TokenSecret, backup.NewLocalRepository(h.options.Backups.Directory), backup.StorageConfig{Driver: func() string {
		if h.options.Backups.S3.Bucket != "" {
			return "s3"
		}
		return "local"
	}(), Bucket: h.options.Backups.S3.Bucket, Prefix: h.options.Backups.S3.Prefix, Region: h.options.Backups.S3.Region, Endpoint: h.options.Backups.S3.Endpoint, AccessKey: h.options.Backups.S3.AccessKey, SecretKey: h.options.Backups.S3.SecretKey, UsePathStyle: h.options.Backups.S3.UsePathStyle, Retention: h.options.Backups.Retention})
	if err != nil {
		controllerPool.Close()
		return err
	}
	h.controller, h.controllerPool = controller, controllerPool
	alerter := newBackupFailureAlerter(controllerPool, normalized)
	manager, err := backup.NewManager(backup.ManagerOptions{Directory: normalized.Backups.Directory, Repository: controller, Retention: normalized.Backups.Retention, Lifecycle: backupLifecycle(normalized.Hooks), LogError: func(ctx context.Context, err error) {
		normalized.Logger.ErrorContext(ctx, "backup operation failed", "error", err)
	}, OnFailure: func(ctx context.Context, job backup.Job) { alerter.Notify(ctx, job) }, Create: h.createBackup, Restore: h.restoreBackup})
	if err != nil {
		controllerPool.Close()
		return err
	}
	h.options.Backups.manager = manager
	h.options.Backups.controller = controller
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), normalized.ShutdownTimeout)
		defer cancel()
		_ = manager.Shutdown(shutdown)
		h.controllerPool.Close()
	}()
	app, err := New(ctx, h.options)
	if err != nil {
		return err
	}
	if err = app.Start(context.WithoutCancel(ctx)); err != nil {
		_ = app.Close(context.Background())
		return err
	}
	h.app = app
	listener, err := net.Listen("tcp", normalized.HTTPAddress)
	if err != nil {
		_ = app.Close(context.Background())
		return err
	}
	server := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	err = h.wait(ctx, served)
	shutdown, cancel := context.WithTimeout(context.Background(), normalized.ShutdownTimeout)
	defer cancel()
	err = errors.Join(err, manager.Shutdown(shutdown), server.Shutdown(shutdown))
	h.mu.Lock()
	current := h.app
	h.maintenance = true
	h.mu.Unlock()
	return errors.Join(err, current.Close(shutdown))
}

type managedHost struct {
	options        Options
	mu             sync.Mutex
	app            *App
	active         int
	maintenance    bool
	drained        chan struct{}
	controller     *backup.StorageController
	controllerPool *database.Pool
}

func (h *managedHost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	if h.maintenance {
		h.mu.Unlock()
		w.Header().Set("Retry-After", "2")
		http.Error(w, "Restore activation in progress. Retry shortly.", http.StatusServiceUnavailable)
		return
	}
	app := h.app
	h.active++
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.active--
		if h.active == 0 && h.drained != nil {
			close(h.drained)
			h.drained = nil
		}
		h.mu.Unlock()
	}()
	app.Handler().ServeHTTP(w, r)
}
func (h *managedHost) createBackup(ctx context.Context, destination string) error {
	h.mu.Lock()
	app := h.app
	h.mu.Unlock()
	var source backup.ObjectSource
	if files, ok := app.fileWorker.(*filestore.Service); ok {
		source = files
	}
	return backup.CreateOnline(ctx, destination, app.options.DatabaseURL, source)
}
func (h *managedHost) restoreBackup(ctx context.Context, directory string) (resultErr error) {
	h.mu.Lock()
	options := h.options
	h.mu.Unlock()
	target, err := backup.RestoreNew(ctx, directory, options.DatabaseURL, filepath.Join(options.Backups.Directory, "targets"))
	if err != nil {
		return err
	}
	targetPool, err := database.Open(ctx, target.DatabaseURL, options.StatementTimeout, options.AllowInsecureDatabase)
	if err != nil {
		return err
	}
	targetPoolAdopted := false
	defer func() {
		if !targetPoolAdopted {
			targetPool.Close()
		}
	}()
	if err = filestore.ResetRestoredStorage(ctx, targetPool); err != nil {
		return err
	}
	if _, err = targetPool.Exec(ctx, `delete from pgbase_meta.backup_storage_operations`); err != nil {
		return err
	}
	activated := false
	defer func() {
		if activated || h.targetReferenced(target) {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), options.ShutdownTimeout)
		defer cancel()
		resultErr = errors.Join(resultErr, backup.DiscardTarget(cleanup, target))
	}()
	nextOptions := options
	nextOptions.DatabaseURL = target.DatabaseURL
	nextOptions.Files = restoredFileOptions(options.Files, target.FilesDirectory)
	// Build and verify all migrations/options before pausing the old target.
	if err = backup.Progress(ctx, "verifying_application"); err != nil {
		return err
	}
	candidate, err := New(ctx, nextOptions)
	if err != nil {
		return err
	}
	started := false
	defer func() {
		if !started {
			closeCtx, cancel := context.WithTimeout(context.Background(), options.ShutdownTimeout)
			defer cancel()
			_ = candidate.Close(closeCtx)
		}
	}()
	if err = backup.Progress(ctx, "draining_requests"); err != nil {
		return err
	}
	h.mu.Lock()
	h.maintenance = true
	drained := make(chan struct{})
	if h.active == 0 {
		close(drained)
	} else {
		h.drained = drained
	}
	old := h.app
	h.mu.Unlock()
	defer func() { h.mu.Lock(); h.maintenance = false; h.mu.Unlock() }()
	drainCtx, cancel := context.WithTimeout(ctx, options.ShutdownTimeout)
	defer cancel()
	select {
	case <-drained:
	case <-drainCtx.Done():
		return errors.New("active requests did not drain before restore activation")
	}
	// Start the isolated candidate before stopping the old app. No HTTP requests
	// are served in this short window, and failure leaves the original app usable.
	if err = candidate.Start(context.WithoutCancel(ctx)); err != nil {
		return err
	}
	if err = backup.Progress(ctx, "activating_target"); err != nil {
		return err
	}
	nextControllerPool := targetPool
	if err = h.controller.ValidatePool(ctx, nextControllerPool); err != nil {
		nextControllerPool.Close()
		return err
	}
	if err = h.persistTarget(target); err != nil {
		nextControllerPool.Close()
		return err
	}
	previousControllerPool := h.controllerPool
	h.controller.RebindPool(nextControllerPool)
	h.controllerPool = nextControllerPool
	targetPoolAdopted = true
	h.mu.Lock()
	h.app = candidate
	h.options = nextOptions
	h.mu.Unlock()
	started = true
	activated = true
	closeCtx, closeCancel := context.WithTimeout(context.Background(), options.ShutdownTimeout)
	defer closeCancel()
	if err = old.Close(closeCtx); err != nil {
		options.Logger.Error("restored target activated; previous app shutdown failed", "error", err)
	}
	previousControllerPool.Close()
	return nil
}
func restoredFileOptions(previous FileOptions, directory string) FileOptions {
	previous.Driver = "local"
	previous.LocalDirectory = directory
	previous.S3Bucket = ""
	previous.S3Region = ""
	previous.S3Endpoint = ""
	previous.S3AccessKey = ""
	previous.S3SecretKey = ""
	previous.S3UsePathStyle = false
	return previous
}
func (h *managedHost) targetPath() string {
	return filepath.Join(h.options.Backups.Directory, "active-target.json")
}
func (h *managedHost) loadTarget() error {
	raw, err := os.ReadFile(h.targetPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var target RestoreTarget
	if json.Unmarshal(raw, &target) != nil || target.DatabaseURL == "" || target.FilesDirectory == "" {
		return errors.New("invalid private restore activation receipt")
	}
	h.options.DatabaseURL = target.DatabaseURL
	h.options.Files = restoredFileOptions(h.options.Files, target.FilesDirectory)
	return nil
}
func (h *managedHost) persistTarget(target RestoreTarget) error {
	raw, err := json.Marshal(target)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(h.options.Backups.Directory, ".activation-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	if err = file.Chmod(0600); err != nil {
		_ = file.Close()
		return err
	}
	_, err = file.Write(raw)
	err = errors.Join(err, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	if err = backup.DurableRename(name, h.targetPath()); err != nil {
		return fmt.Errorf("persist restore activation: %w", err)
	}
	return nil
}

func (h *managedHost) wait(ctx context.Context, served <-chan error) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-served:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-ticker.C:
			// Keep selection and the nonblocking health read under the host lock.
			// Otherwise activation can swap/close an old app after selection and
			// its expected shutdown would be mistaken for a new target failure.
			h.mu.Lock()
			app := h.app
			if h.maintenance {
				h.mu.Unlock()
				continue
			}
			app.closeMu.Lock()
			select {
			case <-app.backgroundDone:
				err := app.backgroundErr
				app.closeMu.Unlock()
				h.mu.Unlock()
				if err == nil {
					err = errors.New("application background work stopped")
				}
				return err
			default:
				app.closeMu.Unlock()
				h.mu.Unlock()
			}
		}
	}
}

// A failed sync after rename can leave a visible activation receipt. Preserve
// both targets in that indeterminate case; deleting its target would make a
// subsequent restart point at a database we just destroyed.
func (h *managedHost) targetReferenced(target RestoreTarget) bool {
	raw, err := os.ReadFile(h.targetPath())
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		return true
	}
	var saved RestoreTarget
	if json.Unmarshal(raw, &saved) != nil {
		return true
	}
	return saved.DatabaseURL == target.DatabaseURL
}
