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
		"repeated witness": {"schema-index", "activate",
			"--witness", "0199c100-0000-7000-8000-000000000001",
			"--witness", "0199c100-0000-7000-8000-000000000001"},
	} {
		t.Run(name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := Execute(context.Background(), args, emptyEnv, IO{Stdout: io.Discard, Stderr: &stderr})
			if code != 2 {
				t.Fatalf("exit=%d stderr=%q", code, stderr.String())
			}
			if stderr.String() != "pgbase: "+schemaIndexUsage+"\n" {
				t.Fatalf("stderr=%q", stderr.String())
			}
		})
	}
}

// A repeated witness is how a proof comes to have the right length while a live
// schema writer nobody observed goes unwitnessed. The transition refuses it too;
// the flag refuses it where the message can still name the flag.
func TestSchemaIndexWitnessRepeatIsRefused(t *testing.T) {
	var witnesses witnessList
	for _, value := range []string{
		"0199c100-0000-7000-8000-000000000002",
		"0199c100-0000-7000-8000-000000000001",
	} {
		if err := witnesses.Set(value); err != nil {
			t.Fatalf("set witness %s: %v", value, err)
		}
	}
	if err := witnesses.Set("0199c100-0000-7000-8000-000000000002"); err == nil {
		t.Fatal("repeated witness was accepted")
	}
	if len(witnesses) != 2 {
		t.Fatalf("witnesses = %v", witnesses)
	}
}

func TestSchemaIndexIsDispatchedAsItsOwnCommand(t *testing.T) {
	if _, dispatched := productionCommands()["schema-index"]; !dispatched {
		t.Fatal("schema-index has no production handler")
	}
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"schema-index", "status"}, emptyEnv, IO{Stdout: io.Discard, Stderr: &stderr})
	// An empty environment fails configuration, which proves dispatch reached the
	// handler rather than falling through to the unknown-command path.
	if code != 2 || strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}
