package pgbase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/testutil"
)

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

func TestConcurrentClosePreventsRunFromStartingBackgroundWork(t *testing.T) {
	listenEntered := make(chan struct{})
	releaseListen := make(chan struct{})
	listener := &barrierListener{accepted: make(chan struct{}), closed: make(chan struct{})}
	cleaner := &barrierCleaner{started: make(chan struct{}), stopped: make(chan struct{})}
	app := &App{
		options:          Options{HTTPAddress: "127.0.0.1:0", ShutdownTimeout: time.Second},
		httpServer:       &http.Server{},
		readiness:        &readinessState{},
		cleaner:          cleaner,
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
	if err := app.Run(context.Background()); !errors.Is(err, errAppClosed) {
		t.Fatalf("Run after Close error = %v, want %v", err, errAppClosed)
	}
	if err := app.Close(context.Background()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

type barrierCleaner struct {
	started chan struct{}
	stopped chan struct{}
}

func (c *barrierCleaner) Run(ctx context.Context) error {
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
