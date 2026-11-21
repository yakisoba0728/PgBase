//go:build integration

package filestore

import (
	"bytes"
	"context"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

// A storage lease's coordination connection is also used by its owner, for
// example to take and release staged-object locks (staging.go). Canceling the
// request must never make a second goroutine write to that *pgx.Conn while the
// owner is using it: pgconn is not safe for concurrent use and panics with
// "BUG: slow write timer already active" outside any HTTP recover.
func TestStorageLeaseCancellationNeverSharesConnectionWithOwner(t *testing.T) {
	ctx := t.Context()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	controller, err := NewStorageController(ctx, pool, bytes.Repeat([]byte{9}, 32), StorageConfig{Driver: "local", LocalDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for iteration := range 150 {
		operationCtx, cancel := context.WithCancel(ctx)
		leased, release, err := controller.BeginStorageOperation(operationCtx)
		if err != nil {
			cancel()
			t.Fatalf("iteration %d: %v", iteration, err)
		}
		lease := leased.Value(storageLeaseContextKey{}).(storageLease)
		var owner sync.WaitGroup
		owner.Go(func() {
			// Mirror reserveObject and stagedObjectOwnership.release, whose
			// cleanup deliberately outlives the request context.
			for range 1000 {
				ownership := &stagedObjectOwnership{conn: lease.conn, key: "objects/" + uuid.NewString()}
				_, lockErr := lease.conn.Exec(context.Background(), `select pg_advisory_lock(hashtextextended($1,0))`, stagedObjectLockName(ownership.key))
				ownership.release()
				if lockErr != nil {
					return
				}
			}
		})
		time.Sleep(time.Duration(rand.IntN(3000)) * time.Microsecond)
		cancel()
		owner.Wait()
		if iteration%2 == 0 {
			release()
		} else {
			// Race the owner's release against the cancellation callback.
			var both sync.WaitGroup
			both.Go(release)
			both.Go(release)
			both.Wait()
		}
		if active := len(controller.operations); active != 0 {
			t.Fatalf("iteration %d: released operation retained %d admission slots", iteration, active)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			var locked bool
			if err := pool.QueryRow(ctx, `select exists(select 1 from pg_locks where locktype='advisory' and pid=$1 and granted)`, int32(lease.backend)).Scan(&locked); err != nil {
				t.Fatal(err)
			}
			if !locked {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("iteration %d: canceled lease kept its advisory locks", iteration)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}
