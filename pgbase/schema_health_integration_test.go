//go:build integration

package pgbase

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/api"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

// TestStartedAppDiagnosesSchemaHealthThroughTheNamedTaskRegistry pins the
// registration and the state a sound database produces: the diagnosis runs
// because the app's background registry started it, and a migrated database
// with nothing wrong reports healthy with a success time rather than the
// unavailable zero it carried before anything had been observed.
func TestStartedAppDiagnosesSchemaHealthThroughTheNamedTaskRegistry(t *testing.T) {
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
	if app.schemaHealth == nil {
		t.Fatal("the app did not construct a schema health sampler")
	}
	before := app.schemaHealth.Health(ctx)
	if before.State != api.SchemaHealthUnavailable {
		t.Fatalf("health before startup = %#v, want an undiagnosed schema", before)
	}

	go func() { _ = app.Start(ctx) }()
	select {
	case <-app.schemaHealth.sampled:
	case <-time.After(30 * time.Second):
		t.Fatal("the started app never diagnosed schema health")
	}

	health := app.schemaHealth.Health(ctx)
	if health.State != api.SchemaHealthHealthy {
		t.Fatalf("health = %#v, want a healthy diagnosis", health)
	}
	if health.FindingCount != 0 || len(health.Findings) != 0 || len(health.Unavailable) != 0 {
		t.Fatalf("a healthy diagnosis reported a problem: %#v", health)
	}
	// The zero finding count is only readable as health because the run that
	// produced it recorded when it completed.
	if health.LastSuccessAt == "" || health.SampledAt == "" {
		t.Fatalf("a healthy diagnosis has no run time: %#v", health)
	}
	if health.Retries != 0 {
		t.Fatalf("retries = %d on a completed run", health.Retries)
	}
}

// A catalog collection with no table behind it is the drift doctor exists to
// find, and the sampler must publish it as drift — a third state, neither the
// healthy run above nor the unavailable one a failed run produces.
func TestSchemaHealthPublishesTheDoctorsDriftFindings(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	runner := migrate.New(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}

	sampler := newSchemaHealthSampler(pool, runner)
	sampler.sample(ctx)
	if healthy := sampler.Health(ctx); healthy.State != api.SchemaHealthHealthy {
		t.Fatalf("health of an untouched database = %#v, want healthy", healthy)
	}

	// A catalog collection whose table was never created, which is exactly the
	// catalog-versus-physical disagreement doctor compares for.
	for _, statement := range []string{
		`insert into pgbase_meta.collections (id, name, table_name, status, created_at, updated_at)
			values ('11111111-1111-4111-8111-111111111111', 'posts', 'posts', 'active', current_timestamp, current_timestamp)`,
		`insert into pgbase_meta.collection_creation_counters (collection_id, generation, last_ordinal, updated_at)
			values ('11111111-1111-4111-8111-111111111111', 0, 0, transaction_timestamp())`,
		`insert into pgbase_meta.record_membership_backfills (collection_id, generation, state, updated_at)
			values ('11111111-1111-4111-8111-111111111111', 0, 'off', transaction_timestamp())`,
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	sampler.sample(ctx)

	health := sampler.Health(ctx)
	if health.State != api.SchemaHealthDrift {
		t.Fatalf("health after removing a collection table = %#v, want drift", health)
	}
	if len(health.Findings) == 0 {
		t.Fatalf("drift reported no finding: %#v", health)
	}
	for _, finding := range health.Findings {
		if finding.Code == "" || finding.Message == "" {
			t.Fatalf("finding %#v is missing its stable code or operator message", finding)
		}
		// The whole report is safe to render only because no finding names an
		// object: the drifted collection is called "posts" and must not appear.
		if containsAny(finding.Code+finding.Message, "posts", "pgbase_meta", "select ", "insert ") {
			t.Fatalf("finding %#v names the object or the SQL that produced it", finding)
		}
	}
	if health.LastSuccessAt == "" || health.Retries != 0 {
		t.Fatalf("drift was recorded as a failed run: %#v", health)
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(strings.ToLower(value), needle) {
			return true
		}
	}
	return false
}
