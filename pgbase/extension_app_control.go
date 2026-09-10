package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/migrate"
)

type extensionAppControl struct {
	app        *App
	dataDir    string
	encryptEnv string
	dev        bool
	restart    func(context.Context) error
	mu         sync.RWMutex
	store      map[string]json.RawMessage
}

func newExtensionAppControl(app *App, options *JavaScriptOptions) *extensionAppControl {
	control := &extensionAppControl{app: app, store: map[string]json.RawMessage{}}
	if options != nil {
		control.dataDir = options.PocketBaseCompatibility.DataDirectory
		control.encryptEnv = options.PocketBaseCompatibility.EncryptionEnvironment
		control.dev = options.PocketBaseCompatibility.Development
		control.restart = options.PocketBaseCompatibility.ProcessRestart
	}
	return control
}

func (host *extensionHost) callAppControl(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	if host.principal.Kind != identity.Administrator {
		return nil, extensionError("app_control_denied", errors.New("administrator authority is required"))
	}
	control := host.bridge.appControl
	if control == nil || control.app == nil {
		return nil, extensionError("app_control_unavailable", errors.New("application control is unavailable"))
	}
	switch method {
	case "app-control.isBootstrapped":
		status, err := migrate.New(control.app.pool, control.app.options.Logger).Status(ctx)
		if err != nil {
			return nil, extensionError("app_control_failed", err)
		}
		return json.Marshal(status.Current == status.Latest && status.Latest > 0 && !status.Dirty)
	case "app-control.dataDir":
		return json.Marshal(control.dataDir)
	case "app-control.encryptionEnv":
		return json.Marshal(control.encryptEnv)
	case "app-control.isDev":
		return json.Marshal(control.dev)
	case "app-control.settings":
		return json.Marshal(control.app.runtimeSettings.Get())
	case "app-control.reloadSettings":
		if err := control.app.runtimeSettings.Reload(ctx); err != nil {
			return nil, extensionError("settings_reload_failed", err)
		}
		return json.Marshal(control.app.runtimeSettings.Get())
	case "app-control.storeGet", "app-control.storeSet", "app-control.storeDelete":
		return control.callStore(method, raw)
	case "app-control.createBackup":
		if control.app.backupManager == nil {
			return nil, extensionError("backup_unavailable", errors.New("backup manager is unavailable"))
		}
		job, err := control.app.backupManager.Create(ctx)
		if err != nil {
			return nil, extensionError("backup_create_failed", err)
		}
		return json.Marshal(job)
	case "app-control.restoreBackup":
		if control.app.backupManager == nil {
			return nil, extensionError("backup_unavailable", errors.New("backup manager is unavailable"))
		}
		var request struct {
			ID string `json:"id"`
		}
		if err := decodeExtensionInput(raw, &request); err != nil || request.ID == "" {
			return nil, extensionError("invalid_backup", err)
		}
		job, err := control.app.backupManager.Restore(ctx, request.ID)
		if err != nil {
			return nil, extensionError("backup_restore_failed", err)
		}
		return json.Marshal(job)
	case "app-control.restart":
		if control.restart == nil {
			return nil, extensionError("restart_unavailable", errors.New("operator restart callback is unavailable"))
		}
		if err := control.restart(ctx); err != nil {
			return nil, extensionError("restart_failed", err)
		}
		return json.RawMessage(`{"accepted":true}`), nil
	case "app-control.runSystemMigrations":
		if err := migrate.New(control.app.pool, control.app.options.Logger).Up(ctx); err != nil {
			return nil, extensionError("system_migration_failed", err)
		}
		return json.RawMessage(`{"applied":true}`), nil
	case "app-control.runAppMigrations":
		results, err := control.runAppMigrations(ctx)
		if err != nil {
			return nil, extensionError("app_migration_failed", err)
		}
		return json.Marshal(results)
	case "app-control.runAllMigrations":
		if err := migrate.New(control.app.pool, control.app.options.Logger).Up(ctx); err != nil {
			return nil, extensionError("system_migration_failed", err)
		}
		results, err := control.runAppMigrations(ctx)
		if err != nil {
			return nil, extensionError("app_migration_failed", err)
		}
		return json.Marshal(results)
	case "app-control.transactionInfo":
		return json.RawMessage(`{"transactional":false}`), nil
	default:
		return nil, extensionError("unsupported_app_control", errors.New("unsupported app control operation"))
	}
}

func (control *extensionAppControl) runAppMigrations(ctx context.Context) ([]UserMigrationResult, error) {
	migrations, err := control.app.JavaScriptMigrations()
	if err != nil {
		return nil, err
	}
	runner, err := NewUserMigrationRunner(control.app, migrations)
	if err != nil {
		return nil, err
	}
	return runner.Up(ctx)
}

func (control *extensionAppControl) callStore(method string, raw json.RawMessage) (json.RawMessage, error) {
	var request struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	if err := decodeExtensionInput(raw, &request); err != nil || request.Key == "" || len(request.Key) > 256 {
		return nil, extensionError("invalid_store_key", err)
	}
	switch method {
	case "app-control.storeGet":
		control.mu.RLock()
		value, ok := control.store[request.Key]
		control.mu.RUnlock()
		if !ok {
			return json.RawMessage(`null`), nil
		}
		return append(json.RawMessage(nil), value...), nil
	case "app-control.storeSet":
		if len(request.Value) == 0 || !json.Valid(request.Value) {
			return nil, extensionError("invalid_store_value", nil)
		}
		control.mu.Lock()
		control.store[request.Key] = append(json.RawMessage(nil), request.Value...)
		control.mu.Unlock()
		return append(json.RawMessage(nil), request.Value...), nil
	case "app-control.storeDelete":
		control.mu.Lock()
		_, ok := control.store[request.Key]
		delete(control.store, request.Key)
		control.mu.Unlock()
		return json.Marshal(ok)
	}
	return nil, extensionError("unsupported_app_control", nil)
}
