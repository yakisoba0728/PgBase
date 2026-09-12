package pgbase

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/database"
)

func TestManagedRestoreDiscardsProvisionedTargetWhenOpeningFails(t *testing.T) {
	openErr := errors.New("open restored database")
	target := backup.Target{DatabaseURL: "postgres://restored", FilesDirectory: filepath.Join(t.TempDir(), "files")}
	var discarded backup.Target
	discardedCalls := 0
	host := &managedHost{
		options: Options{Backups: BackupOptions{Directory: t.TempDir()}, ShutdownTimeout: time.Second},
		restore: managedRestoreDependencies{
			restoreNew:   func(context.Context, string, string, string) (backup.Target, error) { return target, nil },
			openDatabase: func(context.Context, string, time.Duration, bool) (*database.Pool, error) { return nil, openErr },
			discard: func(_ context.Context, got backup.Target) error {
				discardedCalls++
				discarded = got
				return nil
			},
		},
	}

	err := host.restoreBackup(context.Background(), "backup")
	if !errors.Is(err, openErr) {
		t.Fatalf("restore error = %v, want opening error", err)
	}
	if discardedCalls != 1 || discarded != target {
		t.Fatalf("discard calls=%d target=%#v, want one discard of %#v", discardedCalls, discarded, target)
	}
}
