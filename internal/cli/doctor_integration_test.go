//go:build integration

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

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
