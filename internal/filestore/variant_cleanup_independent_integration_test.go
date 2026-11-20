//go:build integration

package filestore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

// Losing database ownership does not imply that an in-flight remote Put has
// finished. The uncertain key remains retryable even after successful Delete.
func TestVariantRetainedIntentSurvivesLostSessionsAndLatePut(t *testing.T) {
	ctx := t.Context()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Up(ctx); err != nil {
		t.Fatal(err)
	}
	local, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{pool: pool, objects: local}
	key := "thumbnails/" + uuid.NewString()
	owner, err := service.reserveObject(ctx, key, true)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.release()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := lockObjectReference(ctx, tx, key); err != nil {
		t.Fatal(err)
	}
	var publisherPID int32
	if err := tx.QueryRow(ctx, "select pg_backend_pid()").Scan(&publisherPID); err != nil {
		t.Fatal(err)
	}
	// Before losing either lock, the pre-registered intent is visible but not
	// drainable. No bytes have reached their final object key yet.
	if worked, err := service.DrainOne(ctx); err != nil || worked {
		t.Fatalf("live upload drained=%v err=%v", worked, err)
	}
	for _, pid := range []int32{int32(owner.conn.PgConn().PID()), publisherPID} {
		var terminated bool
		if err := pool.QueryRow(ctx, "select pg_terminate_backend($1,5000)", pid).Scan(&terminated); err != nil || !terminated {
			t.Fatalf("terminate pid=%d ok=%v err=%v", pid, terminated, err)
		}
	}
	_ = tx.Rollback(ctx)
	// A cleaner can now observe no reference. Delete on a nonexistent key is
	// successful, but must not discard all durable knowledge of an unknown Put.
	if worked, err := service.DrainOne(ctx); err != nil || !worked {
		t.Fatalf("crash cleanup worked=%v err=%v", worked, err)
	}
	if err := local.Put(ctx, key, bytes.NewBufferString("late object-store completion")); err != nil {
		t.Fatal(err)
	}
	var tracked bool
	if err := pool.QueryRow(ctx, "select exists(select 1 from pgbase_meta.file_object_deletions where object_key=$1)", key).Scan(&tracked); err != nil {
		t.Fatal(err)
	}
	reader, err := local.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	if !tracked {
		t.Fatal("late Put left real bytes without a live reference or durable deletion intent")
	}
	var retained, deferred bool
	var attempts int
	if err := pool.QueryRow(ctx, `select retain_on_success,attempts,next_attempt_at>clock_timestamp() from pgbase_meta.file_object_deletions where object_key=$1`, key).Scan(&retained, &attempts, &deferred); err != nil {
		t.Fatal(err)
	}
	if !retained || !deferred || attempts != 1 {
		t.Fatalf("uncertain cleanup state retained=%v deferred=%v attempts=%d", retained, deferred, attempts)
	}
	if worked, err := service.DrainOne(ctx); err != nil || worked {
		t.Fatalf("uncertain intent hot-looped before retry: worked=%v err=%v", worked, err)
	}
	// Advance the persisted retry schedule without a wall-clock sleep; a fresh
	// service must delete the late bytes and retain the still-uncertain intent.
	if _, err := pool.Exec(ctx, `update pgbase_meta.file_object_deletions set next_attempt_at=clock_timestamp() where object_key=$1`, key); err != nil {
		t.Fatal(err)
	}
	restartedStore, err := NewLocalStore(local.root)
	if err != nil {
		t.Fatal(err)
	}
	restartedService := &Service{pool: pool, objects: restartedStore}
	if worked, err := restartedService.DrainOne(ctx); err != nil || !worked {
		t.Fatalf("retry did not remove late bytes: worked=%v err=%v", worked, err)
	}
	if reader, err := local.Open(ctx, key); !errors.Is(err, ErrNotFound) {
		if reader != nil {
			_ = reader.Close()
		}
		t.Fatalf("late object survived physical deletion: %v", err)
	}
	if err := pool.QueryRow(ctx, `select retain_on_success,attempts from pgbase_meta.file_object_deletions where object_key=$1`, key).Scan(&retained, &attempts); err != nil || !retained || attempts != 2 {
		t.Fatalf("retry lost uncertainty: retained=%v attempts=%d err=%v", retained, attempts, err)
	}
}

func TestVariantUncertainIntentProtectsLegacyWorkerAndMigrationSource(t *testing.T) {
	ctx, pool, controller, migrationID, _, _, _ := newLocalMigrationFixture(t, nil)
	key := "thumbnails/" + uuid.NewString()
	service := &Service{pool: pool}
	owner, err := service.reserveObject(ctx, key, true)
	if err != nil {
		t.Fatal(err)
	}
	owner.release()
	_, err = pool.Exec(ctx, `delete from pgbase_meta.file_object_deletions where object_key=$1`, key)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55000" {
		t.Fatalf("legacy DELETE did not preserve uncertain intent: %v", err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, `select exists(select 1 from pgbase_meta.file_object_deletions where object_key=$1 and retain_on_success)`, key).Scan(&exists); err != nil || !exists {
		t.Fatalf("legacy worker discarded uncertainty: exists=%v err=%v", exists, err)
	}
	migration := NewObjectMigrationService(controller, "")
	if activated, err := migration.activate(ctx, migrationID); !errors.Is(err, ErrStorageConflict) || activated {
		t.Fatalf("uncertain source migrated: activated=%v err=%v", activated, err)
	}
	var revision int64
	if err := pool.QueryRow(ctx, `select revision from pgbase_meta.file_storage_settings where singleton`).Scan(&revision); err != nil || revision != 1 {
		t.Fatalf("failed cutover changed storage revision=%d err=%v", revision, err)
	}
}

func TestRestoredStorageDetachesRetainedSourceIntents(t *testing.T) {
	ctx, pool, _, _, _, _, _ := newLocalMigrationFixture(t, nil)
	key := "thumbnails/" + uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.file_object_deletions(object_key,queued_at,retain_on_success,attempts,last_attempt_at,next_attempt_at,last_error) values($1,current_timestamp,true,3,current_timestamp,current_timestamp+interval '1 hour','object deletion failed')`, key); err != nil {
		t.Fatal(err)
	}
	if err := ResetRestoredStorage(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := ResetRestoredStorage(ctx, pool); err != nil {
		t.Fatalf("restore reset is not repeatable: %v", err)
	}
	var retained, migrationWork int
	var driver string
	if err := pool.QueryRow(ctx, `select (select count(*) from pgbase_meta.file_object_deletions),(select count(*) from pgbase_meta.file_storage_migrations),(select driver from pgbase_meta.file_storage_settings where singleton)`).Scan(&retained, &migrationWork, &driver); err != nil {
		t.Fatal(err)
	}
	if retained != 0 || migrationWork != 0 || driver != "local" {
		t.Fatalf("restored source work retained=%d migrations=%d driver=%q", retained, migrationWork, driver)
	}
}
