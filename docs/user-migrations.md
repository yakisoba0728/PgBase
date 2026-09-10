# User migrations

User migrations are Go callbacks compiled into your application. Generate a source-bound template for handwritten SQL with:

```sh
pgbase migration create add_widgets ./migrations
```

For an automatically generated collection-schema migration, capture the database before and after the administrator change and diff the two snapshots:

```sh
pgbase migration snapshot before.json
# Apply and verify the administrator schema changes.
pgbase migration snapshot after.json
pgbase migration diff rename_article_title before.json after.json ./migrations
```

`snapshot -` writes the snapshot to standard output. Snapshot files use `pgbase.schema-migration/v1`; they contain the logical collection schema, stable field matching IDs, relations, views and dependencies, indexes, rules, access, presentation metadata, defaults, and field options. They omit records, files, credentials, administrator identity, timestamps, and physical PostgreSQL names. Repeated snapshots of unchanged schema are byte-identical.

The diff command writes executable Go. Its `Up` checks the exact starting logical snapshot and applies the target snapshot transactionally. Its `Down` applies the reverse transition when values can be retained. The command prints data-loss warnings for actions such as a Down that removes a newly created field. Collection/field deletion and field type replacement make Down return `*pgbase.IrreversibleMigrationError`, because removed or converted values cannot be reconstructed. Review the warnings and generated source before committing it.

Every generated file embeds its own source and registers its exact bytes. PgBase stores the SHA-256 digest when the migration is applied and refuses to continue if an applied file is later edited. Import the generated migration package from your command binary and delegate to the public CLI entry point:

```go
package main

import (
	"context"
	"os"

	_ "example.com/myapp/migrations"
	"github.com/yakisoba0728/PgBase/pgbasecli"
)

func main() {
	os.Exit(pgbasecli.Execute(context.Background(), os.Args[1:]))
}
```

`Up` applies pending migrations in filename order. `Down` reverts only the latest applied migration. Each callback, collection catalog update, history update, and schema-version transition uses one PostgreSQL transaction. A failure or cancellation rolls back its SQL and leaves history unchanged. Before commit, PgBase compiles its catalog and checks every managed table, column, index, constraint, trigger, and function. Callbacks must also leave PgBase's user-migration ledger and schema-version row unchanged; the runner owns those updates and rejects a callback that alters them. Application-owned objects outside the managed schema remain available for custom migrations. Concurrent migration runners serialize with PgBase schema operations, and successful changes refresh the application's shared schema cache.

The stock `pgbase` executable can generate snapshots and files. Run `migration list`, `migration up`, `migration down`, `migration history`, or `migration history-sync` through your command binary so its generated Go callbacks are compiled and registered. The complete runnable layout is in `examples/user-migrations`.

`migration history` compares compiled source with database history and reports `pending`, `applied`, `checksum_mismatch`, `source_gap`, or `history_only`. History problems remain visible even when normal `list`/`up` validation would stop at the first mismatch. `migration history-sync ID` is an explicit recovery operation for a generated migration whose schema is already present but whose history row is missing. It accepts only the next source migration, requires embedded before/after snapshots, compares the current database to the exact target snapshot under the migration and schema locks, and then inserts the checksum-pinned history row without running migration callbacks or changing schema version. It cannot adopt handwritten migration source.

Administrator schema changes can be recorded automatically by setting `Options.UserMigrationAutoRecordDirectory` or `PGBASE_USER_MIGRATION_AUTO_RECORD_DIR`. Recording is disabled when the directory is empty. When enabled, PgBase stores the exact generated source in PostgreSQL in the same transaction as each committed collection mutation or schema import, then writes the `.go` file after commit. A filesystem failure leaves a durable `pending` row and does not turn a committed schema request into a failure. Use these commands to inspect or retry it:

```sh
pgbase migration auto-status
pgbase migration recover
```

Recovery is idempotent. A byte-identical existing file is accepted; a different file at the reserved name is left untouched and reported as `conflict`. Pending recovery also runs in the application background while automatic recording is enabled.

Do not edit a migration after applying it. Add a later migration that corrects or reverses the change. Keep `Down` as the exact inverse of `Up` and use only the provided `pgx.Tx`; opening an independent connection would escape atomic rollback.

These callbacks are trusted Go code, not a sandbox. PgBase cannot roll back network calls, filesystem changes, or SQL sent through a separate connection. Keep irreversible effects outside retryable callbacks; database rollback applies to work performed through the supplied transaction.

Verification includes cancellation during SQL, failed Up/Down with unchanged history, source changes, concurrent runners, and a schema Apply waiting behind an active user migration. Generated migration verification builds a separate Go module and executes its registered Up/Down against a fresh disposable PostgreSQL database. Focused PostgreSQL tests also cover relation/view ordering, field-ID rename with preserved row values and options, history sync, and automatic-source recovery after commit and filesystem failures.
