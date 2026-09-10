package pgbase

import (
	"context"

	"github.com/yakisoba0728/PgBase/internal/backup"
)

const (
	HookBackupCreate  = "backup.create"
	HookBackupRestore = "backup.restore"
	HookBackupDelete  = "backup.delete"
)

type BackupEvent = backup.LifecycleEvent

type backupLifecycleHooks struct{ registry *HookRegistry }

func (h backupLifecycleHooks) BeginBackupOperation(ctx context.Context, event *backup.LifecycleEvent) (func(error), error) {
	kind := HookBackupCreate
	switch event.Operation {
	case "restore":
		kind = HookBackupRestore
	case "delete":
		kind = HookBackupDelete
	}
	return h.registry.Begin(ctx, &HookEvent{Kind: kind, Data: event})
}
