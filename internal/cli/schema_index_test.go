package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

// The rollout switch must be reachable only through this operator command, so
// its argument handling refuses anything it does not recognize rather than
// guessing a transition.
func TestSchemaIndexRejectsInvalidUsage(t *testing.T) {
	for name, args := range map[string][]string{
		"missing subcommand":       {"schema-index"},
		"unknown subcommand":       {"schema-index", "promote"},
		"unknown flag":             {"schema-index", "status", "--force"},
		"positional argument":      {"schema-index", "status", "extra"},
		"malformed witness":        {"schema-index", "activate", "--witness", "not-a-uuid"},
		"witness outside activate": {"schema-index", "build", "--witness", "0199c100-0000-7000-8000-000000000001"},
	} {
		t.Run(name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := Run(context.Background(), args, emptyEnv, io.Discard, &stderr, DefaultCommands())
			if code != 2 {
				t.Fatalf("exit=%d stderr=%q", code, stderr.String())
			}
			if stderr.String() != "pgbase: "+schemaIndexUsage+"\n" {
				t.Fatalf("stderr=%q", stderr.String())
			}
		})
	}
}

func TestSchemaIndexIsDispatchedAsItsOwnCommand(t *testing.T) {
	commands := DefaultCommands()
	if commands.SchemaIndex == nil {
		t.Fatal("schema-index has no default handler")
	}
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"schema-index", "status"}, emptyEnv, io.Discard, &stderr, DefaultCommands())
	// An empty environment fails configuration, which proves dispatch reached the
	// handler rather than falling through to the unknown-command path.
	if code != 2 || strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}
