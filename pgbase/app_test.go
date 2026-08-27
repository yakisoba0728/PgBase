package pgbase

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
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
