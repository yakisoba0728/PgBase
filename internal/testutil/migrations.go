package testutil

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

// ApplySystemMigrationPrefix installs a prefix of the embedded system migrations
// and records the matching history. It is intended for integration tests that
// need to exercise compatibility with an older, but valid, database.
func ApplySystemMigrationPrefix(t *testing.T, databaseURL string, migrations []system.Migration) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL test database: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(ctx); err != nil {
			t.Errorf("close PostgreSQL test database connection: %v", err)
		}
	})

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin system migration prefix transaction: %v", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && err != pgx.ErrTxClosed {
			t.Errorf("roll back system migration prefix transaction: %v", err)
		}
	}()

	if _, err := tx.Exec(ctx, `
		create schema if not exists pgbase_meta;
		create table pgbase_meta.system_migrations (
			version bigint primary key,
			name text not null,
			checksum bytea not null,
			applied_at timestamptz not null default now(),
			execution_ms bigint not null check (execution_ms >= 0)
		)`); err != nil {
		t.Fatalf("create system migration history: %v", err)
	}
	for _, migration := range migrations {
		if _, err := tx.Exec(ctx, migration.SQL); err != nil {
			t.Fatalf("apply system migration %d: %v", migration.Version, err)
		}
		if _, err := tx.Exec(ctx, `insert into pgbase_meta.system_migrations
			(version, name, checksum, execution_ms) values ($1, $2, $3, 0)`,
			migration.Version, migration.Name, migration.SHA256[:],
		); err != nil {
			t.Fatalf("record system migration %d: %v", migration.Version, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit system migration prefix: %v", err)
	}
}
