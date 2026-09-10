//go:build integration

package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestBackupGateExcludesWritersAndOtherBackupOperations(t *testing.T) {
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	writer, err := TryBackupGate(ctx, databaseURL, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryBackupGate(ctx, databaseURL, true); !errors.Is(err, ErrBackupGateBusy) {
		t.Fatalf("exclusive with writer = %v", err)
	}
	if err := writer.Close(ctx); err != nil {
		t.Fatal(err)
	}
	exclusive, err := TryBackupGate(ctx, databaseURL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer exclusive.Close(ctx)
	if _, err := TryBackupGate(ctx, databaseURL, false); !errors.Is(err, ErrBackupGateBusy) {
		t.Fatalf("writer with exclusive = %v", err)
	}
}

func TestDatabasePoolConnectionsParticipateInBackupGate(t *testing.T) {
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	pool, err := Open(ctx, databaseURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = TryBackupGate(ctx, databaseURL, true); !errors.Is(err, ErrBackupGateBusy) {
		t.Fatalf("exclusive with active pool = %v", err)
	}
	// Losing the standalone lifetime session does not create a backup window:
	// every live/reconnected pool session independently holds the shared lock.
	lifetime, err := TryBackupGate(ctx, databaseURL, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = lifetime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err = pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = TryBackupGate(ctx, databaseURL, true); !errors.Is(err, ErrBackupGateBusy) {
		t.Fatalf("exclusive after lifetime gate loss = %v", err)
	}
	pool.Close()
	exclusive, err := TryBackupGate(ctx, databaseURL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer exclusive.Close(ctx)
	if _, err = Open(ctx, databaseURL, time.Second); !errors.Is(err, ErrBackupGateBusy) {
		t.Fatalf("Open under exclusive gate = %v", err)
	}
}

func TestBackupGateMonitorReportsSessionLoss(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	databaseURL := testutil.NewDatabase(t)
	gate, err := TryBackupGate(ctx, databaseURL, false)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close(context.Background())
	pid := gate.conn.PgConn().PID()
	result := make(chan error, 1)
	go func() { result <- gate.Run(ctx) }()
	terminator, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer terminator.Close(ctx)
	var terminated bool
	if err = terminator.QueryRow(ctx, `select pg_terminate_backend($1)`, pid).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate gate backend = %v, %v", terminated, err)
	}
	select {
	case err = <-result:
		if err == nil {
			t.Fatal("gate monitor returned nil after session loss")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gate monitor did not report session loss")
	}
}
