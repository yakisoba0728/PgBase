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

	"github.com/yakisoba0728/PgBase/internal/api"
	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/observability"
	"github.com/yakisoba0728/PgBase/internal/recordops"
	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/sqlconsole"
	"github.com/yakisoba0728/PgBase/internal/usermigrate"
)

type App struct {
	schemaCache            *schema.Cache
	backupManager          *backup.Manager
	requestLog             *observability.RequestLog
	realtimeBroker         *api.ApplicationRealtimeBroker
	runtimeSettings        *runtimesettings.Controller
	options                Options
	pool                   *database.Pool
	records                *recordops.Service
	backupGate             *database.BackupGate
	handler                http.Handler
	httpServer             *http.Server
	challengeServer        *http.Server
	readiness              *readinessState
	cleaner                backgroundRunner
	recordCleaner          backgroundRunner
	realtimeWorker         backgroundRunner
	jobWorker              backgroundRunner
	fileWorker             backgroundRunner
	storageMigrationWorker backgroundRunner
	schemaOperations       backgroundRunner
	schemaListener         backgroundRunner
	membershipWriter       backgroundRunner
	systemStatus           backgroundRunner
	schemaHealth           backgroundRunner
	userMigrationSources   backgroundRunner
	userMigrationRecorder  *usermigrate.AutoRecorder
	sqlConsole             *sqlconsole.Service
	javaScriptRuntime      *javaScriptRuntime
	extensions             *extensionBridge
	listen                 func(string, string) (net.Listener, error)
	serve                  func(net.Listener) error
	startChallenge         func() error
	shutdownMu             sync.Mutex
	shutdownStarted        bool
	shutdownDone           chan struct{}

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

// sampleLoop samples immediately and then on the interval until the context is
// cancelled, which is the app's shutdown signal. Both samplers run this shape.
func sampleLoop(ctx context.Context, interval time.Duration, sample func(context.Context)) error {
	sample(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			sample(ctx)
		}
	}
}

// closeGate closes a first-sample gate exactly once, from the one sampling
// goroutine. Only tests wait on it; a request served earlier sees no sample.
func closeGate(gate chan struct{}) {
	select {
	case <-gate:
	default:
		close(gate)
	}
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

const readinessProbeCacheTTL = 250 * time.Millisecond

type readinessState struct {
	accepting    atomic.Bool
	dependencies []api.Readiness
	recorder     observability.Recorder
	probeMu      sync.Mutex
	probeNow     func() time.Time
	probeValid   bool
	probeErr     error
	probeExpires time.Time
	probeRunning bool
	probeWait    chan struct{}
	probeVersion uint64
}

func (s *readinessState) Ready(ctx context.Context) error {
	for {
		if !s.accepting.Load() {
			return readinessFailure{components: []string{"http"}}
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		s.probeMu.Lock()
		if s.probeValid && s.probeTime().Before(s.probeExpires) {
			err := s.probeErr
			s.probeMu.Unlock()
			if !s.accepting.Load() {
				return readinessFailure{components: []string{"http"}}
			}
			return err
		}
		if !s.probeRunning {
			s.probeRunning = true
			s.probeWait = make(chan struct{})
			version := s.probeVersion
			s.probeMu.Unlock()
			s.runProbe(ctx, version)
			continue
		}
		wait := s.probeWait
		s.probeMu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wait:
		}
	}
}

func (s *readinessState) runProbe(ctx context.Context, version uint64) {
	probeContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			s.abortProbe()
			panic(recovered)
		}
	}()
	s.finishProbe(version, s.probeDependencies(probeContext))
}

func (s *readinessState) probeDependencies(ctx context.Context) error {
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

func (s *readinessState) finishProbe(version uint64, err error) {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	if version == s.probeVersion {
		s.probeValid = true
		s.probeErr = err
		s.probeExpires = s.probeTime().Add(readinessProbeCacheTTL)
	}
	s.probeRunning = false
	close(s.probeWait)
	s.probeWait = nil
}

func (s *readinessState) abortProbe() {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	s.probeValid = false
	s.probeRunning = false
	close(s.probeWait)
	s.probeWait = nil
}

func (s *readinessState) probeTime() time.Time {
	if s.probeNow != nil {
		return s.probeNow()
	}
	return time.Now()
}

func (s *readinessState) invalidateProbe() {
	s.probeMu.Lock()
	s.probeVersion++
	s.probeValid = false
	s.probeMu.Unlock()
}

func (s *readinessState) setAccepting(ctx context.Context, accepting bool) {
	if s == nil {
		return
	}
	if accepting {
		s.invalidateProbe()
		s.accepting.Store(true)
	} else {
		s.accepting.Store(false)
		s.invalidateProbe()
	}
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

// Handler returns the application's HTTP handler without starting a listener.
// Call Start before serving it through a caller-owned HTTP server. Run starts
// the same background lifecycle and uses pgBase's built-in server instead.
func (a *App) Handler() http.Handler { return a.handler }

// Start starts pgBase background components for a caller-owned HTTP server.
// A started application cannot subsequently be passed to Run; call Close when
// the caller-owned server has stopped.
//
// The context cancels background work directly, and pgBase cannot order that
// against a server it does not own. A context tied to SIGTERM therefore stops
// the listener and the membership heartbeat while the caller's requests are
// still draining; Run escapes this only because it owns the server and drains
// first. Pass a context that outlives the server and shut down through Close,
// which drains before it cancels.
func (a *App) Start(ctx context.Context) (err error) {
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
	finish, err := a.beginApplicationStart(ctx, false)
	if err != nil {
		finish(err)
		a.abortStartup(startupDone)
		return err
	}
	backgroundDone, activated := a.activate(ctx, nil, nil, startupDone)
	if !activated {
		finish(errAppClosed)
		return errAppClosed
	}
	finish(nil)
	go func() {
		<-backgroundDone
		a.readiness.setAccepting(context.Background(), false)
	}()
	return nil
}

// Run serves HTTP until the context is cancelled, the server is closed, or it
// encounters an operational error.
func (a *App) Run(ctx context.Context) (err error) {
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
	finish, err := a.beginApplicationStart(ctx, true)
	if err != nil {
		finish(err)
		a.abortStartup(startupDone)
		return err
	}
	listen := a.listen
	if listen == nil {
		listen = net.Listen
	}
	address := a.options.HTTPAddress
	if a.options.TLS != nil {
		address = a.options.TLS.Address
	}
	listener, err := listen("tcp", address)
	if err != nil {
		a.finishStartup()
		closeCtx, cancel := context.WithTimeout(context.Background(), a.options.ShutdownTimeout)
		defer cancel()
		err = errors.Join(fmt.Errorf("listen for HTTP: %w", err), a.Close(closeCtx))
		finish(err)
		return err
	}
	if a.startChallenge != nil {
		if err := a.startChallenge(); err != nil {
			_ = listener.Close()
			a.finishStartup()
			closeCtx, cancel := context.WithTimeout(context.Background(), a.options.ShutdownTimeout)
			defer cancel()
			err = errors.Join(fmt.Errorf("listen for ACME HTTP challenge: %w", err), a.Close(closeCtx))
			finish(err)
			return err
		}
	}
	serverDone := make(chan error, 1)
	// Background tasks are deliberately detached from Run's parent context. A
	// SIGTERM cancels that parent, and cancellation reaches a direct child
	// before Run's own select arm runs, so the listener, the membership
	// heartbeat and the reconciler would stop under requests still in flight.
	// The parent signal only starts shutdown here; shutdown cancels the tasks
	// after the drain. Start keeps its caller-owned contract, where the
	// caller's context does cancel background work directly.
	backgroundDone, activated := a.activate(context.WithoutCancel(ctx), listener, serverDone, startupDone)
	if !activated {
		finish(errAppClosed)
		return errAppClosed
	}
	finish(nil)

	select {
	case serverErr := <-serverDone:
		return a.shutdown(serverErr, nil)
	case <-backgroundDone:
		a.closeMu.Lock()
		backgroundErr := a.backgroundErr
		a.closeMu.Unlock()
		return errors.Join(backgroundErr, a.shutdown(nil, serverDone))
	case <-ctx.Done():
		return a.shutdown(nil, serverDone)
	}
}

// shutdown is the one exit Run takes, whatever ended it: readiness goes false
// so nothing new arrives, Close drains HTTP before it cancels background work,
// and only then is the HTTP server result collected.
func (a *App) shutdown(serverErr error, awaitServer <-chan error) error {
	a.readiness.setAccepting(context.Background(), false)
	closeCtx, cancel := context.WithTimeout(context.Background(), a.options.ShutdownTimeout)
	defer cancel()
	closeErr := a.Close(closeCtx)
	if awaitServer != nil {
		serverErr = waitForServer(closeCtx, awaitServer)
	}
	if errors.Is(serverErr, http.ErrServerClosed) {
		serverErr = nil
	}
	return errors.Join(serverErr, closeErr)
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

// activate publishes the running lifecycle under one lock so a concurrent
// Close either wins outright or waits for a fully started application. A nil
// listener is the caller-owned Start path, which serves no pgBase listener.
func (a *App) activate(ctx context.Context, listener net.Listener, serverDone chan<- error, startupDone chan struct{}) (<-chan struct{}, bool) {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if a.lifecycleState != appLifecycleStarting || a.startupDone != startupDone {
		if listener != nil {
			_ = listener.Close()
		}
		a.completeStartupLocked(startupDone)
		return nil, false
	}
	backgroundDone := a.startBackground(ctx)
	if listener != nil {
		serve := a.serve
		if serve == nil {
			serve = a.httpServer.Serve
		}
		go func() { serverDone <- serve(listener) }()
	}
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

func (a *App) abortStartup(startupDone chan struct{}) {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if a.lifecycleState == appLifecycleStarting && a.startupDone == startupDone {
		a.lifecycleState = appLifecycleInactive
	}
	a.completeStartupLocked(startupDone)
}

func (a *App) completeStartupLocked(startupDone chan struct{}) {
	if startupDone == nil || a.startupDone != startupDone {
		return
	}
	close(startupDone)
	a.startupDone = nil
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
		shutdownTimeout := a.options.ShutdownTimeout
		if shutdownTimeout <= 0 {
			shutdownTimeout = 15 * time.Second
		}
		cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		go func() {
			defer cancelCleanup()
			a.closeResources(cleanupCtx, startupDone, a.shutdownDone)
		}()
	}
	done := a.shutdownDone
	a.shutdownMu.Unlock()

	// A shutdown that finished as the deadline passed reports its own result,
	// not the deadline.
	select {
	case <-done:
	case <-ctx.Done():
		select {
		case <-done:
		default:
			return ctx.Err()
		}
	}
	a.closeMu.Lock()
	defer a.closeMu.Unlock()
	return a.closeErr
}

func (a *App) closeResources(ctx context.Context, startupDone <-chan struct{}, done chan struct{}) {
	terminateFinish, terminateErr := a.beginApplicationTerminate(ctx)
	var closeErr error
	closeErr = errors.Join(closeErr, terminateErr)
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
	if a.challengeServer != nil {
		shutdownErr := a.challengeServer.Shutdown(ctx)
		if !errors.Is(shutdownErr, http.ErrServerClosed) {
			closeErr = errors.Join(closeErr, shutdownErr)
		}
	}
	if backgroundCancel != nil {
		backgroundCancel()
	}
	if err := waitForDone(ctx, backgroundDone, "background components"); err != nil {
		closeErr = errors.Join(closeErr, err)
	}
	if a.backupManager != nil {
		closeErr = errors.Join(closeErr, a.backupManager.Shutdown(ctx))
	}
	if a.requestLog != nil {
		closeErr = errors.Join(closeErr, a.requestLog.Shutdown(ctx))
	}
	if a.sqlConsole != nil {
		closeErr = errors.Join(closeErr, a.sqlConsole.Close(ctx))
	}
	if a.javaScriptRuntime != nil {
		closeErr = errors.Join(closeErr, a.javaScriptRuntime.Close(ctx))
	}
	if a.pool != nil {
		if err := a.pool.CloseContext(ctx); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close PostgreSQL pool: %w", err))
		}
	}
	if a.backupGate != nil {
		if err := a.backupGate.Close(ctx); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("release PostgreSQL writer gate: %w", err))
		}
	}

	a.closeMu.Lock()
	a.closeErr = errors.Join(closeErr, a.backgroundErr)
	finalErr := a.closeErr
	a.closeMu.Unlock()
	terminateFinish(finalErr)
	a.lifecycleMu.Lock()
	a.lifecycleState = appLifecycleClosed
	a.lifecycleMu.Unlock()
	close(done)
}

func forceCloseHTTP(ctx context.Context, server *http.Server) error {
	result := make(chan error, 1)
	go func() { result <- server.Close() }()
	var err error
	select {
	case err = <-result:
	default:
		select {
		case err = <-result:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	if err == nil {
		return nil
	}
	return fmt.Errorf("force close HTTP server: %w", err)
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
