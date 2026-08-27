package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRunRejectsUnknownCommand(t *testing.T) {
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"unknown"}, emptyEnv, io.Discard, &stderr, CommandSet{})
	if code != 2 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "serve, migrate, admin, doctor") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunDispatchesKnownCommands(t *testing.T) {
	called := false
	command := func(ctx context.Context, args []string, lookup func(string) (string, bool), stdout, stderr io.Writer) error {
		called = true
		if got := strings.Join(args, ","); got != "--check,fast" {
			t.Fatalf("args = %q", got)
		}
		if value, ok := lookup("PGBASE_TEST"); !ok || value != "present" {
			t.Fatal("lookup was not passed through")
		}
		if _, err := io.WriteString(stdout, "ran"); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	var stdout bytes.Buffer
	code := Run(context.Background(), []string{"migrate", "--check", "fast"}, func(key string) (string, bool) {
		return "present", key == "PGBASE_TEST"
	}, &stdout, io.Discard, CommandSet{Migrate: command})
	if code != 0 || !called || stdout.String() != "ran" {
		t.Fatalf("code=%d called=%t stdout=%q", code, called, stdout.String())
	}
}

func TestRunKnownCommandFailuresReturnExitCodeOne(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		commands CommandSet
		want     string
	}{
		{name: "nil handler", command: "serve", commands: CommandSet{}, want: "unavailable"},
		{name: "handler error", command: "doctor", commands: CommandSet{Doctor: func(context.Context, []string, func(string) (string, bool), io.Writer, io.Writer) error {
			return errors.New("database unavailable")
		}}, want: "database unavailable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := Run(context.Background(), []string{tt.command}, emptyEnv, io.Discard, &stderr, tt.commands)
			if code != 1 || !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
		})
	}
}

func TestRunDispatchesHiddenHealthcheck(t *testing.T) {
	called := false
	code := Run(context.Background(), []string{"healthcheck"}, emptyEnv, io.Discard, io.Discard, CommandSet{
		Healthcheck: func(context.Context, []string, func(string) (string, bool), io.Writer, io.Writer) error {
			called = true
			return nil
		},
	})
	if code != 0 || !called {
		t.Fatalf("code=%d called=%t", code, called)
	}
}

func emptyEnv(string) (string, bool) { return "", false }
