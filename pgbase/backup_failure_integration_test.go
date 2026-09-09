//go:build integration

package pgbase

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/backup"
)

func TestFailedBackupActivationDiscardsOnlyOwnedTarget(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "known_failure", true: "uncertain"}[uncertain], func(t *testing.T) {
			captured := make(chan RestoreTarget, 1)
			fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
				options.Backups = BackupOptions{Directory: t.TempDir(), Activate: func(_ context.Context, target RestoreTarget) error {
					captured <- target
					if uncertain {
						return ErrActivationOutcomeUnknown
					}
					return context.Canceled
				}}
			})
			manager := fixture.app.backupManager
			creation, err := manager.Create(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			waitBackupState(t, manager, creation.ID, "completed")
			restore, err := manager.Restore(context.Background(), creation.BackupID)
			if err != nil {
				t.Fatal(err)
			}
			waitBackupState(t, manager, restore.ID, "failed")
			var target RestoreTarget
			select {
			case target = <-captured:
			case <-time.After(time.Second):
				t.Fatal("activation callback not invoked")
			}
			parsed, err := url.Parse(target.DatabaseURL)
			if err != nil {
				t.Fatal(err)
			}
			var exists bool
			if err = fixture.app.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)`, strings.TrimPrefix(parsed.Path, "/")).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if exists != uncertain {
				t.Fatalf("restored database existence=%v uncertain=%v", exists, uncertain)
			}
			_, err = os.Stat(filepath.Dir(target.FilesDirectory))
			if !uncertain && !os.IsNotExist(err) {
				t.Fatalf("failed target directory retained: %v", err)
			}
			if uncertain {
				if err = backup.DiscardTarget(context.Background(), target); err != nil {
					t.Fatal(err)
				}
			}
			assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "GET", "/health/ready", nil, "", nil, nil), 200)
		})
	}
}
func waitBackupState(t *testing.T, manager *backup.Manager, id, want string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		job, err := manager.Job(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status != "running" {
			if job.Status != want {
				t.Fatalf("job=%+v want=%s", job, want)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("backup job timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
