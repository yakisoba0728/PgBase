//go:build integration

package pgbase

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

// TestStartedAppSamplesSystemStatusOnceThroughTheNamedTaskRegistry pins the
// registration: the sampler runs because the app's background registry started
// it, and its own snapshot is what the status endpoint serves.
func TestStartedAppSamplesSystemStatusOnceThroughTheNamedTaskRegistry(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	testutil.ApplySystemMigrationPrefix(t, databaseURL, system.Migrations())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app, err := New(ctx, Options{
		DatabaseURL: databaseURL,
		TokenSecret: make([]byte, 32),
		Logger:      slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := app.Close(context.Background()); closeErr != nil {
			t.Errorf("close app: %v", closeErr)
		}
	})
	if app.systemStatus == nil {
		t.Fatal("the app did not construct a system status sampler")
	}
	before := app.systemStatus.Status(ctx)
	if !probeReported(before, systemComponentDatabase) {
		t.Fatalf("status before startup = %#v, want the database reported as unsampled", before)
	}

	go func() { _ = app.Start(ctx) }()
	select {
	case <-app.systemStatus.sampled:
	case <-time.After(15 * time.Second):
		t.Fatal("the started app never produced a system status sample")
	}

	status := app.systemStatus.Status(ctx)
	if !status.DatabaseReachable {
		t.Fatalf("sampled status = %#v, want a reachable database", status)
	}
	if status.MigrationCurrent == 0 || status.MigrationCurrent != status.MigrationLatest {
		t.Fatalf("sampled status = %#v, want a current migration history", status)
	}
	if status.SampledAt == "" {
		t.Fatalf("sampled status = %#v, want an attempt time", status)
	}
	if probeReported(status, systemComponentDatabase) || probeReported(status, systemComponentMigration) ||
		probeReported(status, systemComponentCatalog) {
		t.Fatalf("sampled status = %#v, want the reachable probes reported as available", status)
	}
}
