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
	code := Execute(context.Background(), []string{"unknown"}, emptyEnv, testIO(nil, io.Discard, &stderr))
	if code != 2 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "serve, migrate, migration, admin, doctor") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunTreatsDiagnosticWriteFailureAsOperationalFailure(t *testing.T) {
	code := Execute(context.Background(), nil, emptyEnv, testIO(nil, io.Discard, failingWriter{}))
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 when stderr cannot be written", code)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestExecutePassesArgumentsEnvironmentAndStreamsToTheCommand(t *testing.T) {
	called := false
	handler := func(_ context.Context, args []string, env environment, streams IO) error {
		called = true
		if got := strings.Join(args, ","); got != "--check,fast" {
			t.Fatalf("args = %q", got)
		}
		if value, ok := env("PGBASE_TEST"); !ok || value != "present" {
			t.Fatal("the environment was not passed through")
		}
		if _, err := io.WriteString(streams.stdout(), "ran"); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	var stdout bytes.Buffer
	code := execute(context.Background(), []string{"migrate", "--check", "fast"}, func(key string) (string, bool) {
		return "present", key == "PGBASE_TEST"
	}, testIO(nil, &stdout, io.Discard), commandTable{"migrate": handler})
	if code != 0 || !called || stdout.String() != "ran" {
		t.Fatalf("code=%d called=%t stdout=%q", code, called, stdout.String())
	}
}

func TestExecuteReturnsExitCodeOneWhenACommandFails(t *testing.T) {
	var stderr bytes.Buffer
	code := execute(context.Background(), []string{"doctor"}, emptyEnv, testIO(nil, io.Discard, &stderr),
		commandTable{"doctor": func(context.Context, []string, environment, IO) error {
			return errors.New("database unavailable")
		}})
	if code != 1 || !strings.Contains(stderr.String(), "command failed") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestRunSanitizesOperationalErrors(t *testing.T) {
	const dsn = "postgres://admin:super-secret@db.example/pgbase"
	const sqlState = "SQLSTATE 42P01"
	const sql = "select secret_column from pgbase_meta.admins"

	var stderr bytes.Buffer
	code := execute(context.Background(), []string{"migrate"}, emptyEnv, testIO(nil, io.Discard, &stderr), commandTable{
		"migrate": func(context.Context, []string, environment, IO) error {
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

// TestExecuteDispatchesTheHiddenHealthcheck keeps the container health probe
// reachable even though it is deliberately absent from the usage line.
func TestExecuteDispatchesTheHiddenHealthcheck(t *testing.T) {
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"healthcheck"}, emptyEnv, testIO(nil, io.Discard, &stderr))
	if code != 2 || !strings.Contains(stderr.String(), "usage: pgbase healthcheck") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestHealthcheckAcceptsOnlyHTTP200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"healthcheck", "--url", server.URL}, emptyEnv, testIO(nil, io.Discard, &stderr))
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
			code := Execute(context.Background(), []string{"healthcheck", "--url", server.URL}, emptyEnv, testIO(nil, io.Discard, &stderr))
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
	code := Execute(context.Background(), []string{"healthcheck", "--url", server.URL, "--timeout", "25ms"}, emptyEnv, testIO(nil, io.Discard, &stderr))
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
	code := Execute(context.Background(), []string{"healthcheck", "--url", server.URL}, emptyEnv, testIO(nil, io.Discard, &stderr))
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
	code := Execute(context.Background(), []string{"healthcheck", "--url", "://not-a-url"}, emptyEnv, testIO(nil, io.Discard, &stderr))
	if code != 2 || stderr.String() != "pgbase: invalid healthcheck URL\n" {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestHealthcheckIsExcludedFromOrdinaryHelp(t *testing.T) {
	var stderr bytes.Buffer
	code := Execute(context.Background(), nil, emptyEnv, testIO(nil, io.Discard, &stderr))
	if code != 2 || strings.Contains(stderr.String(), "healthcheck") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestMigrateRejectsInvalidConfigurationWithExitCodeTwo(t *testing.T) {
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"migrate", "status"}, emptyEnv, testIO(nil, io.Discard, &stderr))
	if code != 2 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.String() != "pgbase: invalid configuration\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestServeRejectsInvalidConfigurationWithExitCodeTwo(t *testing.T) {
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"serve"}, emptyEnv, testIO(nil, io.Discard, &stderr))
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
			code := Execute(context.Background(), []string{"serve"}, migrationEnv(test.databaseURL), testIO(nil, io.Discard, &stderr))
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
	code := Execute(context.Background(), []string{"migrate", "status"}, migrationEnv(databaseURL), testIO(nil, io.Discard, &stderr))
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

func TestMigrateRejectsAnUndersizedPoolAsConfiguration(t *testing.T) {
	const databaseURL = "postgres://admin@127.0.0.1/pgbase?sslmode=disable&pool_max_conns=3"
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"migrate", "status"}, migrationEnv(databaseURL), testIO(nil, io.Discard, &stderr))
	if code != 2 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "pool_max_conns must be at least 4") ||
		!strings.HasSuffix(stderr.String(), "pgbase: invalid configuration\n") {
		t.Fatalf("stderr = %q, want actionable configuration rejection", stderr.String())
	}
}

func TestAdminRejectsMalformedDatabaseURLWithoutLeakingIt(t *testing.T) {
	const secret = "admin-configuration-secret-must-not-leak"
	const databaseURL = "postgres://admin:" + secret + "@localhost/pgbase?pool_max_conns=not-a-number"
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"admin", "create", "--email", "admin@example.com", "--password-stdin"},
		migrationEnv(databaseURL), testIO(strings.NewReader("correct horse battery staple\n"), &stdout, &stderr))
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
	code := Execute(context.Background(), []string{"doctor"}, migrationEnv(databaseURL), testIO(nil, &stdout, &stderr))
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
	code := Execute(context.Background(), []string{"migrate", "status"}, migrationEnv(databaseURL), testIO(nil, io.Discard, &stderr))
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
	code := Execute(context.Background(), []string{"migrate", "down"}, migrationEnv("postgres://localhost/unused"), testIO(nil, io.Discard, &stderr))
	if code != 2 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: pgbase migrate") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestMigrateAcceptsExactTargetGrammarBeforeLoadingConfiguration(t *testing.T) {
	for _, args := range [][]string{
		{"migrate", "up", "--through", "5"},
		{"migrate", "up", "--through=5"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			lookupCalled := false
			lookup := func(string) (string, bool) {
				lookupCalled = true
				return "", false
			}
			var stderr bytes.Buffer
			code := Execute(context.Background(), args, lookup, testIO(nil, io.Discard, &stderr))
			if code != 2 || !lookupCalled {
				t.Fatalf("code=%d lookupCalled=%t stderr=%q", code, lookupCalled, stderr.String())
			}
			if stderr.String() != "pgbase: invalid configuration\n" {
				t.Fatalf("stderr = %q", stderr.String())
			}
		})
	}
}

func TestMigrateRejectsInvalidExactTargetGrammarBeforeLoadingConfiguration(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing version", args: []string{"migrate", "up", "--through"}},
		{name: "zero", args: []string{"migrate", "up", "--through", "0"}},
		{name: "negative", args: []string{"migrate", "up", "--through", "-1"}},
		{name: "nonnumeric", args: []string{"migrate", "up", "--through", "five"}},
		{name: "overflow", args: []string{"migrate", "up", "--through", "9223372036854775808"}},
		{name: "duplicate", args: []string{"migrate", "up", "--through", "5", "--through=5"}},
		{name: "extra positional", args: []string{"migrate", "up", "--through", "5", "extra"}},
		{name: "bare flag terminator", args: []string{"migrate", "up", "--"}},
		{name: "single dash alias", args: []string{"migrate", "up", "-through", "5"}},
		{name: "status flag", args: []string{"migrate", "status", "--through", "5"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lookupCalled := false
			lookup := func(string) (string, bool) {
				lookupCalled = true
				return "", false
			}
			var stderr bytes.Buffer
			code := Execute(context.Background(), test.args, lookup, testIO(nil, io.Discard, &stderr))
			if code != 2 || lookupCalled {
				t.Fatalf("code=%d lookupCalled=%t stderr=%q", code, lookupCalled, stderr.String())
			}
			if !strings.Contains(stderr.String(), "usage: pgbase migrate") {
				t.Fatalf("stderr = %q", stderr.String())
			}
		})
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
