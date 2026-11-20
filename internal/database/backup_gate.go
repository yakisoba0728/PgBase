package database

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// BackupGate serializes PgBase application writers with local backup and
// restore operations. Applications hold the shared form for their lifetime;
// backup tooling takes the exclusive form and therefore fails while any
// participating PgBase binary can write.
type BackupGate struct {
	mu   sync.Mutex
	conn *pgx.Conn
}

const backupGateKey int64 = 0x504742415345424b         // "PGBASEBK"
const managedRestoreGateKey int64 = 0x5047424153455253 // "PGBASERS"

var ErrBackupGateBusy = errors.New("another PgBase writer or backup operation is active")

func TryBackupGate(ctx context.Context, databaseURL string, exclusive, allowInsecure bool) (*BackupGate, error) {
	poolConfig, err := parsePoolConfig(databaseURL, allowInsecure)
	if err != nil {
		return nil, err
	}
	config := poolConfig.ConnConfig.Copy()
	config.RuntimeParams["application_name"] = "pgbase-backup-gate"
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("connect PostgreSQL writer gate ([REDACTED]): %w", err)
	}
	query := `select pg_try_advisory_lock_shared($1)`
	if exclusive {
		query = `select pg_try_advisory_lock($1)`
	}
	var acquired bool
	if err := conn.QueryRow(ctx, query, backupGateKey).Scan(&acquired); err != nil {
		_ = conn.Close(context.Background())
		return nil, err
	}
	if !acquired {
		_ = conn.Close(context.Background())
		return nil, ErrBackupGateBusy
	}
	return &BackupGate{conn: conn}, nil
}

func (g *BackupGate) Active() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.conn != nil
}

// AdmitApplication coordinates live restore with application startup without
// involving the pool connections needed to drain and close the old target.
func (g *BackupGate) AdmitApplication(ctx context.Context) error {
	return g.restoreLock(ctx, `select pg_catalog.pg_try_advisory_lock_shared($1)`)
}

// BeginRestoreActivation upgrades the application's own shared admission lock.
// A session may hold both modes, so any other application's lock blocks it.
func (g *BackupGate) BeginRestoreActivation(ctx context.Context) error {
	return g.restoreLock(ctx, `select pg_catalog.pg_try_advisory_lock($1)`)
}

func (g *BackupGate) CancelRestoreActivation(ctx context.Context) error {
	return g.restoreLock(ctx, `select pg_catalog.pg_advisory_unlock($1)`)
}

func (g *BackupGate) restoreLock(ctx context.Context, sql string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.conn == nil {
		return errors.New("PostgreSQL writer gate is closed")
	}
	var acquired bool
	if err := g.conn.QueryRow(ctx, sql, managedRestoreGateKey).Scan(&acquired); err != nil {
		return err
	}
	if !acquired {
		return ErrBackupGateBusy
	}
	return nil
}

// QueryRow uses the gate's connection for preflight before monitoring starts.
func (g *BackupGate) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return g.conn.QueryRow(ctx, query, args...)
}

// Run fails if the dedicated advisory-lock session is lost. A running server
// uses this as a required background component, so it stops accepting writes
// instead of silently continuing after PostgreSQL releases the lock.
func (g *BackupGate) Run(ctx context.Context) error {
	if !g.Active() {
		return errors.New("PostgreSQL writer gate is not initialized")
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := g.ping(ctx); err != nil {
				return fmt.Errorf("check PostgreSQL writer gate: %w", err)
			}
		}
	}
}

func (g *BackupGate) ping(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.conn == nil {
		return errors.New("PostgreSQL writer gate is closed")
	}
	var alive int
	return g.conn.QueryRow(ctx, `select 1`).Scan(&alive)
}

func (g *BackupGate) Close(ctx context.Context) error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.conn == nil {
		return nil
	}
	err := g.conn.Close(ctx)
	g.conn = nil
	return err
}
