//go:build integration

package pgbase

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/membership"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/schema"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

func TestNewIncludesEmbeddedAdminUI(t *testing.T) {
	app, err := New(context.Background(), Options{
		DatabaseURL: testutil.NewDatabase(t),
		TokenSecret: make([]byte, 32),
		AutoMigrate: true,
		Logger:      slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = app.Close(context.Background()) })

	request := httptest.NewRequest(http.MethodGet, "/admin", nil)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusPermanentRedirect || response.Header().Get("Location") != "/admin/" {
		t.Fatalf("status=%d location=%q body=%q", response.Code, response.Header().Get("Location"), response.Body.String())
	}
}

func TestMetricsEndpointIsOptInAndBearerProtected(t *testing.T) {
	newApp := func(t *testing.T, metricsToken []byte) *App {
		t.Helper()
		app, err := New(context.Background(), Options{
			DatabaseURL:  testutil.NewDatabase(t),
			TokenSecret:  make([]byte, 32),
			MetricsToken: metricsToken,
			AutoMigrate:  true,
			Logger:       slog.New(slog.NewJSONHandler(io.Discard, nil)),
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = app.Close(context.Background()) })
		return app
	}

	disabled := newApp(t, nil)
	disabledResponse := httptest.NewRecorder()
	disabled.Handler().ServeHTTP(disabledResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if disabledResponse.Code != http.StatusNotFound {
		t.Fatalf("disabled metrics status = %d, want 404", disabledResponse.Code)
	}

	token := strings.Repeat("m", 32)
	enabled := newApp(t, []byte(token))
	unauthorized := httptest.NewRecorder()
	enabled.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized metrics status = %d, want 401", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	authorized := httptest.NewRecorder()
	enabled.Handler().ServeHTTP(authorized, request)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized metrics status = %d body=%s", authorized.Code, authorized.Body.String())
	}
	if authorized.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("metrics Cache-Control = %q", authorized.Header().Get("Cache-Control"))
	}
	if !strings.Contains(authorized.Body.String(), "pgbase_database_pool_max_connections") {
		t.Fatalf("metrics omitted database pool snapshot: %s", authorized.Body.String())
	}
	for _, initialized := range []string{
		`pgbase_application_readiness{component="database"} 1`,
		`pgbase_application_readiness{component="migrations"} 1`,
		`pgbase_application_readiness{component="http"} 0`,
		`pgbase_application_readiness{component="schema_listener"} 0`,
	} {
		if !strings.Contains(authorized.Body.String(), initialized) {
			t.Fatalf("metrics omitted initialized readiness %q: %s", initialized, authorized.Body.String())
		}
	}
}

func TestStartEnablesHandlerForCallerOwnedHTTPServer(t *testing.T) {
	app, err := New(context.Background(), Options{
		DatabaseURL: testutil.NewDatabase(t),
		TokenSecret: make([]byte, 32),
		AutoMigrate: true,
		Logger:      slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_ = app.Close(closeCtx)
	})

	deadline := time.Now().Add(3 * time.Second)
	for {
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
		if response.Code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("caller-owned handler never became ready: status=%d body=%s", response.Code, response.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := app.Run(context.Background()); !errors.Is(err, errAppAlreadyStarted) {
		t.Fatalf("Run after Start error = %v, want already started", err)
	}
}

func TestNewConfiguresBoundedHTTPReadWriteAndIdleTimeouts(t *testing.T) {
	app, err := New(context.Background(), Options{
		DatabaseURL: testutil.NewDatabase(t),
		TokenSecret: make([]byte, 32),
		AutoMigrate: true,
		Logger:      slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close(context.Background()) })
	if app.httpServer.ReadHeaderTimeout <= 0 || app.httpServer.ReadTimeout <= 0 || app.httpServer.WriteTimeout <= 0 || app.httpServer.IdleTimeout <= 0 {
		t.Fatalf("HTTP timeouts are not fully bounded: %+v", app.httpServer)
	}
}

func TestRunCancellationClosesListenerAndPool(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	address := unusedAddress(t)
	app, err := New(context.Background(), Options{
		DatabaseURL: databaseURL,
		HTTPAddress: address,
		TokenSecret: make([]byte, 32),
		AutoMigrate: true,
		Logger:      slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	waitForLive(t, "http://"+address+"/health/live")

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
	if connection, err := net.DialTimeout("tcp", address, time.Second); err == nil {
		connection.Close()
		t.Fatal("listener still accepts connections")
	}
	if _, err := app.pool.Acquire(context.Background()); err == nil {
		t.Fatal("pool acquired a connection after shutdown")
	}
}

func TestNewRejectsDatabaseWithPendingMigrationsWhenAutoMigrateIsDisabled(t *testing.T) {
	app, err := New(context.Background(), Options{
		DatabaseURL: testutil.NewDatabase(t),
		TokenSecret: make([]byte, 32),
		Logger:      slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err == nil {
		app.Close(context.Background())
		t.Fatal("New succeeded with pending migrations")
	}
}

func TestReleaseBNewAcceptsOnlyCleanPostMembershipHistory(t *testing.T) {
	migrations := system.Migrations()
	if len(migrations) != 8 {
		t.Fatalf("record-membership release-B manifest = %#v", migrations)
	}
	tests := []struct {
		name        string
		through     int
		dirty       bool
		autoMigrate bool
		populate    bool
		wantAccept  bool
	}{
		{name: "v5 rejected", through: 5},
		{name: "v5 rejected without auto-upgrade", through: 5, autoMigrate: true},
		{name: "v6 rejected", through: 6},
		{name: "populated v6 rejected without auto-upgrade", through: 6, autoMigrate: true, populate: true},
		// v7 and v8 are the two histories this artifact declares: it must serve
		// the fleet both before and after the companion index migration.
		{name: "v7 accepted", through: 7, wantAccept: true},
		{name: "v8 accepted", through: 8, wantAccept: true},
		{name: "dirty v6 rejected", through: 6, dirty: true},
		{name: "dirty v7 rejected", through: 7, dirty: true},
		{name: "dirty v8 rejected", through: 8, dirty: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			databaseURL := testutil.NewDatabase(t)
			testutil.ApplySystemMigrationPrefix(t, databaseURL, migrations[:testCase.through])
			if testCase.dirty || testCase.populate {
				pool, err := database.Open(context.Background(), databaseURL, time.Second)
				if err != nil {
					t.Fatal(err)
				}
				if testCase.dirty {
					if _, err := pool.Exec(context.Background(), `update pgbase_meta.system_migrations
						set checksum = decode(repeat('00', 32), 'hex') where version = 1`); err != nil {
						pool.Close()
						t.Fatal(err)
					}
				}
				if testCase.populate {
					if _, err := pool.Exec(context.Background(), `CREATE TABLE app.existing_records (
						id uuid PRIMARY KEY,
						created_at timestamptz NOT NULL,
						updated_at timestamptz NOT NULL
					);
					INSERT INTO pgbase_meta.collections
						(id, name, table_name, status, created_at, updated_at)
					VALUES ('019551d0-e9c0-7000-8000-000000000701', 'existing_records',
						'existing_records', 'active', transaction_timestamp(), transaction_timestamp());
					INSERT INTO app.existing_records (id, created_at, updated_at)
					VALUES ('019551d0-e9c0-7000-8000-000000000702',
						transaction_timestamp(), transaction_timestamp())`); err != nil {
						pool.Close()
						t.Fatal(err)
					}
				}
				pool.Close()
			}
			app, err := New(context.Background(), Options{
				DatabaseURL: databaseURL,
				TokenSecret: make([]byte, 32),
				AutoMigrate: testCase.autoMigrate,
				Logger:      slog.New(slog.NewJSONHandler(io.Discard, nil)),
			})
			if testCase.wantAccept {
				if err != nil {
					t.Fatalf("New rejected Release-B history: %v", err)
				}
				status, statusErr := migrate.New(app.pool, slog.New(slog.NewJSONHandler(io.Discard, nil))).Status(context.Background())
				if statusErr != nil {
					_ = app.Close(context.Background())
					t.Fatal(statusErr)
				}
				if status.Current != int64(testCase.through) || !status.Compatible {
					_ = app.Close(context.Background())
					t.Fatalf("startup changed accepted Release-B history: %#v", status)
				}
				var membershipTable *string
				if queryErr := app.pool.QueryRow(context.Background(),
					"select to_regclass('pgbase_meta.record_membership_capability')::text",
				).Scan(&membershipTable); queryErr != nil {
					_ = app.Close(context.Background())
					t.Fatal(queryErr)
				}
				if (membershipTable != nil) != (testCase.through >= 7) {
					_ = app.Close(context.Background())
					t.Fatalf("membership table after v%d startup = %v", testCase.through, membershipTable)
				}
				if testCase.through >= 7 {
					var leases int
					if err := app.pool.QueryRow(context.Background(), `select count(*)
						from pgbase_meta.runtime_writer_leases
						where writer_version = 3 and lease_expires_at > clock_timestamp()`).Scan(&leases); err != nil {
						_ = app.Close(context.Background())
						t.Fatal(err)
					}
					if leases != 1 {
						_ = app.Close(context.Background())
						t.Fatalf("Release-C startup active writer leases = %d, want 1", leases)
					}
					runtime, ok := app.membershipWriter.(*membership.Runtime)
					if !ok || runtime.WriterVersion() != membership.WriterC {
						_ = app.Close(context.Background())
						t.Fatalf("membership writer = %T/%v, want Release C", app.membershipWriter, runtime)
					}
				}
				if err := app.Start(context.Background()); err != nil {
					_ = app.Close(context.Background())
					t.Fatalf("start app with feature history: %v", err)
				}
				deadline := time.Now().Add(3 * time.Second)
				for app.readiness.Ready(context.Background()) != nil {
					if time.Now().After(deadline) {
						_ = app.Close(context.Background())
						t.Fatal("app with feature history did not become ready")
					}
					time.Sleep(10 * time.Millisecond)
				}
				// Remove the required history, not the top one: at v8 the top row
				// is the optional migration, and dropping it leaves the clean v7
				// this artifact is supposed to keep serving. Removing v7 leaves
				// either a history below the range or a gap, both of which are
				// drift under any policy.
				if _, err := app.pool.Exec(context.Background(),
					"delete from pgbase_meta.system_migrations where version = 7",
				); err != nil {
					_ = app.Close(context.Background())
					t.Fatal(err)
				}
				if err := app.readiness.Ready(context.Background()); err == nil {
					_ = app.Close(context.Background())
					t.Fatalf("readiness accepted history drift after v%d startup", testCase.through)
				}
				if closeErr := app.Close(context.Background()); closeErr != nil {
					t.Fatal(closeErr)
				}
				return
			}
			if err == nil {
				_ = app.Close(context.Background())
				t.Fatal("New accepted history outside the exact v7 Release-A boundary")
			}
			if testCase.autoMigrate {
				pool, openErr := database.Open(context.Background(), databaseURL, time.Second)
				if openErr != nil {
					t.Fatal(openErr)
				}
				defer pool.Close()
				status, statusErr := migrate.New(pool, slog.New(slog.NewJSONHandler(io.Discard, nil))).Status(context.Background())
				if statusErr != nil {
					t.Fatal(statusErr)
				}
				if status.Current != int64(testCase.through) {
					t.Fatalf("rejected Release-A auto-migration changed history: %#v", status)
				}
			}
		})
	}
}

func TestNewAutoMigratePreflightsBeforeAndAfterMigrationsWithOneCache(t *testing.T) {
	var firstCache *schema.Cache
	preflightCalls := 0
	app, err := newWithCatalogPreflight(context.Background(), Options{
		DatabaseURL: testutil.NewDatabase(t),
		TokenSecret: make([]byte, 32),
		AutoMigrate: true,
		Logger:      slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}, func(ctx context.Context, pool *database.Pool, cache *schema.Cache) error {
		preflightCalls++
		if firstCache == nil {
			firstCache = cache
		} else if cache != firstCache {
			return errors.New("catalog preflight cache changed across migration")
		}
		var catalogTablesAvailable bool
		if err := pool.QueryRow(ctx, `select to_regclass('pgbase_meta.field_relations') is not null`).Scan(&catalogTablesAvailable); err != nil {
			return err
		}
		if want := preflightCalls == 2; catalogTablesAvailable != want {
			return fmt.Errorf("catalog tables available on preflight %d = %t, want %t", preflightCalls, catalogTablesAvailable, want)
		}
		return schema.PreflightCatalog(ctx, pool, cache)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(context.Background()) }()
	if preflightCalls != 2 {
		t.Fatalf("catalog preflight calls = %d, want two", preflightCalls)
	}
}

func TestNewRejectsLegacyCatalogAboveRolloutLimitBeforeReadiness(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	pool, err := database.Open(context.Background(), databaseURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.New(pool, nil).Up(context.Background()); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	collectionID := uuid.New()
	if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at)
		values ($1, 'legacy_catalog_limit', 'legacy_catalog_limit', 'active', current_timestamp, current_timestamp)`, collectionID); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	legacyDefault := strings.Repeat("x", (16<<20)+1)
	if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.fields
		(id, collection_id, name, column_name, field_type, required, is_unique, is_indexed,
		 has_default, default_json, ordinal, created_at)
		values ($1, $2, 'body', 'body', 'text', false, false, false,
		 true, to_jsonb($3::text), 0, current_timestamp)`, uuid.New(), collectionID, legacyDefault); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	pool.Close()

	app, err := New(context.Background(), Options{
		DatabaseURL: databaseURL,
		TokenSecret: make([]byte, 32),
		Logger:      slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if app != nil {
		_ = app.Close(context.Background())
		t.Fatal("New returned an application for an oversized legacy catalog")
	}
	if !errors.Is(err, schema.ErrCatalogLimitExceeded) {
		t.Fatalf("New error = %v, want ErrCatalogLimitExceeded", err)
	}
}

func TestSystemStatusReportsDirtyMigrationHistory(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	runner := migrate.New(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update pgbase_meta.system_migrations set checksum = decode(repeat('00', 32), 'hex') where version = 1"); err != nil {
		t.Fatal(err)
	}

	sampler := newSystemStatusSampler(pool, runner, nil, nil)
	sampler.sample(ctx)
	status := sampler.Status(ctx)
	if !status.MigrationDirty {
		t.Fatalf("system status = %#v, want dirty migration history", status)
	}
}

// Release C is only a deployable artifact if the writer the app registers
// carries every rollout transition it has to reach. Both proofs match the
// active lease set exactly and demand at least WriterB, so this also pins the
// floor: retirement does not raise it to WriterC, and migration 7 could not
// store that if it did, because the ready/v5 state fixes
// minimum_writer_version at 2. Release B therefore stays a lease-able rollback
// target after the point of no return.
func TestReleaseCAppRuntimeRetiresCursorV4WithoutRaisingTheWriterFloor(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	testutil.ApplySystemMigrationPrefix(t, databaseURL, system.Migrations())
	// An upgraded deployment carries data, which is exactly what makes the
	// rollout real: the fresh-install bootstrap refuses a database that holds a
	// record, so this exercises every hold point rather than the shortcut.
	seedUpgradedDeploymentRecord(t, databaseURL)
	ctx := context.Background()
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
	runtime, ok := app.membershipWriter.(*membership.Runtime)
	if !ok || runtime.WriterVersion() != membership.WriterC {
		t.Fatalf("membership writer = %T/%v, want Release C", app.membershipWriter, runtime)
	}
	proof := membership.FleetProof{Members: []membership.FleetMember{runtime.FleetMember()}}

	if _, err := membership.New(app.pool).Cutover(ctx, proof); err != nil {
		t.Fatalf("Cutover with the app's own fleet proof: %v", err)
	}
	for attempt := 0; ; attempt++ {
		progress, err := membership.New(app.pool).Backfill(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if progress.Done {
			break
		}
		if attempt == 8 {
			t.Fatalf("backfill did not finish: %#v", progress)
		}
	}
	if _, err := membership.New(app.pool).PromoteReady(ctx); err != nil {
		t.Fatal(err)
	}
	activation, err := membership.New(app.pool).EnableV5(ctx, proof)
	if err != nil {
		t.Fatalf("EnableV5 with the app's own fleet proof: %v", err)
	}
	if activation.MinimumWriterVersion != membership.WriterB {
		t.Fatalf("activation minimum writer version = %v, want Release B", activation.MinimumWriterVersion)
	}

	var issueVersion int
	if err := app.pool.QueryRow(ctx, `select cursor_issue_version
		from pgbase_meta.record_membership_capability where singleton`).Scan(&issueVersion); err != nil {
		t.Fatal(err)
	}
	if issueVersion != 5 {
		t.Fatalf("cursor issue version after activation = %d, want 5", issueVersion)
	}

	if _, err := membership.New(app.pool).RetireV4(ctx, proof); !errors.Is(err, membership.ErrCursorDrainPending) {
		t.Fatalf("retirement before the cursor TTL drained = %v", err)
	}
	drainCursorTTL(t, ctx, app)
	retirement, err := membership.New(app.pool).RetireV4(ctx, proof)
	if err != nil {
		t.Fatalf("RetireV4 with the app's own fleet proof: %v", err)
	}
	if retirement.RetiredAt.IsZero() {
		t.Fatalf("retirement receipt = %#v", retirement)
	}

	status, err := membership.New(app.pool).Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.MinimumWriterVersion != membership.WriterB {
		t.Fatalf("minimum writer version after retirement = %v, want Release B",
			status.MinimumWriterVersion)
	}
	if _, err := membership.RegisterWriter(ctx, membership.New(app.pool), membership.WriterB); err != nil {
		t.Fatalf("register the Release B rollback target after retirement: %v", err)
	}
}

// The catalog rows are written directly because the rollout has to start
// before the app does, and the app is what owns the schema service.
func seedUpgradedDeploymentRecord(t *testing.T, databaseURL string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(ctx); err != nil {
			t.Errorf("close upgraded deployment seed connection: %v", err)
		}
	}()
	collectionID := uuid.MustParse("0199b800-0000-7000-8000-000000000001")
	for _, statement := range []struct {
		sql       string
		arguments []any
	}{
		{sql: `insert into pgbase_meta.collections
			(id, name, table_name, status, created_at, updated_at)
			values ($1, 'upgraded_records', 'upgraded_records', 'active',
				transaction_timestamp(), transaction_timestamp())`,
			arguments: []any{collectionID}},
		{sql: `insert into pgbase_meta.collection_creation_counters
			(collection_id, generation, last_ordinal, updated_at)
			values ($1, 0, 0, transaction_timestamp())`,
			arguments: []any{collectionID}},
		{sql: `insert into pgbase_meta.record_membership_backfills
			(collection_id, generation, state, updated_at)
			values ($1, 0, 'off', transaction_timestamp())`,
			arguments: []any{collectionID}},
		{sql: `create table app.upgraded_records (
			id uuid primary key,
			created_at timestamptz not null,
			updated_at timestamptz not null
		)`},
		{sql: `insert into app.upgraded_records (id, created_at, updated_at)
			values ($1, transaction_timestamp(), transaction_timestamp())`,
			arguments: []any{uuid.MustParse("0199b800-0000-7000-8000-0000000000a1")}},
	} {
		if _, err := conn.Exec(ctx, statement.sql, statement.arguments...); err != nil {
			t.Fatalf("seed upgraded deployment: %v", err)
		}
	}
}

// An operator who installs pgBase today has nothing to roll out, so startup
// has to leave the database in the state Release C can actually serve. The
// capability is not a migration, so how the schema arrived is irrelevant: the
// bootstrap runs where the writer registers, which is after startup has
// already required a compatible history on both paths.
func TestNewBootstrapsAFreshInstallToTheRetiredTerminalStateOnEitherMigrationPath(t *testing.T) {
	for _, test := range []struct {
		name        string
		autoMigrate bool
		preMigrated bool
	}{
		{name: "auto-migrated by the application", autoMigrate: true},
		{name: "migrated by hand with auto-migration off", preMigrated: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			databaseURL := testutil.NewDatabase(t)
			if test.preMigrated {
				testutil.ApplySystemMigrationPrefix(t, databaseURL, system.Migrations())
			}
			ctx := context.Background()
			app, err := New(ctx, Options{
				DatabaseURL: databaseURL,
				TokenSecret: make([]byte, 32),
				AutoMigrate: test.autoMigrate,
				Logger:      slog.New(slog.NewJSONHandler(io.Discard, nil)),
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			t.Cleanup(func() {
				if closeErr := app.Close(context.Background()); closeErr != nil {
					t.Errorf("close app: %v", closeErr)
				}
			})
			status, err := membership.New(app.pool).Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if status.State != membership.StateReady || status.CursorIssueVersion != 5 ||
				status.V4RetiredAt == nil || status.Generation != 1 ||
				status.MinimumWriterVersion != membership.WriterB {
				t.Fatalf("fresh install capability = %#v, want the retired terminal state", status)
			}
		})
	}
}

// Backdating the whole timeline is the only way to observe the drained TTL
// without waiting for it; the v7 timeline CHECK keeps the order intact.
func drainCursorTTL(t *testing.T, ctx context.Context, app *App) {
	t.Helper()
	if _, err := app.pool.Exec(ctx, `update pgbase_meta.record_membership_capability set
		cutover_at = transaction_timestamp() - interval '20 minutes',
		ready_at = transaction_timestamp() - interval '19 minutes',
		v5_enabled_at = transaction_timestamp() - interval '16 minutes',
		v4_decode_until = transaction_timestamp() - interval '1 minute',
		updated_at = transaction_timestamp() where singleton`); err != nil {
		t.Fatal(err)
	}
}
