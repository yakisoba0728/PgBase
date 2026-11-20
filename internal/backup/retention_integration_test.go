//go:build integration

package backup

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

type pauseRetentionList struct {
	Repository
	entered chan struct{}
	resume  chan struct{}
	first   sync.Once
}

func (r *pauseRetentionList) List(ctx context.Context) ([]Entry, error) {
	blocked := false
	r.first.Do(func() {
		close(r.entered)
		blocked = true
	})
	if !blocked {
		return r.Repository.List(ctx)
	}
	select {
	case <-r.resume:
		return r.Repository.List(ctx)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestPruneFencesRetentionChanges(t *testing.T) {
	for _, policy := range []string{"storage", "runtime", "concurrent"} {
		t.Run(policy, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			pool, err := database.Open(ctx, testutil.NewDatabase(t), 10*time.Second, true)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			if err := migrate.New(pool, nil).Up(ctx); err != nil {
				t.Fatal(err)
			}
			key := bytes.Repeat([]byte("r"), 32)
			runtimeSettings, err := runtimesettings.New(ctx, pool, key)
			if err != nil {
				t.Fatal(err)
			}
			updated := runtimeSettings.Get()
			if policy == "runtime" {
				updated.Backups.Keep = 1
				updated, err = runtimeSettings.Update(ctx, updated)
				if err != nil {
					t.Fatal(err)
				}
			}
			local := NewLocalRepository(t.TempDir())
			gate := &pauseRetentionList{Repository: local, entered: make(chan struct{}), resume: make(chan struct{})}
			directory := t.TempDir()
			controller, err := NewStorageController(ctx, pool, key, gate, StorageConfig{Driver: "local", Retention: 1})
			if err != nil {
				t.Fatal(err)
			}
			manager, err := NewManager(ManagerOptions{
				Directory:  directory,
				Repository: controller,
				Create:     func(context.Context, string) error { return nil },
				Restore:    func(context.Context, string) error { return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			revision := updated.Revision
			manager.SetRetention(updated.Backups.Keep, func(ctx context.Context, tx pgx.Tx) (bool, error) {
				return runtimeSettings.PinRevision(ctx, tx, revision)
			})
			var current, otherCurrent string
			for index := range 3 {
				entry := Entry{ID: uuid.NewString(), CreatedAt: time.Unix(int64(index+1), 0).UTC(), Size: 1}
				if err := controller.Publish(ctx, entry, bytes.NewReader([]byte("x"))); err != nil {
					t.Fatal(err)
				}
				otherCurrent, current = current, entry.ID
			}
			pruned := make(chan error, 1)
			go func() { pruned <- manager.prune(ctx, current) }()
			select {
			case <-gate.entered:
			case <-ctx.Done():
				t.Fatal("pruner did not read candidates")
			}

			kept := 3
			switch policy {
			case "runtime":
				// Use another controller so the pruning process retains its old snapshot.
				other, err := runtimesettings.New(ctx, pool, key)
				if err != nil {
					t.Fatal(err)
				}
				next := other.Get()
				next.Backups.Keep = 3
				if _, err := other.Update(ctx, next); err != nil {
					t.Fatal(err)
				}
				close(gate.resume)
			case "storage":
				saved := make(chan error, 1)
				go func() {
					_, err := controller.Update(ctx, StorageUpdate{StorageConfig: StorageConfig{Driver: "local", Retention: 3}, Revision: 1})
					saved <- err
				}()
				waitForStorageSettingsWriter(t, ctx, pool)
				close(gate.resume)
				if err := <-saved; err != nil {
					t.Fatal(err)
				}
			case "concurrent":
				// A second publisher has a different archive to protect. Running
				// both plans can otherwise remove even the retained archive.
				if err := manager.prune(ctx, otherCurrent); err != nil {
					t.Fatal(err)
				}
				kept = 1
				close(gate.resume)
			}
			if err := <-pruned; err != nil {
				t.Fatal(err)
			}
			entries, err := local.List(ctx)
			if err != nil || len(entries) != kept || kept == 1 && entries[0].ID != current {
				t.Fatalf("%s pruning: kept %v, wanted %d archives, err=%v", policy, entries, kept, err)
			}
		})
	}
}

func waitForStorageSettingsWriter(t *testing.T, ctx context.Context, pool *database.Pool) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	high, low := uint32(uint64(backupStorageLock)>>32), uint32(uint64(backupStorageLock)&0xffffffff)
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `select exists(select 1 from pg_locks
			where locktype='advisory' and classid=$1 and objid=$2 and mode='ExclusiveLock' and not granted
			and database=(select oid from pg_database where datname=current_database()))`, high, low).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("storage settings update did not queue behind the list")
		}
	}
}
