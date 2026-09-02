//go:build integration

package pgbase

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

// A schema apply holds pgbase_meta.schema_state FOR UPDATE for the whole of its
// DDL, with the request statement timeout disabled and a budget of its own. The
// membership lease heartbeat pinned the same row under the pool's ordinary five
// second timeout, so an ordinary large ALTER killed the heartbeat worker, and
// App treats a background runner that returns early as fatal: it cancels the
// others and propagates a run error. A legitimate DDL took the process down.
func TestLongSchemaDDLDoesNotStopTheRunningApp(t *testing.T) {
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
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- app.Run(ctx) }()
	readyURL := "http://" + address + "/health/ready"
	waitForLive(t, readyURL)

	release := holdLongSchemaDDL(t, databaseURL)

	// Longer than one 20 second membership heartbeat period plus the five
	// second request statement timeout the heartbeat used to die on.
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		select {
		case runErr := <-runDone:
			t.Fatalf("app stopped while a legitimate schema DDL was running: %v", runErr)
		case <-time.After(time.Second):
		}
		requireReady(t, readyURL, "during the schema DDL")
	}

	release()
	requireReady(t, readyURL, "after the schema DDL committed")
	select {
	case runErr := <-runDone:
		t.Fatalf("app stopped after the schema DDL committed: %v", runErr)
	default:
	}

	cancel()
	if runErr := waitForResult(t, runDone, "app never shut down"); runErr != nil {
		t.Fatalf("Run() = %v", runErr)
	}
}

// holdLongSchemaDDL takes the lock profile of a long schema apply on a
// connection of its own: real DDL inside a transaction that holds the exclusive
// schema pin with the request statement timeout disabled. The returned release
// commits it, and is safe to call more than once.
func holdLongSchemaDDL(t *testing.T, databaseURL string) func() {
	t.Helper()
	ctx := context.Background()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		_ = connection.Close(ctx)
	}
	t.Cleanup(release)
	if _, err := connection.Exec(ctx, `create table app.long_running_ddl (id uuid primary key)`); err != nil {
		t.Fatal(err)
	}
	tx, err := connection.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"set local statement_timeout = 0",
		"select version from pgbase_meta.schema_state where singleton for update",
		"alter table app.long_running_ddl add column marker text",
	} {
		if _, err := tx.Exec(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	return func() {
		if released {
			return
		}
		if err := tx.Commit(ctx); err != nil {
			t.Errorf("commit long schema DDL: %v", err)
		}
		release()
	}
}

func requireReady(t *testing.T, url, when string) {
	t.Helper()
	response, err := http.Get(url) // #nosec G107 -- URL is a test-local listener.
	if err != nil {
		t.Fatalf("readiness request %s: %v", when, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("readiness %s = %d, want 200: %s", when, response.StatusCode, body)
	}
}
