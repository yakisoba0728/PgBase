//go:build integration

package filestore

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestStagedOwnershipReleaseWaitsForServerUnlock(t *testing.T) {
	testStorageSessionRelease(t, false)
}

func TestStorageAdmissionReleaseWaitsForServerUnlock(t *testing.T) {
	testStorageSessionRelease(t, true)
}

func testStorageSessionRelease(t *testing.T, admission bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	url := testutil.NewDatabase(t)
	owner, cleanup, err := testutil.ConnectWithDelayedTermination(ctx, url)
	t.Cleanup(cleanup)
	if err != nil {
		t.Fatal(err)
	}
	observer, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observer.Close(context.Background()) })
	key := "objects/" + uuid.NewString()
	lock := stagedObjectLockName(key)
	acquire, attempt := `select pg_advisory_lock(hashtextextended($1,0))`, `select pg_try_advisory_lock(hashtextextended($1,0))`
	var lockValue any = lock
	if admission {
		acquire, attempt, lockValue = `select pg_advisory_lock_shared($1)`, `select pg_try_advisory_lock($1)`, storageTransitionLock
	}
	if _, err := owner.Exec(ctx, acquire, lockValue); err != nil {
		t.Fatal(err)
	}
	tryLock := func() bool {
		t.Helper()
		var acquired bool
		if err := observer.QueryRow(ctx, attempt, lockValue).Scan(&acquired); err != nil {
			t.Fatal(err)
		}
		return acquired
	}
	if tryLock() {
		t.Fatal("live staging owner did not protect its object")
	}
	if admission {
		_ = database.CloseCoordinationSession(ctx, owner)
	} else {
		(&stagedObjectOwnership{conn: owner, key: key, ownConn: true}).release()
	}
	if !tryLock() {
		t.Fatal("release returned before PostgreSQL acknowledged the staging unlock")
	}
}
