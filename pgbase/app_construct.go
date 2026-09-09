package pgbase

import (
	"context"
	"errors"
	"fmt"
	"github.com/webisnoteasy/PgBase/internal/adminauth"
	"github.com/webisnoteasy/PgBase/internal/api"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/membership"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/internal/publicsite"
	"github.com/webisnoteasy/PgBase/internal/realtime"
	"github.com/webisnoteasy/PgBase/internal/recordaccess"
	"github.com/webisnoteasy/PgBase/internal/recordops"
	"github.com/webisnoteasy/PgBase/internal/schema"
	"github.com/webisnoteasy/PgBase/internal/usermigrate"
	adminui "github.com/webisnoteasy/PgBase/ui/admin"
	"net"
	"net/http"
	"time"
)

// New validates options, establishes PostgreSQL connectivity, verifies the
// system schema, and builds an inactive HTTP application. It does not start
// pgBase background workers or an HTTP listener; the PostgreSQL pool may run
// its own internal maintenance goroutines. Callers must invoke Start or Run.
func New(ctx context.Context, options Options) (*App, error) {
	return newWithCatalogPreflight(ctx, options, schema.PreflightCatalog)
}

func newWithCatalogPreflight(
	ctx context.Context,
	options Options,
	preflight func(context.Context, *database.Pool, *schema.Cache) error,
) (*App, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, err := options.Normalized()
	if err != nil {
		return nil, err
	}
	gate, err := database.TryBackupGate(ctx, normalized.DatabaseURL, false)
	if err != nil {
		return nil, fmt.Errorf("acquire PostgreSQL writer gate: %w", err)
	}
	keepGate := false
	defer func() {
		if !keepGate {
			_ = gate.Close(context.Background())
		}
	}()
	pool, err := database.Open(ctx, normalized.DatabaseURL, normalized.StatementTimeout, normalized.AllowInsecureDatabase)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}
	keepPool := false
	defer func() {
		if !keepPool {
			pool.Close()
		}
	}()

	var metrics *observability.Metrics
	var metricsExposition http.Handler
	if len(normalized.MetricsToken) > 0 {
		metrics = observability.NewMetrics(pool.Stats)
		// Built here so the bearer secret stops at composition. The metrics
		// module hashes it immediately and keeps only the hash.
		metricsExposition = metrics.Handler(normalized.MetricsToken)
	}
	runner := migrate.NewWithRecorder(pool, normalized.Logger, metrics)
	schemaCache := &schema.Cache{}
	if normalized.AutoMigrate {
		if err := preflight(ctx, pool, schemaCache); err != nil {
			return nil, fmt.Errorf("verify schema catalog rollout: %w", err)
		}
		before, err := runner.Status(ctx)
		if err != nil {
			return nil, fmt.Errorf("read system migration status before auto-migration: %w", err)
		}
		if before.Current != 0 && !before.Compatible {
			return nil, errors.New("system migrations are not current")
		}
		// An existing database is accepted only on the checksum-pinned history
		// the compatibility policy declares. A designated exact-target job must
		// cross the boundary to the next version; auto-migration remains
		// available only for a fresh database bootstrap.
		if before.Current == 0 {
			if err := runner.Up(ctx); err != nil {
				return nil, fmt.Errorf("apply system migrations: %w", err)
			}
		}
	}
	status, err := runner.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("read system migration status: %w", err)
	}
	if !status.Compatible {
		return nil, errors.New("system migrations are not current")
	}
	if err := preflight(ctx, pool, schemaCache); err != nil {
		return nil, fmt.Errorf("verify schema catalog rollout: %w", err)
	}
	if metrics != nil {
		metrics.Record(ctx, observability.ReadinessEvent(observability.ReadinessDatabase, true))
		metrics.Record(ctx, observability.ReadinessEvent(observability.ReadinessMigrations, true))
	}

	readiness := &readinessState{recorder: metrics, dependencies: []api.Readiness{
		namedReadiness{name: "database", check: readinessFunc(pool.Ping)},
		namedReadiness{name: "migrations", check: readinessFunc(func(ctx context.Context) error {
			status, err := runner.Status(ctx)
			if err != nil {
				return err
			}
			if !status.Compatible {
				return errors.New("system migrations are not current")
			}
			return nil
		})},
	}}
	schemaService := schema.NewService(pool, schemaCache, metrics, normalized.TokenSecret)
	var userMigrationRecorder *usermigrate.AutoRecorder
	if normalized.UserMigrationAutoRecordDirectory != "" {
		userMigrationRecorder, err = usermigrate.NewAutoRecorder(pool, normalized.UserMigrationAutoRecordDirectory, normalized.Logger)
		if err != nil {
			return nil, fmt.Errorf("initialize automatic user migration recorder: %w", err)
		}
		schemaService.SetMigrationRecorder(userMigrationRecorder)
	}
	if normalized.Hooks != nil {
		schemaService.SetCollectionMutationLifecycle(schemaLifecycleHooks{normalized.Hooks})
	}
	schemaListener := schema.NewListener(pool, schemaCache, metrics, schemaService)
	readiness.dependencies = append(readiness.dependencies, schemaListener)
	membershipRuntime, err := membership.RegisterWriter(ctx, membership.New(pool), membership.WriterC)
	if err != nil {
		return nil, fmt.Errorf("register record membership writer: %w", err)
	}
	readiness.dependencies = append(readiness.dependencies,
		namedReadiness{name: "record membership writer", check: membershipRuntime})
	authService := adminauth.NewService(
		pool,
		adminauth.NewArgon2idPasswords(),
		normalized.TokenSecret,
		normalized.SetupToken,
		normalized.AdminSessionTTL,
		nil,
	)
	recordService := recordops.NewService(
		pool, schemaService, normalized.TokenSecret, normalized.Logger, metrics, membershipRuntime,
	)
	adminHandler, err := adminui.Handler()
	if err != nil {
		return nil, fmt.Errorf("initialize administrator UI: %w", err)
	}
	var publicHandler http.Handler
	if normalized.PublicDir != "" {
		publicHandler, err = publicsite.New(normalized.PublicDir, normalized.PublicSPAFallback)
		if err != nil {
			return nil, fmt.Errorf("initialize public static site: %w", err)
		}
	}
	systemStatus := newSystemStatusSampler(pool, runner, schemaCache, schemaListener)
	schemaHealth := newSchemaHealthSampler(pool, runner)
	recordService.SetHooks(applicationMutationHooks(normalized))
	jobService, err := newJobService(ctx, pool, normalized)
	if err != nil {
		return nil, err
	}
	realtimeService := realtime.NewService(pool, normalized.Logger, schemaService)
	requestLog := newRequestLog(pool, normalized)
	defer func() {
		if !keepPool {
			ctx, cancel := context.WithTimeout(context.Background(), normalized.ShutdownTimeout)
			defer cancel()
			_ = requestLog.Shutdown(ctx)
		}
	}()
	dependencies, err := withUserServices(api.Dependencies{
		Realtime:           realtimeService,
		RequestLog:         requestLog,
		RecordRules:        recordaccess.NewRuleService(pool, schemaService),
		Readiness:          readiness,
		Logger:             normalized.Logger,
		CORSAllowedOrigins: normalized.CORSAllowedOrigins,
		AdminAuth:          authService,
		CustomRoutes:       normalized.CustomRoutes,
		Schema:             schemaService,
		Records:            recordService,
		PublicURL:          normalized.PublicURL,
		TrustedProxies:     normalized.TrustedProxies,
		System:             systemStatus,
		SchemaHealth:       schemaHealth,
		AdminUI:            adminHandler,
		PublicStatic:       publicHandler,
		Metrics:            metrics,
		MetricsExposition:  metricsExposition,
		SchemaApplyTimeout: normalized.SchemaApplyTimeout,
	}, pool, normalized)
	if err != nil {
		return nil, err
	}
	if normalized.Hooks != nil {
		dependencies.RealtimeLifecycle = realtimeLifecycleHooks{normalized.Hooks}
		dependencies.LifecycleHooks = normalized.Hooks
	}
	dependencies, fileService, err := withFileServices(ctx, dependencies, pool, normalized, schemaService)
	if err != nil {
		return nil, err
	}
	recordService.SetTransactionValidators([]recordops.TransactionValidator{requiredFileValidator{schemas: schemaService}})
	dependencies, backupManager, err := withBackupServices(ctx, dependencies, fileService, pool, normalized)
	if err != nil {
		return nil, err
	}
	defer func() {
		if !keepPool && backupManager != nil {
			ctx, cancel := context.WithTimeout(context.Background(), normalized.ShutdownTimeout)
			defer cancel()
			_ = backupManager.Shutdown(ctx)
		}
	}()
	if jobService != nil {
		dependencies.Jobs = jobService
	}
	dependencies.MaximumFileUploadBytes = normalized.Files.MaximumBytes + 64<<10
	handler, err := api.NewRouter(dependencies)
	if err != nil {
		return nil, err
	}
	handler = requestHooks(normalized.Hooks, handler)
	app := &App{
		schemaCache:           schemaCache,
		options:               normalized,
		backupManager:         backupManager,
		requestLog:            dependencies.RequestLog,
		pool:                  pool,
		backupGate:            gate,
		handler:               handler,
		readiness:             readiness,
		cleaner:               adminauth.NewCleaner(pool, nil, normalized.Logger),
		recordCleaner:         recordops.NewIdempotencyCleaner(pool, normalized.Logger),
		realtimeWorker:        realtimeService,
		schemaOperations:      schemaService,
		schemaListener:        schemaListener,
		membershipWriter:      membershipRuntime,
		systemStatus:          systemStatus,
		schemaHealth:          schemaHealth,
		userMigrationSources:  userMigrationRecorder,
		userMigrationRecorder: userMigrationRecorder,
		listen:                net.Listen,
		lifecycleClosing:      make(chan struct{}),
	}
	if jobService != nil {
		app.jobWorker = jobService
	}
	if fileService != nil {
		app.fileWorker = fileService
	}
	if worker, ok := dependencies.StorageMigrations.(backgroundRunner); ok {
		app.storageMigrationWorker = worker
	}
	app.httpServer = &http.Server{
		Addr:              normalized.HTTPAddress,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	keepPool = true
	keepGate = true
	return app, nil
}
