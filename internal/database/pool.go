package database

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Pool struct {
	inner     *pgxpool.Pool
	closeOnce sync.Once
	closeDone chan struct{}
}

// PoolStats is a point-in-time snapshot of every statistic exposed by pgxpool.
// Keeping this type independent of pgx lets observability code collect pool
// metrics without gaining access to the underlying pool or its configuration.
type PoolStats struct {
	AcquireCount            int64
	AcquireDuration         time.Duration
	AcquiredConns           int32
	CanceledAcquireCount    int64
	ConstructingConns       int32
	EmptyAcquireCount       int64
	EmptyAcquireWaitTime    time.Duration
	IdleConns               int32
	MaxConns                int32
	MaxIdleDestroyCount     int64
	MaxLifetimeDestroyCount int64
	NewConnsCount           int64
	TotalConns              int32
}

func Open(ctx context.Context, databaseURL string, statementTimeout time.Duration) (*Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL pool configuration: %w", err)
	}
	if config.MaxConns < 2 {
		return nil, fmt.Errorf("parse PostgreSQL pool configuration: pool_max_conns must be at least 2")
	}
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(ctx, "set application_name = 'pgbase'"); err != nil {
			return fmt.Errorf("set application name: %w", err)
		}
		if _, err := conn.Exec(ctx, "set timezone = 'UTC'"); err != nil {
			return fmt.Errorf("set timezone: %w", err)
		}
		// Apply the caller's request timeout last so an intentionally tiny
		// value cannot cancel the remaining connection initialization commands.
		if _, err := conn.Exec(ctx, "select set_config('statement_timeout', $1, false)", postgresDuration(statementTimeout)); err != nil {
			return fmt.Errorf("set statement timeout: %w", err)
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

// postgresDuration avoids Go's duration units (for example "1m30s" and
// "100µs") that PostgreSQL does not accept. A unitless PostgreSQL timeout is
// milliseconds; rounding up keeps a positive sub-millisecond Go duration from
// accidentally disabling the timeout.
func postgresDuration(duration time.Duration) string {
	milliseconds := duration / time.Millisecond
	if duration%time.Millisecond != 0 {
		milliseconds++
	}
	return strconv.FormatInt(int64(milliseconds), 10)
}

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

func (p *Pool) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	return p.inner.Acquire(ctx)
}

func (p *Pool) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	return p.inner.Exec(ctx, sql, arguments...)
}

func (p *Pool) Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error) {
	return p.inner.Query(ctx, sql, arguments...)
}

func (p *Pool) QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row {
	return p.inner.QueryRow(ctx, sql, arguments...)
}

// Stats returns a complete point-in-time snapshot of the pgx pool counters and
// gauges. A nil pool has no connections and returns the zero snapshot.
func (p *Pool) Stats() PoolStats {
	if p == nil || p.inner == nil {
		return PoolStats{}
	}
	stats := p.inner.Stat()
	return PoolStats{
		AcquireCount:            stats.AcquireCount(),
		AcquireDuration:         stats.AcquireDuration(),
		AcquiredConns:           stats.AcquiredConns(),
		CanceledAcquireCount:    stats.CanceledAcquireCount(),
		ConstructingConns:       stats.ConstructingConns(),
		EmptyAcquireCount:       stats.EmptyAcquireCount(),
		EmptyAcquireWaitTime:    stats.EmptyAcquireWaitTime(),
		IdleConns:               stats.IdleConns(),
		MaxConns:                stats.MaxConns(),
		MaxIdleDestroyCount:     stats.MaxIdleDestroyCount(),
		MaxLifetimeDestroyCount: stats.MaxLifetimeDestroyCount(),
		NewConnsCount:           stats.NewConnsCount(),
		TotalConns:              stats.TotalConns(),
	}
}
