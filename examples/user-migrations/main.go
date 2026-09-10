// Run with the usual PGBASE_* environment: go run ./examples/user-migrations.
package main

import (
	"context"
	"os"

	_ "github.com/yakisoba0728/PgBase/examples/user-migrations/migrations"
	"github.com/yakisoba0728/PgBase/pgbasecli"
)

func main() {
	os.Exit(pgbasecli.Execute(context.Background(), os.Args[1:]))
}
