// Package cli provides command dispatch for the pgbase executable.
package cli

import (
	"context"
	"fmt"
	"io"
)

// Command is a pgbase CLI command handler.
type Command func(context.Context, []string, func(string) (string, bool), io.Writer, io.Writer) error

// CommandSet contains the handlers exposed by the pgbase command line.
type CommandSet struct {
	Serve       Command
	Migrate     Command
	Admin       Command
	Doctor      Command
	Healthcheck Command
}

// DefaultCommands returns the command handlers wired into the production binary.
// Commands are added by their owning packages in later implementation tasks.
func DefaultCommands() CommandSet {
	return CommandSet{}
}

// Run dispatches a command and returns its process exit code.
func Run(ctx context.Context, args []string, lookup func(string) (string, bool), stdout, stderr io.Writer, commands CommandSet) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	name := args[0]
	var command Command
	switch name {
	case "serve":
		command = commands.Serve
	case "migrate":
		command = commands.Migrate
	case "admin":
		command = commands.Admin
	case "doctor":
		command = commands.Doctor
	case "healthcheck":
		command = commands.Healthcheck
	default:
		fmt.Fprintf(stderr, "pgbase: unknown command %q\n", name)
		printUsage(stderr)
		return 2
	}

	if command == nil {
		fmt.Fprintf(stderr, "pgbase: command %q is unavailable\n", name)
		return 1
	}
	if err := command(ctx, args[1:], lookup, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "pgbase: %v\n", err)
		return 1
	}
	return 0
}

func printUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "usage: pgbase <serve, migrate, admin, doctor> [arguments]")
}
