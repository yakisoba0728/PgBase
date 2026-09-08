package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// BackupGate serializes PgBase application writers with local backup and
// restore operations. Applications hold the shared form for their lifetime;
// backup tooling takes the exclusive form and therefore fails while any
// participating PgBase binary can write.
type BackupGate struct{ conn *pgx.Conn }

const backupGateKey int64 = 0x504742415345424b // "PGBASEBK"

var ErrBackupGateBusy = errors.New("another PgBase writer or backup operation is active")

func TryBackupGate(ctx context.Context, databaseURL string, exclusive bool) (*BackupGate, error) {
	poolConfig, err := parsePoolConfig(databaseURL, true)
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

func (g *BackupGate) Active() bool { return g != nil && g.conn != nil }

// Run fails if the dedicated advisory-lock session is lost. A running server
// uses this as a required background component, so it stops accepting writes
// instead of silently continuing after PostgreSQL releases the lock.
func (g *BackupGate) Run(ctx context.Context) error {
	if g == nil || g.conn == nil {
		return errors.New("PostgreSQL writer gate is not initialized")
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			var alive int
			if err := g.conn.QueryRow(ctx, `select 1`).Scan(&alive); err != nil {
				return fmt.Errorf("check PostgreSQL writer gate: %w", err)
			}
		}
	}
}

func (g *BackupGate) Close(ctx context.Context) error {
	if g == nil || g.conn == nil {
		return nil
	}
	err := g.conn.Close(ctx)
	g.conn = nil
	return err
}
