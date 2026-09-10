//go:build integration

package usermigrate

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/doctor"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func validateManagedSchema(ctx context.Context, tx pgx.Tx) error {
	manifest, err := schema.ExternalMigrationManifest(ctx, tx)
	if err != nil {
		return err
	}
	return doctor.ValidateRequiredPhysicalSchema(ctx, tx, manifest)
}

func TestUserMigrationUpDownFailureAndSourceChange(t *testing.T) {
	pool := userMigrationPool(t)
	ctx := context.Background()
	migration := Migration{ID: "20260909120000_widgets", Revision: sha256.Sum256([]byte("widgets-v1")),
		Up: func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `create table public.user_widgets(id bigint primary key)`)
			return err
		},
		Down: func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `drop table public.user_widgets`)
			return err
		},
	}
	runner, err := New(pool, []Migration{migration}, schema.ValidateExternalMigration, nil)
	if err != nil {
		t.Fatal(err)
	}
	results, err := runner.Up(ctx)
	if err != nil || len(results) != 1 {
		t.Fatalf("Up()=%+v,%v", results, err)
	}
	if entries, err := runner.List(ctx); err != nil || len(entries) != 1 || !entries[0].Applied {
		t.Fatalf("List()=%+v,%v", entries, err)
	}
	changed := migration
	changed.Revision = sha256.Sum256([]byte("edited-source"))
	changedRunner, _ := New(pool, []Migration{changed}, schema.ValidateExternalMigration, nil)
	if _, err := changedRunner.List(ctx); err == nil {
		t.Fatal("edited applied migration accepted")
	}

	failing := Migration{ID: "20260909130000_failure", Revision: sha256.Sum256([]byte("failure")),
		Up: func(ctx context.Context, tx pgx.Tx) error {
			_, _ = tx.Exec(ctx, `create table public.must_rollback(id int)`)
			return errors.New("stop")
		},
		Down: func(context.Context, pgx.Tx) error { return nil },
	}
	failingRunner, _ := New(pool, []Migration{migration, failing}, schema.ValidateExternalMigration, nil)
	if _, err := failingRunner.Up(ctx); err == nil {
		t.Fatal("failed migration succeeded")
	}
	var exists bool
	if err := pool.QueryRow(ctx, `select to_regclass('public.must_rollback') is not null`).Scan(&exists); err != nil || exists {
		t.Fatalf("failed Up table exists=%t err=%v", exists, err)
	}
	failingDown := migration
	failingDown.Down = func(ctx context.Context, tx pgx.Tx) error {
		_, _ = tx.Exec(ctx, `drop table public.user_widgets`)
		return errors.New("stop down")
	}
	failingDownRunner, _ := New(pool, []Migration{failingDown}, schema.ValidateExternalMigration, nil)
	if _, err := failingDownRunner.Down(ctx); err == nil {
		t.Fatal("failed down succeeded")
	}
	if err := pool.QueryRow(ctx, `select to_regclass('public.user_widgets') is not null`).Scan(&exists); err != nil || !exists {
		t.Fatalf("failed Down table exists=%t err=%v", exists, err)
	}
	if entries, err := runner.List(ctx); err != nil || !entries[0].Applied {
		t.Fatalf("failed Down changed ledger: entries=%+v err=%v", entries, err)
	}
	if _, err := runner.Down(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select to_regclass('public.user_widgets') is not null`).Scan(&exists); err != nil || exists {
		t.Fatalf("Down table exists=%t err=%v", exists, err)
	}
	interrupted := Migration{ID: "20260909150000_interrupted", Revision: sha256.Sum256([]byte("interrupted")),
		Up: func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `create table public.interrupted_write(id int)`); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `select pg_sleep(5)`)
			return err
		},
		Down: func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `drop table public.interrupted_write`)
			return err
		},
	}
	interruptedRunner, _ := New(pool, []Migration{interrupted}, schema.ValidateExternalMigration, nil)
	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := interruptedRunner.Up(short); err == nil {
		t.Fatal("interrupted migration succeeded")
	}
	if err := pool.QueryRow(ctx, `select to_regclass('public.interrupted_write') is not null`).Scan(&exists); err != nil || exists {
		t.Fatalf("interrupted Up table exists=%t err=%v", exists, err)
	}
}

func TestUserMigrationRejectsManagedPhysicalDamageAndRollsBack(t *testing.T) {
	pool := userMigrationPool(t)
	ctx := context.Background()
	actor := uuid.New()
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.admins(id,email,password_hash,active,created_at,updated_at)
		values($1,$2,'test',true,now(),now())`, actor, actor.String()+"@example.com"); err != nil {
		t.Fatal(err)
	}
	service := schema.NewService(pool, &schema.Cache{}, nil)
	change := schema.NewCreateChange(schema.CreateCollectionInput{Name: "migration_guard", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}}})
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := service.Apply(ctx, schema.LegacyApply(change, plan, actor))
	if err != nil {
		t.Fatal(err)
	}
	table := applied.Collection.TableName
	column := applied.Collection.Fields[0].ColumnName

	custom := Migration{ID: "20260909155000_custom", Revision: sha256.Sum256([]byte("custom")),
		Up: func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `create table public.application_owned(id bigint primary key)`)
			return err
		},
		Down: func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `drop table public.application_owned`)
			return err
		},
	}
	damaging := Migration{ID: "20260909160000_damage", Revision: sha256.Sum256([]byte("damage")),
		Up: func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `alter table app.`+pgx.Identifier{table}.Sanitize()+` drop column `+pgx.Identifier{column}.Sanitize())
			return err
		},
		Down: func(context.Context, pgx.Tx) error { return nil },
	}
	runner, err := New(pool, []Migration{custom, damaging}, validateManagedSchema, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Up(ctx); err == nil {
		t.Fatal("migration that dropped a managed column succeeded")
	}
	var customExists bool
	if err := pool.QueryRow(ctx, `select to_regclass('public.application_owned') is not null`).Scan(&customExists); err != nil || !customExists {
		t.Fatalf("application-owned table was rejected: exists=%t err=%v", customExists, err)
	}
	var columnExists bool
	if err := pool.QueryRow(ctx, `select exists(select 1 from information_schema.columns where table_schema='app' and table_name=$1 and column_name=$2)`, table, column).Scan(&columnExists); err != nil || !columnExists {
		t.Fatalf("managed column was not rolled back: exists=%t err=%v", columnExists, err)
	}
	var ledger int
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_migrations`).Scan(&ledger); err != nil || ledger != 1 {
		t.Fatalf("damaging migration ledger=%d err=%v", ledger, err)
	}
}

func TestConcurrentUserMigrationRunnersApplyCallbackOnce(t *testing.T) {
	pool := userMigrationPool(t)
	migration := Migration{ID: "20260909140000_once", Revision: sha256.Sum256([]byte("once")),
		Up: func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `create table public.only_once(id int)`)
			return err
		},
		Down: func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `drop table public.only_once`)
			return err
		},
	}
	runner, _ := New(pool, []Migration{migration}, schema.ValidateExternalMigration, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := runner.Up(ctx); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_migrations`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("ledger count=%d err=%v", count, err)
	}
}

func TestUserMigrationRejectsManagedControlStateChanges(t *testing.T) {
	for _, test := range []struct {
		name string
		sql  string
	}{
		{name: "ledger", sql: `delete from pgbase_meta.user_migrations`},
		{name: "schema version", sql: `update pgbase_meta.schema_state set version=version+100 where singleton`},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool := userMigrationPool(t)
			ctx := context.Background()
			var initialVersion int64
			if err := pool.QueryRow(ctx, `select version from pgbase_meta.schema_state where singleton`).Scan(&initialVersion); err != nil {
				t.Fatal(err)
			}
			first := Migration{ID: "20260909170000_first", Revision: sha256.Sum256([]byte("first")),
				Up: func(context.Context, pgx.Tx) error { return nil }, Down: func(context.Context, pgx.Tx) error { return nil }}
			second := Migration{ID: "20260909180000_control", Revision: sha256.Sum256([]byte(test.name)),
				Up: func(ctx context.Context, tx pgx.Tx) error {
					if _, err := tx.Exec(ctx, `create table public.control_state_must_rollback(id int)`); err != nil {
						return err
					}
					_, err := tx.Exec(ctx, test.sql)
					return err
				}, Down: func(context.Context, pgx.Tx) error { return nil }}
			runner, err := New(pool, []Migration{first, second}, schema.ValidateExternalMigration, nil)
			if err != nil {
				t.Fatal(err)
			}
			results, err := runner.Up(ctx)
			if err == nil || len(results) != 1 || results[0].Migration.ID != first.ID {
				t.Fatalf("Up() results=%+v error=%v", results, err)
			}
			var ledger int
			var version int64
			var tableExists bool
			if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_migrations`).Scan(&ledger); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `select version from pgbase_meta.schema_state where singleton`).Scan(&version); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `select to_regclass('public.control_state_must_rollback') is not null`).Scan(&tableExists); err != nil {
				t.Fatal(err)
			}
			if ledger != 1 || version != initialVersion+1 || tableExists {
				t.Fatalf("rollback ledger=%d version=%d tableExists=%t", ledger, version, tableExists)
			}
		})
	}
}

func TestCleanupHistoryPreviewsDeletesOnlyRetiredSourceAndRollsBackFailures(t *testing.T) {
	pool := userMigrationPool(t)
	ctx := context.Background()
	revision := sha256.Sum256([]byte("retired-source"))
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.user_migrations(id,revision,applied_at,execution_ms) values($1,$2,clock_timestamp(),7)`, "20260910120000_retired", revision[:]); err != nil {
		t.Fatal(err)
	}
	var schemaVersion int64
	if err := pool.QueryRow(ctx, `select version from pgbase_meta.schema_state where singleton`).Scan(&schemaVersion); err != nil {
		t.Fatal(err)
	}
	runner, err := New(pool, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := runner.CleanupHistory(ctx, true)
	if err != nil || len(preview.Entries) != 1 || preview.Entries[0].ID != "20260910120000_retired" || !preview.DryRun {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_migrations`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("dry-run ledger=%d err=%v", count, err)
	}
	if _, err := pool.Exec(ctx, `create function pgbase_meta.reject_retired_history_cleanup() returns trigger language plpgsql as $$ begin raise exception 'fixture rollback'; end $$; create trigger reject_retired_history_cleanup before delete on pgbase_meta.user_migrations for each row execute function pgbase_meta.reject_retired_history_cleanup()`); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.CleanupHistory(ctx, false); err == nil {
		t.Fatal("cleanup succeeded despite failing delete trigger")
	}
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_migrations`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed cleanup ledger=%d err=%v", count, err)
	}
	if _, err := pool.Exec(ctx, `drop trigger reject_retired_history_cleanup on pgbase_meta.user_migrations; drop function pgbase_meta.reject_retired_history_cleanup()`); err != nil {
		t.Fatal(err)
	}
	cleanup, err := runner.CleanupHistory(ctx, false)
	if err != nil || cleanup.DryRun || len(cleanup.Entries) != 1 {
		t.Fatalf("cleanup=%+v err=%v", cleanup, err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_migrations`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cleanup ledger=%d err=%v", count, err)
	}
	var afterVersion int64
	if err := pool.QueryRow(ctx, `select version from pgbase_meta.schema_state where singleton`).Scan(&afterVersion); err != nil || afterVersion != schemaVersion {
		t.Fatalf("schema version=%d want=%d err=%v", afterVersion, schemaVersion, err)
	}
}

func userMigrationPool(t *testing.T) *database.Pool {
	t.Helper()
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	return pool
}
