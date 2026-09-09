package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"

	"github.com/webisnoteasy/PgBase/internal/buildinfo"
)

const versionUsage = "usage: pgbase version --json"

// runVersion is composed into the closed production command table by the
// integration lead. It is deliberately network- and database-free.
func runVersion(_ context.Context, args []string, _ environment, streams IO) error {
	flags := flag.NewFlagSet("version", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	jsonOutput := flags.Bool("json", false, "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || !*jsonOutput {
		return configurationError{err: errors.New("invalid version usage"), message: versionUsage}
	}
	return json.NewEncoder(streams.stdout()).Encode(buildinfo.Current())
}
