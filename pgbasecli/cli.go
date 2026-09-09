// Package pgbasecli exposes PgBase's command dispatcher to application
// binaries that register user-authored migrations with blank imports.
package pgbasecli

import (
	"context"
	"os"

	internalcli "github.com/webisnoteasy/PgBase/internal/cli"
)

// Execute runs PgBase commands with the process environment and standard IO.
func Execute(ctx context.Context, args []string) int {
	return internalcli.Execute(ctx, args, os.LookupEnv, internalcli.SystemIO())
}
