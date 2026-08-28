package database

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool is the reusable PostgreSQL connection pool for a pgBase project.
type Pool struct {
	inner     *pgxpool.Pool
	closeOnce sync.Once
	closeDone chan struct{}
}

// Open creates a PostgreSQL pool whose connections use pgBase session settings.
func Open(ctx context.Context, databaseURL string, statementTimeout time.Duration) (*Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL pool configuration: %w", err)
	}
	if config.MaxConns < 2 {
		return nil, fmt.Errorf("parse PostgreSQL pool configuration: pool_max_conns must be at least 2")
	}
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(ctx, "select set_config('statement_timeout', $1, false)", statementTimeout.String()); err != nil {
			return fmt.Errorf("set statement timeout: %w", err)
		}
		if _, err := conn.Exec(ctx, "set application_name = 'pgbase'"); err != nil {
			return fmt.Errorf("set application name: %w", err)
		}
		if _, err := conn.Exec(ctx, "set timezone = 'UTC'"); err != nil {
			return fmt.Errorf("set timezone: %w", err)
		}
		return nil
	}

	inner, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}
	pool := &Pool{inner: inner, closeDone: make(chan struct{})}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL pool: %w", err)
	}
	return pool, nil
}

// Ping verifies that the pool can reach PostgreSQL.
func (p *Pool) Ping(ctx context.Context) error {
	return p.inner.Ping(ctx)
}

// Close closes the pool and waits for borrowed connections to be returned.
func (p *Pool) Close() {
	if p == nil {
		return
	}
	p.startClose()
	<-p.closeDone
}

// CloseContext starts closing the pool and waits only until ctx expires.
// Borrowed connections are marked for destruction when they are returned.
func (p *Pool) CloseContext(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.startClose()
	select {
	case <-p.closeDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Pool) startClose() {
	p.closeOnce.Do(func() {
		p.inner.Reset()
		go func() {
			p.inner.Close()
			close(p.closeDone)
		}()
	})
}

// Acquire reserves a connection from the pool until the caller releases it.
func (p *Pool) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	return p.inner.Acquire(ctx)
}

// Exec runs a statement through the pool.
func (p *Pool) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	return p.inner.Exec(ctx, sql, arguments...)
}

// Query runs a query through the pool.
func (p *Pool) Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error) {
	return p.inner.Query(ctx, sql, arguments...)
}

// QueryRow runs a query expected to return at most one row through the pool.
func (p *Pool) QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row {
	return p.inner.QueryRow(ctx, sql, arguments...)
}
