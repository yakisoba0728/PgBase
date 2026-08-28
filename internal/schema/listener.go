package schema

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
)

const schemaChangeChannel = "pgbase_schema_changed"

var ErrSynchronizationUnavailable = errors.New("schema catalog synchronization is unavailable")

type Listener struct {
	pool  *database.Pool
	cache *Cache
	ready atomic.Bool

	pid      atomic.Uint32
	retryMin time.Duration
	retryMax time.Duration

	afterInitialReload func(Catalog)
}

func NewListener(pool *database.Pool, cache *Cache) *Listener {
	if cache == nil {
		cache = &Cache{}
	}
	return &Listener{pool: pool, cache: cache, retryMin: 100 * time.Millisecond, retryMax: 5 * time.Second}
}

func (l *Listener) Run(ctx context.Context) error {
	if l == nil || l.pool == nil || l.cache == nil {
		return ErrSynchronizationUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	l.ready.Store(false)
	defer l.ready.Store(false)

	minimum, maximum := l.backoffBounds()
	backoff := minimum
	for {
		becameReady, err := l.listenOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		l.ready.Store(false)
		if becameReady {
			backoff = minimum
		}
		if err == nil {
			return nil
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
		if backoff < maximum {
			backoff *= 2
			if backoff > maximum {
				backoff = maximum
			}
		}
	}
}

func (l *Listener) Ready(ctx context.Context) error {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	if l == nil || !l.ready.Load() {
		return ErrSynchronizationUnavailable
	}
	return nil
}

func (*Listener) Component() string { return "schema" }

func (l *Listener) listenOnce(ctx context.Context) (bool, error) {
	connection, err := l.pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("acquire schema listener connection: %w", err)
	}
	defer func() {
		l.ready.Store(false)
		l.pid.Store(0)
		cleanupCtx, cancel := database.BoundedCleanupContext(ctx)
		defer cancel()
		_ = connection.Conn().Close(cleanupCtx)
		connection.Release()
	}()

	l.pid.Store(connection.Conn().PgConn().PID())
	if _, err := connection.Exec(ctx, "listen "+schemaChangeChannel); err != nil {
		return false, fmt.Errorf("listen for schema changes: %w", err)
	}
	catalog, err := l.reload(ctx, connection.Conn())
	if err != nil {
		return false, err
	}
	if l.afterInitialReload != nil {
		l.afterInitialReload(catalog)
	}
	l.ready.Store(true)
	becameReady := true

	for {
		if _, err := connection.Conn().WaitForNotification(ctx); err != nil {
			return becameReady, fmt.Errorf("wait for schema change notification: %w", err)
		}
		if _, err := l.reload(ctx, connection.Conn()); err != nil {
			return becameReady, err
		}
	}
}

func (l *Listener) reload(ctx context.Context, connection *pgx.Conn) (Catalog, error) {
	tx, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return Catalog{}, fmt.Errorf("begin schema catalog reload: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			cleanupCtx, cancel := database.BoundedCleanupContext(ctx)
			defer cancel()
			_ = tx.Rollback(cleanupCtx)
		}
	}()
	store := NewStore(tx)
	version, err := store.SchemaVersion(ctx)
	if err != nil {
		return Catalog{}, err
	}
	catalog, err := store.ReadCatalog(ctx, version)
	if err != nil {
		return Catalog{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Catalog{}, fmt.Errorf("commit schema catalog reload: %w", err)
	}
	committed = true
	l.cache.Replace(catalog)
	return catalog, nil
}

func (l *Listener) backoffBounds() (time.Duration, time.Duration) {
	minimum := l.retryMin
	if minimum <= 0 {
		minimum = 100 * time.Millisecond
	}
	maximum := l.retryMax
	if maximum <= 0 {
		maximum = 5 * time.Second
	}
	if maximum < minimum {
		maximum = minimum
	}
	return minimum, maximum
}
