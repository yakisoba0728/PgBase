// Command javascript runs PgBase's CLI with the JavaScript example bundle.
// Configure PGBASE_JS_DIR and the usual PGBASE_* environment variables before
// invoking js, migration, or serve subcommands.
package main

import (
	"context"
	"os"

	"github.com/webisnoteasy/PgBase/pgbasecli"
)

func main() {
	os.Exit(pgbasecli.Execute(context.Background(), os.Args[1:]))
}
