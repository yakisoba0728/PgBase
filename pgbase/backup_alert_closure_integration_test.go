//go:build integration

package pgbase

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/mailer"
)

type closureAlertSender struct {
	messages   []mailer.Message
	beforeSend func()
}

func (s *closureAlertSender) Send(_ context.Context, message mailer.Message) error {
	if s.beforeSend != nil {
		s.beforeSend()
	}
	s.messages = append(s.messages, message)
	return nil
}

func TestManagedRestoreRebindsScheduledBackupAlertRecipients(t *testing.T) {
	fixture := newPublicUsersApp(t, false)
	setupPublicUsersAdministrator(t, fixture)
	options := fixture.app.options
	options.Backups.Directory = t.TempDir()
	controller, err := backup.NewStorageController(t.Context(), fixture.app.pool, options.TokenSecret, backup.NewLocalRepository(options.Backups.Directory), backup.StorageConfig{Driver: "local"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot")
	if err := backup.CreateOnline(t.Context(), snapshot, options.DatabaseURL, nil); err != nil {
		t.Fatal(err)
	}
	sender := &closureAlertSender{}
	alerter := &backupFailureAlerter{pool: fixture.app.pool, sender: sender, now: time.Now}
	sender.beforeSend = func() {
		if !alerter.mu.TryLock() {
			t.Fatal("recipient pool mutex held during SMTP")
		}
		alerter.mu.Unlock()
	}

	host := &managedHost{options: options, app: fixture.app, controller: controller, controllerPool: fixture.app.pool, backupAlerter: alerter}
	var restoredTarget backup.Target
	host.restore.restoreNew = func(ctx context.Context, directory, url, root string) (backup.Target, error) {
		target, err := backup.RestoreNew(ctx, directory, url, root)
		restoredTarget = target
		return target, err
	}
	if err := host.restoreBackup(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = host.app.Close(context.Background())
		host.controllerPool.Close()
		_ = backup.DiscardTarget(context.Background(), restoredTarget)
	})
	if _, err := host.controllerPool.Exec(t.Context(), `update pgbase_meta.admins set email='restored-alert@example.test' where active`); err != nil {
		t.Fatal(err)
	}
	alerter.Notify(t.Context(), backup.Job{Managed: true, Status: "failed", UpdatedAt: time.Now()})
	if len(sender.messages) != 1 || sender.messages[0].To != "restored-alert@example.test" {
		t.Fatalf("post-restore alert messages=%v", sender.messages)
	}
}
