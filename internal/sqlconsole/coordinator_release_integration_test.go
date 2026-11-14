//go:build integration

package sqlconsole

import (
	"context"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestCoordinatorCloseAcknowledgesLeadershipHandoff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	url := testutil.NewDatabase(t)
	pool, err := database.Open(ctx, url, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	owner, cleanup, err := testutil.ConnectWithDelayedTermination(ctx, url)
	t.Cleanup(cleanup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `select pg_advisory_lock($1)`, coordinatorLockKey); err != nil {
		t.Fatal(err)
	}
	coordinator := &Coordinator{connection: owner, pool: pool, pid: owner.PgConn().PID()}
	coordinator.Close()
	ledger, err := NewLedger(pool)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := ledger.AcquireCoordinator(ctx)
	if err != nil {
		t.Fatalf("closed coordinator still owns leadership: %v", err)
	}
	replacement.Close()
}
