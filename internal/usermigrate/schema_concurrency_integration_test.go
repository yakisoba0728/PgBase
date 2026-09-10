//go:build integration

package usermigrate

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestUserMigrationSerializesWithSchemaApplyAndRefreshesSharedCache(t *testing.T) {
	pool := userMigrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cache := &schema.Cache{}
	schemas := schema.NewService(pool, cache, nil)
	change := schema.NewCreateChange(schema.CreateCollectionInput{
		Name:   "schema_during_user_migration",
		Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}},
	})
	plan, err := schemas.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.admins
		(id,email,password_hash,active,created_at,updated_at)
		values($1,$2,'x',true,now(),now())`, actor, actor.String()+"@example.com"); err != nil {
		t.Fatal(err)
	}

	callbackStarted := make(chan struct{})
	releaseCallback := make(chan struct{})
	migration := Migration{
		ID:       "20260909190000_schema_serialization",
		Revision: sha256.Sum256([]byte("schema-serialization-v1")),
		Up: func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `create table public.user_migration_committed(id int primary key)`); err != nil {
				return err
			}
			close(callbackStarted)
			select {
			case <-releaseCallback:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		Down: func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `drop table public.user_migration_committed`)
			return err
		},
	}
	runner, err := New(pool, []Migration{migration}, schema.ValidateExternalMigration, func(ctx context.Context) error {
		return schema.PreflightCatalog(ctx, pool, cache)
	})
	if err != nil {
		t.Fatal(err)
	}
	migrationResult := make(chan error, 1)
	go func() {
		_, runErr := runner.Up(ctx)
		migrationResult <- runErr
	}()
	<-callbackStarted

	applyResult := make(chan error, 1)
	go func() {
		_, applyErr := schemas.Apply(ctx, schema.LegacyApply(change, plan, actor))
		applyResult <- applyErr
	}()
	waitForSchemaAdvisoryWaiter(t, ctx, pool)
	select {
	case applyErr := <-applyResult:
		t.Fatalf("schema apply settled while user migration held the schema lock: %v", applyErr)
	default:
	}
	assertSchemaConcurrencyState(t, ctx, pool, 0, false, false)

	close(releaseCallback)
	if err := <-migrationResult; err != nil {
		t.Fatalf("user migration: %v", err)
	}
	if err := <-applyResult; !errors.Is(err, schema.ErrConflict) {
		t.Fatalf("schema apply after user migration = %v, want stale-plan conflict", err)
	}
	assertSchemaConcurrencyState(t, ctx, pool, 1, true, false)
	if published := cache.Load(); published.Version != 1 || len(published.Collections) != 0 {
		t.Fatalf("published cache after migration = version %d, collections %#v", published.Version, published.Collections)
	}
}

func waitForSchemaAdvisoryWaiter(t *testing.T, ctx context.Context, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		if err := pool.QueryRow(ctx, `select exists(select 1 from pg_stat_activity
			where datname=current_database() and pid<>pg_backend_pid()
			and wait_event_type='Lock')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("schema apply did not wait for the user migration schema fence")
}

func assertSchemaConcurrencyState(t *testing.T, ctx context.Context, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, version int64, migrationTable, schemaTable bool) {
	t.Helper()
	var gotVersion int64
	var gotMigrationTable, gotSchemaTable bool
	err := pool.QueryRow(ctx, `select version,
		to_regclass('public.user_migration_committed') is not null,
		to_regclass('app.schema_during_user_migration') is not null
		from pgbase_meta.schema_state where singleton`).Scan(&gotVersion, &gotMigrationTable, &gotSchemaTable)
	if err != nil {
		t.Fatal(err)
	}
	if gotVersion != version || gotMigrationTable != migrationTable || gotSchemaTable != schemaTable {
		t.Fatalf("state = version %d, migration table %t, schema table %t; want %d/%t/%t",
			gotVersion, gotMigrationTable, gotSchemaTable, version, migrationTable, schemaTable)
	}
}
