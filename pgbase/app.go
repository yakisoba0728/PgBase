package pgbase

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/webisnoteasy/PgBase/internal/adminauth"
	"github.com/webisnoteasy/PgBase/internal/api"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/membership"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/internal/recordops"
	"github.com/webisnoteasy/PgBase/internal/schema"
	adminui "github.com/webisnoteasy/PgBase/ui/admin"
)

type App struct {
	options          Options
	pool             *database.Pool
	handler          http.Handler
	httpServer       *http.Server
	readiness        *readinessState
	cleaner          backgroundRunner
	recordCleaner    backgroundRunner
	schemaOperations backgroundRunner
	schemaListener   backgroundRunner
	membershipWriter backgroundRunner
	systemStatus     *systemStatusSampler
	schemaHealth     *schemaHealthSampler
	listen           func(string, string) (net.Listener, error)
	shutdownMu       sync.Mutex
	shutdownStarted  bool
	shutdownDone     chan struct{}

	lifecycleMu      sync.Mutex
	lifecycleState   appLifecycleState
	lifecycleClosing chan struct{}
	startupDone      chan struct{}

	closeMu          sync.Mutex
	closeErr         error
	backgroundCancel context.CancelFunc
	backgroundDone   chan struct{}
	backgroundErr    error
}

type backgroundRunner interface {
	Run(context.Context) error
}

type namedBackgroundRunner struct {
	name   string
	runner backgroundRunner
}

type backgroundResult struct {
	name            string
	err             error
	cancellationErr error
}

type appLifecycleState uint8

const (
	appLifecycleInactive appLifecycleState = iota
	appLifecycleStarting
	appLifecycleRunning
	appLifecycleClosing
	appLifecycleClosed
)

var (
	errAppAlreadyStarted = errors.New("pgbase app has already been started")
	errAppClosed         = errors.New("pgbase app is closing or closed")
)

type readinessState struct {
	accepting    atomic.Bool
	dependencies []api.Readiness
	recorder     observability.Recorder
}

func (s *readinessState) Ready(ctx context.Context) error {
	if !s.accepting.Load() {
		return readinessFailure{components: []string{"http"}}
	}
	for _, dependency := range s.dependencies {
		name := "dependency"
		if named, ok := dependency.(interface{ Component() string }); ok {
			name = named.Component()
		}
		err := dependency.Ready(ctx)
		if component, ok := readinessTelemetryComponent(name); ok {
			s.recordReadiness(ctx, component, err == nil)
		}
		if err != nil {
			if name != "dependency" {
				return readinessFailure{components: []string{name}}
			}
			return readinessFailure{components: []string{"dependency"}}
		}
	}
	return nil
}

func (s *readinessState) setAccepting(ctx context.Context, accepting bool) {
	if s == nil {
		return
	}
	s.accepting.Store(accepting)
	s.recordReadiness(ctx, observability.ReadinessHTTP, accepting)
}

func (s *readinessState) recordReadiness(ctx context.Context, component observability.ReadinessComponent, ready bool) {
	if s != nil && s.recorder != nil {
		s.recorder.Record(ctx, observability.ReadinessEvent(component, ready))
	}
}

func readinessTelemetryComponent(name string) (observability.ReadinessComponent, bool) {
	switch name {
	case "database":
		return observability.ReadinessDatabase, true
	case "migrations":
		return observability.ReadinessMigrations, true
	default:
		return "", false
	}
}

type readinessFunc func(context.Context) error

func (f readinessFunc) Ready(ctx context.Context) error { return f(ctx) }

type namedReadiness struct {
	name  string
	check api.Readiness
}

func (r namedReadiness) Component() string               { return r.name }
func (r namedReadiness) Ready(ctx context.Context) error { return r.check.Ready(ctx) }

type readinessFailure struct{ components []string }

func (e readinessFailure) Error() string        { return "required component is not ready" }
func (e readinessFailure) Components() []string { return append([]string(nil), e.components...) }

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
	pool, err := database.Open(ctx, normalized.DatabaseURL, normalized.StatementTimeout)
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
	schemaListener := schema.NewListener(pool, schemaCache, metrics)
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
	systemStatus := newSystemStatusSampler(pool, runner, schemaCache, schemaListener)
	schemaHealth := newSchemaHealthSampler(pool, runner)
	handler, err := api.NewRouter(api.Dependencies{
		Readiness:          readiness,
		Logger:             normalized.Logger,
		CORSAllowedOrigins: normalized.CORSAllowedOrigins,
		AdminAuth:          authService,
		Schema:             schemaService,
		Records:            recordService,
		PublicURL:          normalized.PublicURL,
		TrustedProxies:     normalized.TrustedProxies,
		System:             systemStatus,
		SchemaHealth:       schemaHealth,
		AdminUI:            adminHandler,
		Metrics:            metrics,
		MetricsExposition:  metricsExposition,
		SchemaApplyTimeout: normalized.SchemaApplyTimeout,
	})
	if err != nil {
		return nil, err
	}
	app := &App{
		options:          normalized,
		pool:             pool,
		handler:          handler,
		readiness:        readiness,
		cleaner:          adminauth.NewCleaner(pool, nil, normalized.Logger),
		recordCleaner:    recordops.NewIdempotencyCleaner(pool, normalized.Logger),
		schemaOperations: schemaService,
		schemaListener:   schemaListener,
		membershipWriter: membershipRuntime,
		systemStatus:     systemStatus,
		schemaHealth:     schemaHealth,
		listen:           net.Listen,
		lifecycleClosing: make(chan struct{}),
	}
	app.httpServer = &http.Server{
		Addr:              normalized.HTTPAddress,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	keepPool = true
	return app, nil
}

// Handler returns the application's HTTP handler without starting a listener.
// Call Start before serving it through a caller-owned HTTP server. Run starts
// the same background lifecycle and uses pgBase's built-in server instead.
func (a *App) Handler() http.Handler { return a.handler }

// Start starts pgBase background components for a caller-owned HTTP server.
// A started application cannot subsequently be passed to Run; call Close when
// the caller-owned server has stopped.
func (a *App) Start(ctx context.Context) error {
	if a == nil || a.httpServer == nil {
		return errors.New("pgbase app is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	startupDone, err := a.beginStartup()
	if err != nil {
		return err
	}
	backgroundDone, activated := a.activateBackground(ctx, startupDone)
	if !activated {
		return errAppClosed
	}
	go func() {
		<-backgroundDone
		a.readiness.setAccepting(context.Background(), false)
	}()
	return nil
}

// Run serves HTTP until the context is cancelled, the server is closed, or it
// encounters an operational error.
func (a *App) Run(ctx context.Context) error {
	if a == nil || a.httpServer == nil {
		return errors.New("pgbase app is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	startupDone, err := a.beginStartup()
	if err != nil {
		return err
	}
	listen := a.listen
	if listen == nil {
		listen = net.Listen
	}
	listener, err := listen("tcp", a.options.HTTPAddress)
	if err != nil {
		a.finishStartup()
		closeCtx, cancel := context.WithTimeout(context.Background(), a.options.ShutdownTimeout)
		defer cancel()
		return errors.Join(fmt.Errorf("listen for HTTP: %w", err), a.Close(closeCtx))
	}
	serverDone := make(chan error, 1)
	backgroundDone, activated := a.activateStartup(ctx, listener, serverDone, startupDone)
	if !activated {
		return errAppClosed
	}

	select {
	case serverErr := <-serverDone:
		a.readiness.setAccepting(context.Background(), false)
		closeCtx, cancel := context.WithTimeout(context.Background(), a.options.ShutdownTimeout)
		closeErr := a.Close(closeCtx)
		cancel()
		if errors.Is(serverErr, http.ErrServerClosed) {
			serverErr = nil
		}
		return errors.Join(serverErr, closeErr)
	case <-backgroundDone:
		a.readiness.setAccepting(context.Background(), false)
		a.closeMu.Lock()
		backgroundErr := a.backgroundErr
		a.closeMu.Unlock()
		closeCtx, cancel := context.WithTimeout(context.Background(), a.options.ShutdownTimeout)
		closeErr := a.Close(closeCtx)
		serverErr := waitForServer(closeCtx, serverDone)
		cancel()
		if errors.Is(serverErr, http.ErrServerClosed) {
			serverErr = nil
		}
		return errors.Join(backgroundErr, closeErr, serverErr)
	case <-ctx.Done():
		a.readiness.setAccepting(context.Background(), false)
		closeCtx, cancel := context.WithTimeout(context.Background(), a.options.ShutdownTimeout)
		closeErr := a.Close(closeCtx)
		serverErr := waitForServer(closeCtx, serverDone)
		cancel()
		if errors.Is(serverErr, http.ErrServerClosed) {
			serverErr = nil
		}
		return errors.Join(closeErr, serverErr)
	}
}

func (a *App) beginStartup() (chan struct{}, error) {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if a.lifecycleClosing == nil {
		a.lifecycleClosing = make(chan struct{})
	}
	switch a.lifecycleState {
	case appLifecycleInactive:
		a.lifecycleState = appLifecycleStarting
		a.startupDone = make(chan struct{})
		return a.startupDone, nil
	case appLifecycleStarting, appLifecycleRunning:
		return nil, errAppAlreadyStarted
	default:
		return nil, errAppClosed
	}
}

func (a *App) activateStartup(ctx context.Context, listener net.Listener, serverDone chan<- error, startupDone chan struct{}) (<-chan struct{}, bool) {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if a.lifecycleState != appLifecycleStarting || a.startupDone != startupDone {
		_ = listener.Close()
		a.completeStartupLocked(startupDone)
		return nil, false
	}
	backgroundDone := a.startBackground(ctx)
	go func() {
		serverDone <- a.httpServer.Serve(listener)
	}()
	a.readiness.setAccepting(ctx, true)
	a.lifecycleState = appLifecycleRunning
	a.completeStartupLocked(startupDone)
	return backgroundDone, true
}

func (a *App) activateBackground(ctx context.Context, startupDone chan struct{}) (<-chan struct{}, bool) {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if a.lifecycleState != appLifecycleStarting || a.startupDone != startupDone {
		a.completeStartupLocked(startupDone)
		return nil, false
	}
	backgroundDone := a.startBackground(ctx)
	a.readiness.setAccepting(ctx, true)
	a.lifecycleState = appLifecycleRunning
	a.completeStartupLocked(startupDone)
	return backgroundDone, true
}

func (a *App) finishStartup() {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	a.completeStartupLocked(a.startupDone)
}

func (a *App) completeStartupLocked(startupDone chan struct{}) {
	if startupDone == nil || a.startupDone != startupDone {
		return
	}
	close(startupDone)
	a.startupDone = nil
}

func (a *App) startBackground(ctx context.Context) <-chan struct{} {
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	a.closeMu.Lock()
	a.backgroundCancel = cancel
	a.backgroundDone = done
	a.closeMu.Unlock()
	go func() {
		runners := make([]namedBackgroundRunner, 0, 7)
		if a.cleaner != nil {
			runners = append(runners, namedBackgroundRunner{name: "administrator authentication cleaner", runner: a.cleaner})
		}
		if a.recordCleaner != nil {
			runners = append(runners, namedBackgroundRunner{name: "record idempotency cleaner", runner: a.recordCleaner})
		}
		if a.schemaOperations != nil {
			runners = append(runners, namedBackgroundRunner{name: "schema operation maintenance", runner: a.schemaOperations})
		}
		if a.schemaListener != nil {
			runners = append(runners, namedBackgroundRunner{name: "schema listener", runner: a.schemaListener})
		}
		if a.membershipWriter != nil {
			runners = append(runners, namedBackgroundRunner{name: "record membership writer", runner: a.membershipWriter})
		}
		if a.systemStatus != nil {
			runners = append(runners, namedBackgroundRunner{name: "system status sampler", runner: a.systemStatus})
		}
		if a.schemaHealth != nil {
			runners = append(runners, namedBackgroundRunner{name: "schema health sampler", runner: a.schemaHealth})
		}
		results := make(chan backgroundResult, len(runners))
		for _, runner := range runners {
			go func(runner namedBackgroundRunner) {
				err := runner.runner.Run(runCtx)
				results <- backgroundResult{name: runner.name, err: err, cancellationErr: runCtx.Err()}
			}(runner)
		}
		var backgroundErr error
		for index := range runners {
			result := <-results
			switch {
			case result.err == nil && result.cancellationErr == nil:
				backgroundErr = errors.Join(backgroundErr, fmt.Errorf("%s stopped unexpectedly", result.name))
			case result.err != nil && !(result.cancellationErr != nil && errors.Is(result.err, result.cancellationErr)):
				backgroundErr = errors.Join(backgroundErr, fmt.Errorf("%s: %w", result.name, result.err))
			}
			if index == 0 {
				cancel()
			}
		}
		a.closeMu.Lock()
		a.backgroundErr = backgroundErr
		close(done)
		a.closeMu.Unlock()
	}()
	return done
}

// Close stops accepting HTTP requests, drains active requests, and closes the
// PostgreSQL pool. It is safe to call more than once.
func (a *App) Close(ctx context.Context) error {
	if a == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.shutdownMu.Lock()
	if !a.shutdownStarted {
		a.shutdownStarted = true
		a.shutdownDone = make(chan struct{})
		startupDone := a.beginClose()
		if a.readiness != nil {
			a.readiness.setAccepting(context.Background(), false)
		}
		go a.closeResources(ctx, startupDone, a.shutdownDone)
	}
	done := a.shutdownDone
	a.shutdownMu.Unlock()

	select {
	case <-done:
		a.closeMu.Lock()
		defer a.closeMu.Unlock()
		return a.closeErr
	case <-ctx.Done():
		select {
		case <-done:
			a.closeMu.Lock()
			defer a.closeMu.Unlock()
			return a.closeErr
		default:
			return ctx.Err()
		}
	}
}

func (a *App) closeResources(ctx context.Context, startupDone <-chan struct{}, done chan struct{}) {
	var closeErr error
	if err := waitForDone(ctx, startupDone, "application startup"); err != nil {
		closeErr = errors.Join(closeErr, err)
	}

	a.closeMu.Lock()
	backgroundCancel := a.backgroundCancel
	backgroundDone := a.backgroundDone
	a.closeMu.Unlock()

	// Drain HTTP before cancelling background work. Readiness is already
	// false, so no new request arrives, but the requests still in flight may
	// depend on a cleaner, the schema listener or the membership writer lease.
	// Cancelling those first would fail requests that were about to succeed.
	if a.httpServer != nil {
		shutdownErr := a.httpServer.Shutdown(ctx)
		if errors.Is(shutdownErr, http.ErrServerClosed) {
			shutdownErr = nil
		}
		if shutdownErr != nil {
			closeErr = errors.Join(closeErr, shutdownErr)
			if forceErr := forceCloseHTTP(ctx, a.httpServer); forceErr != nil && !errors.Is(forceErr, http.ErrServerClosed) {
				closeErr = errors.Join(closeErr, forceErr)
			}
		}
	}
	if backgroundCancel != nil {
		backgroundCancel()
	}
	if err := waitForDone(ctx, backgroundDone, "background components"); err != nil {
		closeErr = errors.Join(closeErr, err)
	}
	if a.pool != nil {
		if err := a.pool.CloseContext(ctx); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close PostgreSQL pool: %w", err))
		}
	}

	a.closeMu.Lock()
	a.closeErr = errors.Join(closeErr, a.backgroundErr)
	a.closeMu.Unlock()
	a.lifecycleMu.Lock()
	a.lifecycleState = appLifecycleClosed
	a.lifecycleMu.Unlock()
	close(done)
}

func forceCloseHTTP(ctx context.Context, server *http.Server) error {
	result := make(chan error, 1)
	go func() { result <- server.Close() }()
	select {
	case err := <-result:
		if err == nil {
			return nil
		}
		return fmt.Errorf("force close HTTP server: %w", err)
	default:
	}
	select {
	case err := <-result:
		if err == nil {
			return nil
		}
		return fmt.Errorf("force close HTTP server: %w", err)
	case <-ctx.Done():
		return fmt.Errorf("force close HTTP server: %w", ctx.Err())
	}
}

func waitForDone(ctx context.Context, done <-chan struct{}, component string) error {
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for %s: %w", component, ctx.Err())
	}
}

func waitForServer(ctx context.Context, serverDone <-chan error) error {
	select {
	case err := <-serverDone:
		return err
	default:
	}
	select {
	case err := <-serverDone:
		return err
	case <-ctx.Done():
		return fmt.Errorf("wait for HTTP server: %w", ctx.Err())
	}
}

func (a *App) beginClose() <-chan struct{} {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if a.lifecycleClosing == nil {
		a.lifecycleClosing = make(chan struct{})
	}
	if a.lifecycleState < appLifecycleClosing {
		a.lifecycleState = appLifecycleClosing
		close(a.lifecycleClosing)
	}
	return a.startupDone
}
