//go:build integration

package migrate

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestVariantCleanupUpgradePreservesQueueAndReapplies(t *testing.T) {
	pool, err := database.Open(t.Context(), testutil.NewDatabase(t), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	legacy := New(pool, nil)
	for index, migration := range legacy.migrations {
		if migration.Version == 69 {
			legacy.migrations = legacy.migrations[:index]
			break
		}
	}
	if legacy.migrations[len(legacy.migrations)-1].Version != 68 {
		t.Fatal("fixture must start at schema68")
	}
	if err := legacy.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	key := "thumbnails/" + uuid.NewString()
	if _, err := pool.Exec(t.Context(), `insert into pgbase_meta.file_object_deletions(object_key,queued_at) values($1,current_timestamp)`, key); err != nil {
		t.Fatal(err)
	}
	runner := New(pool, nil)
	for range 2 {
		if err := runner.Up(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	status, err := runner.Status(t.Context())
	if err != nil || status.Current != 72 || status.Latest != 72 || status.Dirty || !status.Compatible {
		t.Fatalf("migration status=%+v err=%v", status, err)
	}
	var retained bool
	var attempts int
	var failure string
	if err := pool.QueryRow(t.Context(), `select retain_on_success,attempts,last_error from pgbase_meta.file_object_deletions where object_key=$1`, key).Scan(&retained, &attempts, &failure); err != nil || retained || attempts != 0 || failure != "" {
		t.Fatalf("legacy queue changed: retained=%t attempts=%d failure=%q err=%v", retained, attempts, failure, err)
	}
	if _, err := pool.Exec(t.Context(), `delete from pgbase_meta.file_object_deletions where object_key=$1`, key); err != nil {
		t.Fatalf("legacy ordinary deletion blocked: %v", err)
	}
}
