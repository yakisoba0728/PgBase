package database

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"runtime"
	"strconv"
	"strings"
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

	coordinationMu     sync.Mutex
	coordination       *pgxpool.Pool
	coordinationSlots  chan struct{}
	coordinationClosed bool
}

// Begin starts a caller-owned transaction on an acquired pooled connection.
// The caller must commit or roll it back to release the connection.
func (p *Pool) Begin(ctx context.Context) (pgx.Tx, error) {
	if p == nil || p.inner == nil {
		return nil, errors.New("database pool is unavailable")
	}
	return p.inner.Begin(ctx)
}

const minimumPoolConnections int32 = 4

// backgroundPoolHeadroom is the number of pool connections set aside for the
// background loops (outbox delivery, file maintenance, cleaners, samplers,
// schema operations, the listener's reconnect) that can hold a connection at
// the same moment as request handlers.
const backgroundPoolHeadroom = 8

// maximumDefaultPoolConnections caps the derived default so a large host does
// not claim most of PostgreSQL's default max_connections=100 by itself. An
// explicit pool_max_conns is never capped.
const maximumDefaultPoolConnections = 64

// DefaultPoolConnections is the pool size used when the database URL omits
// pool_max_conns: two connections per schedulable CPU for request work plus the
// background headroom, at most 64. gomaxprocs is runtime.GOMAXPROCS(0), which
// follows a container's CPU limit.
func DefaultPoolConnections(gomaxprocs int) int32 {
	return int32(min(2*max(gomaxprocs, 1)+backgroundPoolHeadroom, maximumDefaultPoolConnections))
}

// RecommendedMinimumPoolConnections is the smallest pool that still leaves one
// request connection per schedulable CPU beside the background headroom. A
// smaller pool queues requests behind background work under load.
func RecommendedMinimumPoolConnections(gomaxprocs int) int32 {
	return int32(min(max(gomaxprocs, 1)+backgroundPoolHeadroom, maximumDefaultPoolConnections))
}

var errPoolConfiguration = errors.New("parse PostgreSQL pool configuration")

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

// Open constructs a pool under the secure transport policy. Existing callers
// omit allowInsecureTransport and get the fail-closed default; the optional
// fourth argument is only for the explicit development policy loaded by the
// application boundary.
func Open(
	ctx context.Context,
	databaseURL string,
	statementTimeout time.Duration,
	allowInsecureTransport ...bool,
) (*Pool, error) {
	if len(allowInsecureTransport) > 1 {
		return nil, fmt.Errorf("%w: at most one insecure-transport policy may be supplied", errPoolConfiguration)
	}
	allowInsecure := len(allowInsecureTransport) == 1 && allowInsecureTransport[0]
	config, err := parsePoolConfig(databaseURL, allowInsecure)
	if err != nil {
		return nil, err
	}
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		var admitted bool
		if err := conn.QueryRow(ctx, `select pg_catalog.pg_try_advisory_lock_shared($1)`, backupGateKey).Scan(&admitted); err != nil {
			return fmt.Errorf("acquire PostgreSQL writer gate: %w", err)
		}
		if !admitted {
			return ErrBackupGateBusy
		}
		return configureSession(ctx, conn.Exec, statementTimeout)
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

func configureSession(
	ctx context.Context,
	exec func(context.Context, string, ...any) (pgconn.CommandTag, error),
	statementTimeout time.Duration,
) error {
	// Pin object resolution before running any SQL that resolves a function or
	// relation. All application relations are schema-qualified; pg_catalog is
	// the only implicit namespace a pooled session needs.
	if _, err := exec(ctx, "set search_path = pg_catalog"); err != nil {
		return fmt.Errorf("set safe search path: %w", err)
	}
	if _, err := exec(ctx, "set application_name = 'pgbase'"); err != nil {
		return fmt.Errorf("set application name: %w", err)
	}
	if _, err := exec(ctx, "set timezone = 'UTC'"); err != nil {
		return fmt.Errorf("set timezone: %w", err)
	}
	// Apply the caller's request timeout last so an intentionally tiny value
	// cannot cancel the remaining connection initialization commands.
	if _, err := exec(ctx, "select pg_catalog.set_config('statement_timeout', $1, false)", postgresDuration(statementTimeout)); err != nil {
		return fmt.Errorf("set statement timeout: %w", err)
	}
	return nil
}

func parsePoolConfig(databaseURL string, allowInsecure bool) (*pgxpool.Config, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errPoolConfiguration, err)
	}
	if !poolMaxConnsSpecified(databaseURL) {
		config.MaxConns = DefaultPoolConnections(runtime.GOMAXPROCS(0))
	}
	if config.MaxConns < minimumPoolConnections {
		return nil, fmt.Errorf("%w: pool_max_conns must be at least %d", errPoolConfiguration, minimumPoolConnections)
	}
	if err := validateDatabaseTransport(config, allowInsecure); err != nil {
		return nil, fmt.Errorf("%w: %w", errPoolConfiguration, err)
	}
	return config, nil
}

// poolMaxConnsSpecified reports whether the connection string sets
// pool_max_conns itself. Without it pgxpool substitutes max(4, NumCPU), and
// NumCPU ignores a container's CPU quota.
func poolMaxConnsSpecified(connString string) bool {
	if strings.HasPrefix(connString, "postgres://") || strings.HasPrefix(connString, "postgresql://") {
		_, rawQuery, ok := strings.Cut(connString, "?")
		if !ok {
			return false
		}
		rawQuery, _, _ = strings.Cut(rawQuery, "#")
		query, err := url.ParseQuery(rawQuery)
		// ParseConfig already accepted the string. If this simpler parse
		// disagrees, keep what pgx parsed instead of overriding it.
		return err != nil || query.Has("pool_max_conns")
	}
	return keywordPoolMaxConns.MatchString(connString)
}

var keywordPoolMaxConns = regexp.MustCompile(`(?:^|\s)pool_max_conns\s*=`)

func validateDatabaseTransport(config *pgxpool.Config, allowInsecure bool) error {
	if allowInsecure {
		return nil
	}
	if config == nil || config.ConnConfig == nil {
		return fmt.Errorf("database connection configuration is missing")
	}
	if !databaseTargetUsesVerifiedTLS(config.ConnConfig.Host, config.ConnConfig.Port, config.ConnConfig.TLSConfig) {
		return fmt.Errorf("remote PostgreSQL must use sslmode=verify-full; insecure transport is allowed only by explicit development policy")
	}
	for _, fallback := range config.ConnConfig.Fallbacks {
		if fallback == nil || !databaseTargetUsesVerifiedTLS(fallback.Host, fallback.Port, fallback.TLSConfig) {
			return fmt.Errorf("every remote PostgreSQL fallback must use sslmode=verify-full; insecure transport is allowed only by explicit development policy")
		}
	}
	return nil
}

func databaseTargetUsesVerifiedTLS(host string, port uint16, tlsConfig *tls.Config) bool {
	if network, _ := pgconn.NetworkAddress(host, port); network == "unix" {
		return true
	}
	hostname := strings.TrimSuffix(strings.ToLower(host), ".")
	if hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") {
		return true
	}
	if address, err := netip.ParseAddr(hostname); err == nil && address.Unmap().IsLoopback() {
		return true
	}
	return tlsConfig != nil && !tlsConfig.InsecureSkipVerify && tlsConfig.ServerName != ""
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

// OpenCoordinationSession opens a connection outside the request pool for a
// session-scoped advisory lock. Long-lived coordination must not borrow one of
// the bounded request connections or concurrent workers can exhaust the pool
// while each waits for a transaction on that same pool.
func (p *Pool) OpenCoordinationSession(ctx context.Context) (*pgx.Conn, error) {
	if p == nil || p.inner == nil {
		return nil, errors.New("database pool is unavailable")
	}
	config := p.inner.Config().ConnConfig.Copy()
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := admitCoordinationSession(ctx, conn); err != nil {
		_ = conn.Close(context.Background())
		return nil, err
	}
	return conn, nil
}

// admitCoordinationSession takes the backup-writer gate a coordination session
// holds for its lifetime, like every request pool connection.
func admitCoordinationSession(ctx context.Context, conn *pgx.Conn) error {
	var admitted bool
	if err := conn.QueryRow(ctx, `select pg_catalog.pg_try_advisory_lock_shared($1)`, backupGateKey).Scan(&admitted); err != nil {
		return err
	}
	if !admitted {
		return ErrBackupGateBusy
	}
	return nil
}

// CloseCoordinationSession acknowledges lock release before closing the socket.
// pgx Close alone does not wait for PostgreSQL to process session termination.
func CloseCoordinationSession(ctx context.Context, conn *pgx.Conn) error {
	cleanup, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, unlockErr := conn.Exec(cleanup, `select pg_catalog.pg_advisory_unlock_all()`)
	return errors.Join(unlockErr, conn.Close(cleanup))
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
		p.coordinationMu.Lock()
		coordination := p.coordination
		p.coordinationClosed = true
		p.coordinationMu.Unlock()
		p.inner.Reset()
		if coordination != nil {
			coordination.Reset()
		}
		go func() {
			p.inner.Close()
			if coordination != nil {
				coordination.Close()
			}
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
