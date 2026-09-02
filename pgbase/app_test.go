package pgbase

import (
	"context"
	"errors"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/api"
	"github.com/webisnoteasy/PgBase/internal/observability"
)

func TestReadinessProbeOnlyPublishesProbeOwnedComponents(t *testing.T) {
	recorder := &readinessTelemetryRecorder{}
	state := &readinessState{
		recorder: recorder,
		dependencies: []api.Readiness{
			namedReadiness{name: "database", check: readinessFunc(func(context.Context) error { return nil })},
			namedReadiness{name: "migrations", check: readinessFunc(func(context.Context) error { return nil })},
			namedReadiness{name: "schema", check: readinessFunc(func(context.Context) error { return nil })},
		},
	}
	state.accepting.Store(true)
	if err := state.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}

	events := recorder.snapshot()
	if len(events) != 2 || events[0].ReadinessComponent() != observability.ReadinessDatabase || events[1].ReadinessComponent() != observability.ReadinessMigrations {
		t.Fatalf("readiness probe events = %#v, want only database and migrations", events)
	}
}

type readinessTelemetryRecorder struct {
	mu     sync.Mutex
	events []observability.Event
}

func (r *readinessTelemetryRecorder) Record(_ context.Context, event observability.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *readinessTelemetryRecorder) snapshot() []observability.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]observability.Event(nil), r.events...)
}

func TestStartRunsAndCloseStopsEveryBackgroundComponent(t *testing.T) {
	adminCleaner := &barrierCleaner{started: make(chan struct{}), stopped: make(chan struct{})}
	recordCleaner := &barrierCleaner{started: make(chan struct{}), stopped: make(chan struct{})}
	schemaOperations := &barrierCleaner{started: make(chan struct{}), stopped: make(chan struct{})}
	schemaListener := &barrierCleaner{started: make(chan struct{}), stopped: make(chan struct{})}
	app := &App{
		httpServer:       &http.Server{},
		readiness:        &readinessState{},
		cleaner:          adminCleaner,
		recordCleaner:    recordCleaner,
		schemaOperations: schemaOperations,
		schemaListener:   schemaListener,
		lifecycleClosing: make(chan struct{}),
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for name, runner := range map[string]*barrierCleaner{
		"administrator cleaner": adminCleaner,
		"record cleaner":        recordCleaner,
		"schema operations":     schemaOperations,
		"schema listener":       schemaListener,
	} {
		waitForSignal(t, runner.started, name+" did not start")
		if got := runner.runs.Load(); got != 1 {
			t.Fatalf("%s run count = %d, want one", name, got)
		}
	}
	if err := app.Start(context.Background()); !errors.Is(err, errAppAlreadyStarted) {
		t.Fatalf("second Start error = %v, want %v", err, errAppAlreadyStarted)
	}
	for name, runner := range map[string]*barrierCleaner{
		"administrator cleaner": adminCleaner,
		"record cleaner":        recordCleaner,
		"schema operations":     schemaOperations,
		"schema listener":       schemaListener,
	} {
		if got := runner.runs.Load(); got != 1 {
			t.Fatalf("%s run count after second Start = %d, want one", name, got)
		}
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	for name, runner := range map[string]*barrierCleaner{
		"administrator cleaner": adminCleaner,
		"record cleaner":        recordCleaner,
		"schema operations":     schemaOperations,
		"schema listener":       schemaListener,
	} {
		waitForSignal(t, runner.stopped, name+" did not stop")
	}
}

func TestRunReportsUnexpectedBackgroundRunnerExit(t *testing.T) {
	listener := &barrierListener{accepted: make(chan struct{}), closed: make(chan struct{})}
	earlyExit := &releasedRunner{release: make(chan struct{})}
	sibling := &barrierCleaner{started: make(chan struct{}), stopped: make(chan struct{})}
	app := &App{
		options:          Options{HTTPAddress: "127.0.0.1:0", ShutdownTimeout: time.Second},
		httpServer:       &http.Server{},
		readiness:        &readinessState{},
		cleaner:          earlyExit,
		recordCleaner:    sibling,
		lifecycleClosing: make(chan struct{}),
		listen: func(_, _ string) (net.Listener, error) {
			return listener, nil
		},
	}

	runDone := make(chan error, 1)
	go func() { runDone <- app.Run(context.Background()) }()
	waitForSignal(t, listener.accepted, "HTTP server did not start")
	waitForSignal(t, sibling.started, "sibling background runner did not start")
	close(earlyExit.release)

	err := waitForResult(t, runDone, "Run did not return after a background runner exited")
	if err == nil {
		t.Fatal("Run reported a clean shutdown after a background runner returned nil unexpectedly")
	}
	if !strings.Contains(err.Error(), "administrator authentication cleaner") {
		t.Fatalf("Run error = %q, want the failed background runner name", err)
	}
	waitForSignal(t, sibling.stopped, "unexpected runner exit did not cancel its sibling")
}

func TestRunReportsSchemaOperationMaintenanceExitByName(t *testing.T) {
	listener := &barrierListener{accepted: make(chan struct{}), closed: make(chan struct{})}
	earlyExit := &releasedRunner{release: make(chan struct{})}
	sibling := &barrierCleaner{started: make(chan struct{}), stopped: make(chan struct{})}
	app := &App{
		options:          Options{HTTPAddress: "127.0.0.1:0", ShutdownTimeout: time.Second},
		httpServer:       &http.Server{},
		readiness:        &readinessState{},
		schemaOperations: earlyExit,
		schemaListener:   sibling,
		lifecycleClosing: make(chan struct{}),
		listen: func(_, _ string) (net.Listener, error) {
			return listener, nil
		},
	}

	runDone := make(chan error, 1)
	go func() { runDone <- app.Run(context.Background()) }()
	waitForSignal(t, listener.accepted, "HTTP server did not start")
	waitForSignal(t, sibling.started, "sibling background runner did not start")
	close(earlyExit.release)

	err := waitForResult(t, runDone, "Run did not return after schema operation maintenance exited")
	if err == nil || !strings.Contains(err.Error(), "schema operation maintenance") {
		t.Fatalf("Run error = %v, want schema operation maintenance runner name", err)
	}
	waitForSignal(t, sibling.stopped, "schema operation maintenance exit did not cancel its sibling")
}

func TestConcurrentClosePreventsRunFromStartingBackgroundWork(t *testing.T) {
	listenEntered := make(chan struct{})
	releaseListen := make(chan struct{})
	listener := &barrierListener{accepted: make(chan struct{}), closed: make(chan struct{})}
	cleaner := &barrierCleaner{started: make(chan struct{}), stopped: make(chan struct{})}
	schemaListener := &barrierCleaner{started: make(chan struct{}), stopped: make(chan struct{})}
	app := &App{
		options:          Options{HTTPAddress: "127.0.0.1:0", ShutdownTimeout: time.Second},
		httpServer:       &http.Server{},
		readiness:        &readinessState{},
		cleaner:          cleaner,
		schemaListener:   schemaListener,
		lifecycleClosing: make(chan struct{}),
		listen: func(_, _ string) (net.Listener, error) {
			close(listenEntered)
			<-releaseListen
			return listener, nil
		},
	}

	runDone := make(chan error, 1)
	go func() { runDone <- app.Run(context.Background()) }()
	waitForSignal(t, listenEntered, "Run did not reach listener barrier")

	closeDone := make(chan error, 1)
	go func() { closeDone <- app.Close(context.Background()) }()
	waitForSignal(t, app.lifecycleClosing, "Close did not enter closing state")
	close(releaseListen)

	if err := waitForResult(t, closeDone, "Close did not return"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := waitForResult(t, runDone, "Run did not return"); err == nil {
		t.Fatal("Run succeeded after Close began")
	}
	waitForSignal(t, listener.closed, "listener returned after Close began was not closed")
	select {
	case <-listener.accepted:
		t.Fatal("HTTP server started after Close began")
	default:
	}
	select {
	case <-cleaner.started:
		t.Fatal("cleaner started after Close began")
	default:
	}
	select {
	case <-schemaListener.started:
		t.Fatal("schema listener started after Close began")
	default:
	}
	if err := app.Run(context.Background()); !errors.Is(err, errAppClosed) {
		t.Fatalf("Run after Close error = %v, want %v", err, errAppClosed)
	}
	if err := app.Close(context.Background()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestCloseHonorsDeadlineWhileBackgroundRunnerIsStalled(t *testing.T) {
	backgroundDone := make(chan struct{})
	defer close(backgroundDone)
	app := &App{
		httpServer:       &http.Server{},
		readiness:        &readinessState{},
		lifecycleState:   appLifecycleRunning,
		lifecycleClosing: make(chan struct{}),
		backgroundCancel: func() {},
		backgroundDone:   backgroundDone,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	result := make(chan error, 1)
	go func() { result <- app.Close(ctx) }()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close error = %v, want context deadline exceeded", err)
		}
		if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
			t.Fatalf("Close exceeded bounded shutdown window: %s", elapsed)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Close waited indefinitely for a stalled background runner")
	}
}

func TestConcurrentCloseCallerHonorsItsOwnDeadline(t *testing.T) {
	backgroundDone := make(chan struct{})
	app := &App{
		httpServer:       &http.Server{},
		readiness:        &readinessState{},
		lifecycleState:   appLifecycleRunning,
		lifecycleClosing: make(chan struct{}),
		backgroundCancel: func() {},
		backgroundDone:   backgroundDone,
	}
	firstResult := make(chan error, 1)
	go func() { firstResult <- app.Close(context.Background()) }()
	waitForSignal(t, app.lifecycleClosing, "first Close did not begin shutdown")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := app.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("concurrent Close error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("concurrent Close ignored its own deadline: %s", elapsed)
	}

	close(backgroundDone)
	if err := waitForResult(t, firstResult, "first Close did not finish after background release"); err != nil {
		t.Fatalf("first Close: %v", err)
	}
}

func TestCloseForceClosesActiveHTTPConnectionAfterGraceDeadline(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCanceled := make(chan struct{})
	handler := http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(requestStarted)
		<-request.Context().Done()
		close(requestCanceled)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &http.Server{Handler: handler}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(listener) }()
	defer func() {
		_ = server.Close()
		<-serverDone
	}()

	requestDone := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String()) // #nosec G107 -- test-local listener.
		if response != nil {
			response.Body.Close()
		}
		requestDone <- requestErr
	}()
	waitForSignal(t, requestStarted, "request did not reach blocking handler")

	app := &App{
		httpServer:       server,
		readiness:        &readinessState{},
		lifecycleState:   appLifecycleRunning,
		lifecycleClosing: make(chan struct{}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := app.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v, want context deadline exceeded", err)
	}
	select {
	case <-requestCanceled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("active request context remained open after graceful shutdown expired")
	}
	select {
	case <-requestDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("HTTP client connection remained open after forced shutdown")
	}
}

type barrierCleaner struct {
	started chan struct{}
	stopped chan struct{}
	runs    atomic.Int32
}

type releasedRunner struct {
	release chan struct{}
}

func (r *releasedRunner) Run(context.Context) error {
	<-r.release
	return nil
}

func (c *barrierCleaner) Run(ctx context.Context) error {
	c.runs.Add(1)
	close(c.started)
	<-ctx.Done()
	close(c.stopped)
	return ctx.Err()
}

type barrierListener struct {
	accepted   chan struct{}
	closed     chan struct{}
	acceptOnce sync.Once
	closeOnce  sync.Once
}

func (l *barrierListener) Accept() (net.Conn, error) {
	l.acceptOnce.Do(func() { close(l.accepted) })
	<-l.closed
	return nil, net.ErrClosed
}

func (l *barrierListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (*barrierListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
}

func waitForSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal(failure)
	}
}

func waitForResult(t *testing.T, result <-chan error, failure string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal(failure)
		return nil
	}
}

func unusedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close reservation: %v", err)
	}
	return address
}

func waitForLive(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(url) // #nosec G107 -- URL is a test-local listener.
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("live endpoint did not become available")
}

// orderRecorder captures the sequence of shutdown milestones so the ordering
// contract can be asserted rather than inferred from timing.
type orderRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *orderRecorder) record(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *orderRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

// orderedRunner reports when its context is cancelled, which is the moment the
// app stops background work.
type orderedRunner struct {
	recorder *orderRecorder
	started  chan struct{}
}

func (r *orderedRunner) Run(ctx context.Context) error {
	close(r.started)
	<-ctx.Done()
	r.recorder.record("background cancelled")
	return nil
}

// An in-flight request may still depend on a cleaner, the schema listener or
// the membership writer lease. Readiness goes false first so nothing new
// arrives, then HTTP drains, and only then is background work cancelled;
// cancelling first would fail requests that were about to succeed.
func TestCloseDrainsHTTPBeforeCancellingBackgroundWork(t *testing.T) {
	recorder := &orderRecorder{}
	runner := &orderedRunner{recorder: recorder, started: make(chan struct{})}
	inflight := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-inflight
		recorder.record("request served")
		w.WriteHeader(http.StatusNoContent)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()

	readiness := &readinessState{}
	app := &App{
		httpServer:       server,
		readiness:        readiness,
		cleaner:          runner,
		lifecycleClosing: make(chan struct{}),
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForSignal(t, runner.started, "background runner did not start")

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, err := http.Get("http://" + listener.Addr().String() + "/")
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	// Give the request time to reach the handler and block there.
	time.Sleep(50 * time.Millisecond)

	closed := make(chan error, 1)
	go func() { closed <- app.Close(context.Background()) }()
	// Close must now be waiting on the drain, not cancelling background work.
	time.Sleep(50 * time.Millisecond)
	if got := recorder.snapshot(); len(got) != 0 {
		t.Fatalf("shutdown progressed while a request was in flight: %v", got)
	}
	if readiness.Ready(context.Background()) == nil {
		t.Fatal("readiness stayed true after Close began")
	}

	close(inflight)
	<-requestDone
	if err := <-closed; err != nil {
		t.Fatalf("Close() = %v", err)
	}
	want := []string{"request served", "background cancelled"}
	if got := recorder.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("shutdown order = %v, want %v", got, want)
	}
}

// A Run cancellation is a shutdown request, not a kill signal. SIGTERM reaches
// Run's parent context, and the requests already in flight may still depend on
// a cleaner, the schema listener or the membership writer lease. Background
// work must therefore outlive the parent signal and stop only after the drain,
// exactly as it does when Close is called directly.
func TestRunCancellationDrainsHTTPBeforeCancellingBackgroundWork(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	requestEntered := make(chan struct{})
	releaseRequest := make(chan struct{})
	runner := &barrierCleaner{started: make(chan struct{}), stopped: make(chan struct{})}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(requestEntered)
		<-releaseRequest
		w.WriteHeader(http.StatusNoContent)
	})}
	app := &App{
		options:          Options{HTTPAddress: "127.0.0.1:0", ShutdownTimeout: 5 * time.Second},
		httpServer:       server,
		readiness:        &readinessState{},
		cleaner:          runner,
		lifecycleClosing: make(chan struct{}),
		listen:           func(_, _ string) (net.Listener, error) { return listener, nil },
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	runDone := make(chan error, 1)
	go func() { runDone <- app.Run(runCtx) }()
	waitForSignal(t, runner.started, "background runner never started")

	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		response, requestErr := http.Get("http://" + listener.Addr().String() + "/") // #nosec G107 -- test-local listener.
		if requestErr == nil {
			_ = response.Body.Close()
		}
	}()
	waitForSignal(t, requestEntered, "request never reached the handler")

	cancelRun()
	waitForSignal(t, app.lifecycleClosing, "Run cancellation never began shutdown")
	stoppedBeforeDrain := false
	select {
	case <-runner.stopped:
		stoppedBeforeDrain = true
	case <-time.After(200 * time.Millisecond):
	}

	close(releaseRequest)
	waitForSignal(t, clientDone, "request never completed")
	if err := waitForResult(t, runDone, "Run never returned"); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if stoppedBeforeDrain {
		t.Fatal("background runner was cancelled while HTTP request was still in flight")
	}
}
