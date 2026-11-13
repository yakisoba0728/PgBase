package migrate

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/migrations/system"
)

func lockMigrationTransaction(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, "set local lock_timeout = '5s'"); err != nil {
		return fmt.Errorf("set migration lock timeout: %w", err)
	}
	if _, err := tx.Exec(ctx, "set local statement_timeout = 0"); err != nil {
		return fmt.Errorf("disable request statement timeout for migrations: %w", err)
	}
	if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", advisoryLockKey); err != nil {
		return fmt.Errorf("acquire migration advisory lock: %w", err)
	}
	return nil
}

func applyMigration(ctx context.Context, tx pgx.Tx, migration system.Migration) (int64, error) {
	started := time.Now()
	if _, err := tx.Exec(ctx, migration.SQL); err != nil {
		return 0, fmt.Errorf("apply system migration %d (%s): %w", migration.Version, migration.Name, err)
	}
	executionMS := time.Since(started).Milliseconds()
	if _, err := tx.Exec(ctx, `
		insert into pgbase_meta.system_migrations (version, name, checksum, execution_ms)
		values ($1, $2, $3, $4)`, migration.Version, migration.Name, migration.SHA256[:], executionMS); err != nil {
		return 0, fmt.Errorf("record system migration %d (%s): %w", migration.Version, migration.Name, err)
	}
	return executionMS, nil
}
