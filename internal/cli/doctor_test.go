package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
)

func TestDoctorJSONAlwaysReportsDatabaseConnectionFailureAsJSON(t *testing.T) {
	const databaseURL = "postgres://pgbase:must-not-leak@127.0.0.1:1/pgbase?connect_timeout=1"
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"doctor", "--json"}, migrationEnv(databaseURL), testIO(nil, &stdout, &stderr))
	if code != 1 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var report struct {
		OK       bool `json:"ok"`
		Findings []struct {
			Code string `json:"code"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("stdout is not one JSON report: %q: %v", stdout.String(), err)
	}
	if report.OK || len(report.Findings) != 1 || report.Findings[0].Code != "database_unreachable" {
		t.Fatalf("report=%+v", report)
	}
	if strings.Contains(stdout.String(), "must-not-leak") || strings.Contains(stderr.String(), "must-not-leak") {
		t.Fatalf("doctor output leaked database credentials: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "open PostgreSQL pool") {
		t.Fatalf("doctor stderr lost the operational diagnostic: %q", stderr.String())
	}
}

func TestDoctorJSONReportsInvalidConfigurationBeforeDatabaseOpen(t *testing.T) {
	var stdout bytes.Buffer
	code := Execute(context.Background(), []string{"doctor", "--json"}, emptyEnv, testIO(nil, &stdout, io.Discard))
	if code != 2 {
		t.Fatalf("exit=%d stdout=%q", code, stdout.String())
	}
	var report struct {
		Findings []struct {
			Code string `json:"code"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || len(report.Findings) != 1 || report.Findings[0].Code != "configuration_invalid" {
		t.Fatalf("report=%q err=%v", stdout.String(), err)
	}
}

func TestDoctorRejectsInvalidFlagsWithExitCodeTwo(t *testing.T) {
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"doctor", "--unexpected"}, emptyEnv, testIO(nil, io.Discard, &stderr))
	if code != 2 || stderr.String() != "pgbase: usage: pgbase doctor [--json]\n" {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

func prepareDoctorDatabase(t *testing.T, databaseURL string) {
	t.Helper()
	pool, err := database.Open(context.Background(), databaseURL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(context.Background()); err != nil {
		t.Fatal(err)
	}
}
