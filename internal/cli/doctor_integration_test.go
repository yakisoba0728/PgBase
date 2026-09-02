//go:build integration

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestDoctorJSONReportsHealthyMigratedDatabase(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	prepareDoctorDatabase(t, databaseURL)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"doctor", "--json"}, migrationEnv(databaseURL), &stdout, &stderr, DefaultCommands())
	if code != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var report struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || !report.OK {
		t.Fatalf("report=%q err=%v", stdout.String(), err)
	}
}

// Diagnostics can fail after findings have already been gathered, and the
// partial report is still printed. Without a truncation signal the operator
// cannot tell that report apart from a complete one.
func TestDoctorJSONSignalsTruncationAfterAPartialReport(t *testing.T) {
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	prepareDoctorDatabase(t, databaseURL)
	pool, err := database.Open(ctx, databaseURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// A tampered checksum makes migration history dirty, which appends the
	// first finding and marks the history incompatible so the catalog preflight
	// is skipped and the run reaches its transaction. Renaming a catalog column
	// then fails the collection query inside it.
	if _, err := pool.Exec(ctx, `update pgbase_meta.system_migrations
		set checksum = decode(repeat('00', 32), 'hex') where version = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `alter table pgbase_meta.collections
		rename column table_name to table_name_moved`); err != nil {
		t.Fatal(err)
	}
	pool.Close()

	var stdout, stderr bytes.Buffer
	code := Run(ctx, []string{"doctor", "--json"}, migrationEnv(databaseURL), &stdout, &stderr, DefaultCommands())
	if code != 1 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var report struct {
		OK       bool `json:"ok"`
		Findings []struct {
			Severity string `json:"severity"`
			Code     string `json:"code"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("stdout is not one JSON report: %q: %v", stdout.String(), err)
	}
	if report.OK || len(report.Findings) != 2 {
		t.Fatalf("report=%+v, want the history finding and a truncation signal", report)
	}
	if report.Findings[0].Code != "migration_checksum_mismatch" {
		t.Fatalf("findings[0]=%+v, want the finding gathered before the failure", report.Findings[0])
	}
	last := report.Findings[1]
	if last.Code != "diagnostic_failed" || last.Severity != "error" {
		t.Fatalf("findings[1]=%+v, want a trailing diagnostic_failed", last)
	}
	if !strings.Contains(stderr.String(), "run diagnostics") {
		t.Fatalf("doctor stderr lost the operational diagnostic: %q", stderr.String())
	}
	// stderr is the operator log and carries the SQLSTATE detail on purpose;
	// the report on stdout must stay free of connection and SQL detail even
	// though it is now assembled from a failed run.
	if strings.Contains(stdout.String(), databaseURL) || strings.Contains(stdout.String(), "table_name_moved") ||
		strings.Contains(stdout.String(), "42703") {
		t.Fatalf("doctor report leaked operational detail: %q", stdout.String())
	}
}
