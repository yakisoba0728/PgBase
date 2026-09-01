//go:build integration

package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

func TestMigrateUpAndStatus(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	lookup := migrationEnv(databaseURL)
	migrations := system.Migrations()
	if len(migrations) == 0 {
		t.Fatal("embedded system migrations are empty")
	}
	latest := migrations[len(migrations)-1].Version
	wantCurrent := fmt.Sprintf("current=%d", latest)
	wantLatest := fmt.Sprintf("latest=%d", latest)

	var upOutput bytes.Buffer
	if code := Run(context.Background(), []string{"migrate", "up"}, lookup, &upOutput, io.Discard, DefaultCommands()); code != 0 {
		t.Fatalf("migrate up exit code = %d, stdout = %q", code, upOutput.String())
	}
	if !strings.Contains(upOutput.String(), wantCurrent) || !strings.Contains(upOutput.String(), wantLatest) {
		t.Fatalf("migrate up stdout = %q", upOutput.String())
	}

	var statusOutput bytes.Buffer
	if code := Run(context.Background(), []string{"migrate", "status"}, lookup, &statusOutput, io.Discard, DefaultCommands()); code != 0 {
		t.Fatalf("migrate status exit code = %d, stdout = %q", code, statusOutput.String())
	}
	if !strings.Contains(statusOutput.String(), wantCurrent) || !strings.Contains(statusOutput.String(), "dirty=false") {
		t.Fatalf("migrate status stdout = %q", statusOutput.String())
	}
}
