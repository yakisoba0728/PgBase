package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
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

func TestDoctorRejectsInvalidFlagsWithExitCodeTwo(t *testing.T) {
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"doctor", "--unexpected"}, emptyEnv, io.Discard, &stderr, DefaultCommands())
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
