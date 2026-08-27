package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/webisnoteasy/PgBase/internal/testutil"
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
		}}, want: "command failed"},
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

func TestRunSanitizesOperationalErrors(t *testing.T) {
	const dsn = "postgres://admin:super-secret@db.example/pgbase"
	const sqlState = "SQLSTATE 42P01"
	const sql = "select secret_column from pgbase_meta.admins"

	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"migrate"}, emptyEnv, io.Discard, &stderr, CommandSet{
		Migrate: func(context.Context, []string, func(string) (string, bool), io.Writer, io.Writer) error {
			return errors.New(dsn + ": " + sqlState + ": " + sql)
		},
	})
	if code != 1 {
		t.Fatalf("exit code = %d", code)
	}
	if got, want := stderr.String(), "pgbase: command failed\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	for _, sensitive := range []string{dsn, "super-secret", sqlState, sql} {
		if strings.Contains(stderr.String(), sensitive) {
			t.Fatalf("stderr leaked %q: %q", sensitive, stderr.String())
		}
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

func TestMigrateRejectsInvalidConfigurationWithExitCodeTwo(t *testing.T) {
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"migrate", "status"}, emptyEnv, io.Discard, &stderr, DefaultCommands())
	if code != 2 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.String() != "pgbase: invalid configuration\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestServeRejectsInvalidConfigurationWithExitCodeTwo(t *testing.T) {
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"serve"}, emptyEnv, io.Discard, &stderr, DefaultCommands())
	if code != 2 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.String() != "pgbase: invalid configuration\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestMigrateRejectsMalformedDatabaseURLWithExitCodeTwoWithoutLeakingIt(t *testing.T) {
	const databaseURL = "postgres://admin:super-secret@localhost/pgbase?pool_max_conns=not-a-number"
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"migrate", "status"}, migrationEnv(databaseURL), io.Discard, &stderr, DefaultCommands())
	if code != 2 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if got, want := stderr.String(), "pgbase: invalid configuration\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	for _, sensitive := range []string{databaseURL, "super-secret", "not-a-number"} {
		if strings.Contains(stderr.String(), sensitive) {
			t.Fatalf("stderr leaked %q: %q", sensitive, stderr.String())
		}
	}
}

func TestMigrateUpAndStatus(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	lookup := migrationEnv(databaseURL)

	var upOutput bytes.Buffer
	if code := Run(context.Background(), []string{"migrate", "up"}, lookup, &upOutput, io.Discard, DefaultCommands()); code != 0 {
		t.Fatalf("migrate up exit code = %d, stdout = %q", code, upOutput.String())
	}
	if !strings.Contains(upOutput.String(), "current=1") || !strings.Contains(upOutput.String(), "latest=1") {
		t.Fatalf("migrate up stdout = %q", upOutput.String())
	}

	var statusOutput bytes.Buffer
	if code := Run(context.Background(), []string{"migrate", "status"}, lookup, &statusOutput, io.Discard, DefaultCommands()); code != 0 {
		t.Fatalf("migrate status exit code = %d, stdout = %q", code, statusOutput.String())
	}
	if !strings.Contains(statusOutput.String(), "current=1") || !strings.Contains(statusOutput.String(), "dirty=false") {
		t.Fatalf("migrate status stdout = %q", statusOutput.String())
	}
}

func TestMigrateRejectsUnknownOperationWithExitCodeTwo(t *testing.T) {
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"migrate", "down"}, migrationEnv("postgres://localhost/unused"), io.Discard, &stderr, DefaultCommands())
	if code != 2 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: pgbase migrate") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func migrationEnv(databaseURL string) func(string) (string, bool) {
	env := map[string]string{
		"PGBASE_DATABASE_URL": databaseURL,
		"PGBASE_TOKEN_SECRET": base64.StdEncoding.EncodeToString(make([]byte, 32)),
	}
	return func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	}
}

func emptyEnv(string) (string, bool) { return "", false }
