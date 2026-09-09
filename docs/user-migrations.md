# User migrations

User migrations are Go callbacks compiled into your application. Generate a source-bound template with:

```sh
pgbase migration create add_widgets ./migrations
```

The generated file embeds its own source and registers its exact bytes. PgBase stores the SHA-256 digest when the migration is applied and refuses to continue if an applied file is later edited. Import the generated migration package from your command binary and delegate to the public CLI entry point:

```go
package main

import (
	"context"
	"os"

	_ "example.com/myapp/migrations"
	"github.com/webisnoteasy/PgBase/pgbasecli"
)

func main() {
	os.Exit(pgbasecli.Execute(context.Background(), os.Args[1:]))
}
```

`Up` applies pending migrations in filename order. `Down` reverts only the latest applied migration. Each callback and ledger update uses one PostgreSQL transaction. A failure or cancellation rolls back its SQL and leaves history unchanged. Before commit, PgBase compiles its catalog and checks every managed table, column, index, constraint, trigger, and function. Callbacks must also leave PgBase's user-migration ledger and schema-version row unchanged; the runner owns those updates and rejects a callback that alters them. Application-owned objects outside the managed schema remain available for custom migrations. Concurrent migration runners serialize with PgBase schema operations, and successful changes refresh the application's shared schema cache.

The stock `pgbase` executable can generate files. Run `migration list`, `migration up`, or `migration down` through your command binary so its generated Go callbacks are compiled and registered. The complete runnable layout is in `examples/user-migrations`.

Do not edit a migration after applying it. Add a later migration that corrects or reverses the change. Keep `Down` as the exact inverse of `Up` and use only the provided `pgx.Tx`; opening an independent connection would escape atomic rollback.

These callbacks are trusted Go code, not a sandbox. PgBase cannot roll back network calls, filesystem changes, or SQL sent through a separate connection. Keep irreversible effects outside retryable callbacks; database rollback applies to work performed through the supplied transaction.

Verification includes cancellation during SQL, failed Up/Down with unchanged history, source changes, concurrent runners, and a schema Apply waiting behind an active user migration. After that migration commits, the stale schema plan is rejected without partial DDL and the shared cache reflects the committed version. The external-module CLI regression builds the generated package and runs `migration list`; Up/Down transaction semantics are covered by the runner's PostgreSQL tests.
