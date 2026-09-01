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
	"github.com/webisnoteasy/PgBase/internal/database"
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

func TestNewAcceptsOnlySchemaOperationsP0Histories(t *testing.T) {
	migrations := system.Migrations()
	if len(migrations) != 6 {
		t.Fatalf("schema-operations P0 manifest = %#v", migrations)
	}
	tests := []struct {
		name       string
		through    int
		dirty      bool
		wantAccept bool
	}{
		{name: "v4 rejected", through: 4},
		{name: "v5 accepted", through: 5, wantAccept: true},
		{name: "v6 accepted", through: 6, wantAccept: true},
		{name: "dirty v5 rejected", through: 5, dirty: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			databaseURL := testutil.NewDatabase(t)
			testutil.ApplySystemMigrationPrefix(t, databaseURL, migrations[:testCase.through])
			if testCase.dirty {
				pool, err := database.Open(context.Background(), databaseURL, time.Second)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(context.Background(), `update pgbase_meta.system_migrations
					set checksum = decode(repeat('00', 32), 'hex') where version = 1`); err != nil {
					pool.Close()
					t.Fatal(err)
				}
				pool.Close()
			}
			app, err := New(context.Background(), Options{
				DatabaseURL: databaseURL,
				TokenSecret: make([]byte, 32),
				Logger:      slog.New(slog.NewJSONHandler(io.Discard, nil)),
			})
			if testCase.wantAccept {
				if err != nil {
					t.Fatalf("New rejected P0 history: %v", err)
				}
				if err := app.Start(context.Background()); err != nil {
					_ = app.Close(context.Background())
					t.Fatalf("start app with P0 history: %v", err)
				}
				deadline := time.Now().Add(3 * time.Second)
				for app.readiness.Ready(context.Background()) != nil {
					if time.Now().After(deadline) {
						_ = app.Close(context.Background())
						t.Fatal("app with P0 history did not become ready")
					}
					time.Sleep(10 * time.Millisecond)
				}
				if closeErr := app.Close(context.Background()); closeErr != nil {
					t.Fatal(closeErr)
				}
				return
			}
			if err == nil {
				_ = app.Close(context.Background())
				t.Fatal("New accepted history outside the P0 compatibility boundary")
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

	status := (systemStatusState{pool: pool, runner: runner}).Status(ctx)
	if !status.MigrationDirty {
		t.Fatalf("system status = %#v, want dirty migration history", status)
	}
}
