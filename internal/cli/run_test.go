package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestRunTreatsDiagnosticWriteFailureAsOperationalFailure(t *testing.T) {
	code := Run(context.Background(), nil, emptyEnv, io.Discard, failingWriter{}, CommandSet{})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 when stderr cannot be written", code)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

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

func TestHealthcheckAcceptsOnlyHTTP200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"healthcheck", "--url", server.URL}, emptyEnv, io.Discard, &stderr, DefaultCommands())
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestHealthcheckRejectsNon200AndRedirectWithoutFollowingIt(t *testing.T) {
	redirectTargetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirectTargetCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	tests := []struct {
		name    string
		handler http.Handler
	}{
		{name: "non-200", handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})},
		{name: "redirect", handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
		})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()
			var stderr bytes.Buffer
			code := Run(context.Background(), []string{"healthcheck", "--url", server.URL}, emptyEnv, io.Discard, &stderr, DefaultCommands())
			if code != 1 || stderr.String() != "pgbase: command failed\n" {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
		})
	}
	if redirectTargetCalls != 0 {
		t.Fatalf("redirect target calls = %d", redirectTargetCalls)
	}
}

func TestHealthcheckTimesOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	started := time.Now()
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"healthcheck", "--url", server.URL, "--timeout", "25ms"}, emptyEnv, io.Discard, &stderr, DefaultCommands())
	if code != 1 || stderr.String() != "pgbase: command failed\n" {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("healthcheck elapsed = %s", elapsed)
	}
}

func TestHealthcheckUsesTwoSecondDefaultTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	started := time.Now()
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"healthcheck", "--url", server.URL}, emptyEnv, io.Discard, &stderr, DefaultCommands())
	elapsed := time.Since(started)
	if code != 1 || stderr.String() != "pgbase: command failed\n" {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if elapsed < 1500*time.Millisecond || elapsed > 3500*time.Millisecond {
		t.Fatalf("default healthcheck timeout elapsed = %s", elapsed)
	}
}

func TestHealthcheckRejectsInvalidURLWithExitCodeTwo(t *testing.T) {
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"healthcheck", "--url", "://not-a-url"}, emptyEnv, io.Discard, &stderr, DefaultCommands())
	if code != 2 || stderr.String() != "pgbase: invalid healthcheck URL\n" {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestHealthcheckIsExcludedFromOrdinaryHelp(t *testing.T) {
	var stderr bytes.Buffer
	code := Run(context.Background(), nil, emptyEnv, io.Discard, &stderr, DefaultCommands())
	if code != 2 || strings.Contains(stderr.String(), "healthcheck") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
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

func TestServeRedactsPercentEncodedDSNSecretsInStartupErrors(t *testing.T) {
	tests := []struct {
		name        string
		secret      string
		databaseURL string
	}{
		{name: "encoded key", secret: "encoded-startup-secret-94c1", databaseURL: "postgres://127.0.0.1/pgbase?sslpass%77ord=encoded-startup-secret-94c1&pool_max_conns=not-a-number"},
		{name: "raw space in value", secret: "raw-space-secret-suffix-73c9", databaseURL: "postgres://127.0.0.1/pgbase?sslpassword=prefix raw-space-secret-suffix-73c9&pool_max_conns=not-a-number"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := Run(context.Background(), []string{"serve"}, migrationEnv(test.databaseURL), io.Discard, &stderr, DefaultCommands())
			if code != 1 {
				t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
			}
			got := stderr.String()
			if strings.Contains(got, test.secret) || strings.Contains(got, test.databaseURL) {
				t.Fatalf("startup log leaked database secret: %q", got)
			}
			for _, diagnostic := range []string{"[REDACTED]", "pool_max_conns", "not-a-number"} {
				if !strings.Contains(got, diagnostic) {
					t.Fatalf("startup log lost diagnostic %q: %q", diagnostic, got)
				}
			}
		})
	}
}

// TestMigrateRejectsMalformedDatabaseURLWithExitCodeTwoWithoutLeakingIt pins
// both halves of the pool-configuration rejection: the operator gets a
// diagnostic naming the parameter that failed, and neither the password nor
// the DSN the parser echoes back reaches the process log.
func TestMigrateRejectsMalformedDatabaseURLWithExitCodeTwoWithoutLeakingIt(t *testing.T) {
	const secret = "migrate-configuration-secret-must-not-leak"
	const databaseURL = "postgres://admin:" + secret + "@localhost/pgbase?pool_max_conns=not-a-number"
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"migrate", "status"}, migrationEnv(databaseURL), io.Discard, &stderr, DefaultCommands())
	if code != 2 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.HasSuffix(stderr.String(), "pgbase: invalid configuration\n") {
		t.Fatalf("stderr = %q, want the invalid configuration line last", stderr.String())
	}
	for _, diagnostic := range []string{"parse PostgreSQL pool configuration", "pool_max_conns", "not-a-number", "[REDACTED]"} {
		if !strings.Contains(stderr.String(), diagnostic) {
			t.Fatalf("stderr lost configuration diagnostic %q: %q", diagnostic, stderr.String())
		}
	}
	for _, sensitive := range []string{databaseURL, secret} {
		if strings.Contains(stderr.String(), sensitive) {
			t.Fatalf("stderr leaked %q: %q", sensitive, stderr.String())
		}
	}
}

func TestAdminRejectsMalformedDatabaseURLWithoutLeakingIt(t *testing.T) {
	const secret = "admin-configuration-secret-must-not-leak"
	const databaseURL = "postgres://admin:" + secret + "@localhost/pgbase?pool_max_conns=not-a-number"
	originalStdin := commandStdin
	t.Cleanup(func() { commandStdin = originalStdin })
	commandStdin = strings.NewReader("correct horse battery staple\n")
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"admin", "create", "--email", "admin@example.com", "--password-stdin"}, migrationEnv(databaseURL), &stdout, &stderr, DefaultCommands())
	if code != 2 {
		t.Fatalf("exit code = %d, stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	for _, diagnostic := range []string{"parse PostgreSQL pool configuration", "pool_max_conns", "[REDACTED]"} {
		if !strings.Contains(stderr.String(), diagnostic) {
			t.Fatalf("stderr lost configuration diagnostic %q: %q", diagnostic, stderr.String())
		}
	}
	for _, sensitive := range []string{databaseURL, secret, "correct horse battery staple"} {
		if strings.Contains(stdout.String(), sensitive) || strings.Contains(stderr.String(), sensitive) {
			t.Fatalf("admin leaked %q: stdout=%q stderr=%q", sensitive, stdout.String(), stderr.String())
		}
	}
}

func TestDoctorReportsMalformedDatabaseURLWithoutLeakingIt(t *testing.T) {
	const secret = "doctor-configuration-secret-must-not-leak"
	const databaseURL = "postgres://admin:" + secret + "@localhost/pgbase?pool_max_conns=not-a-number"
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"doctor"}, migrationEnv(databaseURL), &stdout, &stderr, DefaultCommands())
	if code != 2 {
		t.Fatalf("exit code = %d, stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "configuration_invalid") {
		t.Fatalf("doctor report = %q, want a configuration_invalid finding", stdout.String())
	}
	for _, diagnostic := range []string{"parse PostgreSQL pool configuration", "pool_max_conns", "[REDACTED]"} {
		if !strings.Contains(stderr.String(), diagnostic) {
			t.Fatalf("stderr lost configuration diagnostic %q: %q", diagnostic, stderr.String())
		}
	}
	for _, sensitive := range []string{databaseURL, secret} {
		if strings.Contains(stdout.String(), sensitive) || strings.Contains(stderr.String(), sensitive) {
			t.Fatalf("doctor leaked %q: stdout=%q stderr=%q", sensitive, stdout.String(), stderr.String())
		}
	}
}

func TestMigrateReportsRedactedOperationalDiagnostics(t *testing.T) {
	const secret = "migrate-password-must-not-leak"
	const databaseURL = "postgres://pgbase:" + secret + "@127.0.0.1:1/pgbase?sslmode=disable&connect_timeout=1"
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"migrate", "status"}, migrationEnv(databaseURL), io.Discard, &stderr, DefaultCommands())
	if code != 1 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "open PostgreSQL pool") {
		t.Fatalf("stderr lost migration diagnostic: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), secret) || strings.Contains(stderr.String(), databaseURL) {
		t.Fatalf("stderr leaked migration database credentials: %q", stderr.String())
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
