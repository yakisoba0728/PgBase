package pgbase

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/api"
	"github.com/yakisoba0728/PgBase/internal/apiratelimit"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/membership"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/observability"
	"github.com/yakisoba0728/PgBase/internal/publicsite"
	"github.com/yakisoba0728/PgBase/internal/realtime"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/recordops"
	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/sqlconsole"
	"github.com/yakisoba0728/PgBase/internal/usermigrate"
	adminui "github.com/yakisoba0728/PgBase/ui/admin"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
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
	var javascript *javaScriptRuntime
	if normalized.JavaScript != nil {
		javascript, err = prepareJavaScript(*normalized.JavaScript)
		if err != nil {
			return nil, fmt.Errorf("initialize JavaScript: %w", err)
		}
		if err := installJavaScriptRoutes(&normalized, javascript); err != nil {
			_ = javascript.Close(context.Background())
			return nil, err
		}
		if len(javascript.bundle.Manifest().Hooks) > 0 && normalized.Hooks == nil {
			normalized.Hooks = &HookRegistry{}
		}
		if err := javascript.installHooks(normalized.Hooks); err != nil {
			_ = javascript.Close(context.Background())
			return nil, err
		}
		if err := installJavaScriptJobs(&normalized, javascript); err != nil {
			_ = javascript.Close(context.Background())
			return nil, err
		}
	}
	keepJavaScript := false
	defer func() {
		if !keepJavaScript && javascript != nil {
			shutdown, cancel := context.WithTimeout(context.Background(), normalized.ShutdownTimeout)
			defer cancel()
			_ = javascript.Close(shutdown)
		}
	}()
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
	var userMigrationSources backgroundRunner
	if normalized.UserMigrationAutoRecordDirectory != "" {
		userMigrationRecorder, err = usermigrate.NewAutoRecorder(pool, normalized.UserMigrationAutoRecordDirectory, normalized.Logger)
		if err != nil {
			return nil, fmt.Errorf("initialize automatic user migration recorder: %w", err)
		}
		schemaService.SetMigrationRecorder(userMigrationRecorder)
		userMigrationSources = userMigrationRecorder
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
	var sqlConsoleService *sqlconsole.Service
	var sqlConsoleHandlers *api.SQLConsoleHandlers
	if normalized.SQLConsole != nil {
		sqlConsoleService, sqlConsoleHandlers, err = newSQLConsole(ctx, pool, schemaCache.Load(), normalized)
		if err != nil {
			return nil, err
		}
	}
	keepSQLConsole := false
	defer func() {
		if !keepSQLConsole && sqlConsoleService != nil {
			shutdown, cancel := context.WithTimeout(context.Background(), normalized.ShutdownTimeout)
			defer cancel()
			_ = sqlConsoleService.Close(shutdown)
		}
	}()
	uiExtensions := make([]adminui.UIExtension, 0, len(normalized.UIExtensions))
	for _, extension := range normalized.UIExtensions {
		uiExtensions = append(uiExtensions, adminui.UIExtension{Name: extension.Name, FS: extension.FS})
	}
	adminHandler, err := adminui.Handler(uiExtensions)
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
	runtimeSettingsKey := normalized.RuntimeSettingsEncryptionKey
	if len(runtimeSettingsKey) == 0 {
		runtimeSettingsKey = normalized.TokenSecret
	}
	runtimeSettings, err := runtimesettings.New(ctx, pool, runtimeSettingsKey)
	if err != nil {
		return nil, fmt.Errorf("initialize runtime settings: %w", err)
	}
	apiLimits := apiratelimit.New(pool, normalized.TokenSecret, runtimeSettings.Get())
	runtimeSettings.Subscribe(apiLimits.Set)
	applyAdminRatePolicy := func(value runtimesettings.Settings) {
		p := value.RateLimits.Admin
		_ = authService.ConfigureRatePolicy(adminauth.RatePolicy{AccountMaxFailures: p.AccountMaxFailures, NetworkMaxFailures: p.NetworkMaxFailures, Window: time.Duration(p.WindowSeconds) * time.Second, Block: time.Duration(p.BlockSeconds) * time.Second})
	}
	applyAdminRatePolicy(runtimeSettings.Get())
	runtimeSettings.Subscribe(applyAdminRatePolicy)
	// Request diagnostics intentionally never retain IP addresses or auth
	// material. The persisted privacy switches remain a truthful declaration of
	// that fixed redaction boundary while retention is applied live.
	applyLogSettings := func(value runtimesettings.Settings) {
		retentionCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if applyErr := requestLog.SetRetention(retentionCtx, time.Duration(value.Logs.RetentionDays)*24*time.Hour); applyErr != nil {
			normalized.Logger.ErrorContext(retentionCtx, "apply request log retention", "error", applyErr)
		}
	}
	applyLogSettings(runtimeSettings.Get())
	runtimeSettings.Subscribe(applyLogSettings)
	defer func() {
		if !keepPool {
			ctx, cancel := context.WithTimeout(context.Background(), normalized.ShutdownTimeout)
			defer cancel()
			_ = requestLog.Shutdown(ctx)
		}
	}()
	applicationRealtimeBroker := api.NewApplicationRealtimeBroker()
	dependencies, extensionMailer, userService, err := withUserServices(api.Dependencies{
		Realtime:                  realtimeService,
		ApplicationRealtimeBroker: applicationRealtimeBroker,
		RequestLog:                requestLog,
		RuntimeSettings:           runtimeSettings,
		SettingsLifecycle:         settingsLifecycleHooks{normalized.Hooks},
		RecordRules:               recordaccess.NewRuleService(pool, schemaService),
		Readiness:                 readiness,
		Logger:                    normalized.Logger,
		CORSAllowedOrigins:        normalized.CORSAllowedOrigins,
		AdminAuth:                 authService,
		CustomRoutes:              normalized.CustomRoutes,
		Schema:                    schemaService,
		Records:                   recordService,
		PublicURL:                 normalized.PublicURL,
		TrustedProxies:            normalized.TrustedProxies,
		System:                    systemStatus,
		SchemaHealth:              schemaHealth,
		AdminUI:                   adminHandler,
		PublicStatic:              publicHandler,
		Metrics:                   metrics,
		MetricsExposition:         metricsExposition,
		SchemaApplyTimeout:        normalized.SchemaApplyTimeout,
		SQLConsole:                sqlConsoleHandlers,
		APIRateLimits:             apiLimits,
	}, pool, normalized)
	if err != nil {
		return nil, err
	}
	if extensionMailer != nil {
		if err := authService.ConfigurePasswordReset(extensionMailer, normalized.PublicURL.String(), time.Hour); err != nil {
			return nil, fmt.Errorf("configure administrator password reset: %w", err)
		}
		if err := authService.ConfigureEmailOTP(extensionMailer, 5*time.Minute); err != nil {
			return nil, fmt.Errorf("configure administrator email OTP: %w", err)
		}
	}
	if normalized.Hooks != nil {
		dependencies.RealtimeLifecycle = realtimeLifecycleHooks{normalized.Hooks}
		dependencies.LifecycleHooks = normalized.Hooks
	}
	dependencies, fileService, objectStore, err := withFileServices(ctx, dependencies, pool, normalized, schemaService)
	if err != nil {
		return nil, err
	}
	recordService.SetTransactionValidators([]recordops.TransactionValidator{requiredFileValidator{schemas: schemaService}})
	extensions := &extensionBridge{records: recordService, files: fileService, mailer: extensionMailer, authMail: userService, authAPI: userService, logger: normalized.Logger, hooks: normalized.Hooks, templates: normalized.Templates}
	if normalized.JavaScript != nil {
		extensions.compatibility = newExtensionCompatibility(normalized.JavaScript.PocketBaseCompatibility)
	}
	if objectStore != nil {
		extensions.objects = &ExtensionObjects{store: objectStore, pool: pool, maximumBytes: normalized.Files.MaximumBytes, hooks: normalized.Hooks}
	}
	if normalized.EnableTrustedDatabaseAccess {
		extensions.database = &TrustedDatabase{pool: pool}
	}
	if javascript != nil {
		javascript.bridge = extensions
	}
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
	if err := installManagedBackupSchedule(ctx, jobService, backupManager, runtimeSettings, func(ctx context.Context, err error) {
		normalized.Logger.ErrorContext(ctx, "reconfigure managed backup schedule", "error", err)
	}); err != nil {
		return nil, fmt.Errorf("install managed backup schedule: %w", err)
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
		realtimeBroker:        applicationRealtimeBroker,
		runtimeSettings:       runtimeSettings,
		pool:                  pool,
		records:               recordService,
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
		userMigrationSources:  userMigrationSources,
		userMigrationRecorder: userMigrationRecorder,
		sqlConsole:            sqlConsoleService,
		javaScriptRuntime:     javascript,
		extensions:            extensions,
		listen:                net.Listen,
		lifecycleClosing:      make(chan struct{}),
	}
	bindRuntimeSettingsLifecycle(app, runtimeSettings)
	extensions.appControl = newExtensionAppControl(app, normalized.JavaScript)
	extensions.requestLog = app.requestLog
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
	if normalized.TLS != nil {
		tlsOptions := *normalized.TLS
		if len(tlsOptions.ACMEHosts) == 0 {
			certificate, err := tls.LoadX509KeyPair(tlsOptions.CertificateFile, tlsOptions.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("load TLS certificate: %w", err)
			}
			app.httpServer.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
			app.serve = func(listener net.Listener) error { return app.httpServer.ServeTLS(listener, "", "") }
		} else {
			manager := &autocert.Manager{Cache: autocert.DirCache(tlsOptions.ACMECacheDirectory), Prompt: autocert.AcceptTOS, HostPolicy: autocert.HostWhitelist(tlsOptions.ACMEHosts...)}
			if tlsOptions.ACMEDirectoryURL != "" {
				manager.Client = &acme.Client{DirectoryURL: tlsOptions.ACMEDirectoryURL}
			}
			app.httpServer.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: manager.GetCertificate}
			app.serve = func(listener net.Listener) error { return app.httpServer.ServeTLS(listener, "", "") }
			redirect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				host := r.Host
				if host == "" {
					http.Error(w, "missing host", http.StatusBadRequest)
					return
				}
				http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusTemporaryRedirect)
			})
			challengeServer := &http.Server{Addr: tlsOptions.HTTPChallengeAddress, Handler: manager.HTTPHandler(redirect), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10}
			app.challengeServer = challengeServer
			app.startChallenge = func() error {
				listener, err := net.Listen("tcp", tlsOptions.HTTPChallengeAddress)
				if err != nil {
					return err
				}
				go func() { _ = challengeServer.Serve(listener) }()
				return nil
			}
		}
	}
	keepPool = true
	keepGate = true
	keepJavaScript = true
	keepSQLConsole = true
	return app, nil
}
