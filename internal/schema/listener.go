package schema

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/observability"
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
	recorder observability.Recorder

	afterInitialReload func(Catalog)
}

func NewListener(pool *database.Pool, cache *Cache) *Listener {
	return NewListenerWithRecorder(pool, cache, nil)
}

func NewListenerWithRecorder(pool *database.Pool, cache *Cache, recorder observability.Recorder) *Listener {
	if cache == nil {
		cache = &Cache{}
	}
	return &Listener{pool: pool, cache: cache, retryMin: 100 * time.Millisecond, retryMax: 5 * time.Second, recorder: recorder}
}

func (l *Listener) Run(ctx context.Context) error {
	if l == nil || l.pool == nil || l.cache == nil {
		return ErrSynchronizationUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	l.setReady(ctx, false)
	defer l.setReady(ctx, false)

	minimum, maximum := l.backoffBounds()
	backoff := minimum
	for {
		becameReady, err := l.listenOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		l.setReady(ctx, false)
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
	connectStarted := time.Now()
	connection, err := l.pool.Acquire(ctx)
	if err != nil {
		l.record(ctx, observability.OperationEvent(observability.OperationListenerConnect, listenerOperationOutcome(err), time.Since(connectStarted)))
		return false, fmt.Errorf("acquire schema listener connection: %w", err)
	}
	defer func() {
		l.setReady(ctx, false)
		l.pid.Store(0)
		cleanupCtx, cancel := database.BoundedCleanupContext(ctx)
		defer cancel()
		_ = connection.Conn().Close(cleanupCtx)
		connection.Release()
	}()

	l.pid.Store(connection.Conn().PgConn().PID())
	if _, err := connection.Exec(ctx, "listen "+schemaChangeChannel); err != nil {
		l.record(ctx, observability.OperationEvent(observability.OperationListenerConnect, listenerOperationOutcome(err), time.Since(connectStarted)))
		return false, fmt.Errorf("listen for schema changes: %w", err)
	}
	l.record(ctx, observability.OperationEvent(observability.OperationListenerConnect, observability.OutcomeSuccess, time.Since(connectStarted)))
	catalog, err := l.reload(ctx, connection.Conn())
	if err != nil {
		return false, err
	}
	if l.afterInitialReload != nil {
		l.afterInitialReload(catalog)
	}
	l.setReady(ctx, true)
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

func (l *Listener) reload(ctx context.Context, connection *pgx.Conn) (catalog Catalog, err error) {
	started := time.Now()
	defer l.finishOperation(ctx, observability.OperationListenerReload, started, &err)
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
	current := l.cache.find(version)
	var compiled *compiledCatalog
	if current != nil && current.catalog.Version == version {
		compiled = current
	} else {
		catalog, err = store.ReadCatalog(ctx, version)
		if err != nil {
			return Catalog{}, err
		}
		compiled, err = l.cache.prepare(catalog, maximumCatalogWireBytes)
		if err != nil {
			return Catalog{}, fmt.Errorf("compile schema catalog: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Catalog{}, fmt.Errorf("commit schema catalog reload: %w", err)
	}
	committed = true
	l.cache.publish(compiled)
	return cloneCatalog(compiled.catalog), nil
}

func (l *Listener) setReady(ctx context.Context, ready bool) {
	if l == nil || l.ready.Swap(ready) == ready {
		return
	}
	l.record(ctx, observability.ReadinessEvent(observability.ReadinessSchemaListener, ready))
}

func (l *Listener) record(ctx context.Context, event observability.Event) {
	if l != nil && l.recorder != nil {
		l.recorder.Record(ctx, event)
	}
}

func (l *Listener) finishOperation(ctx context.Context, operation observability.Operation, started time.Time, operationErr *error) {
	if recovered := recover(); recovered != nil {
		l.record(ctx, observability.OperationEvent(operation, observability.OutcomeFailure, time.Since(started)))
		panic(recovered)
	}
	var err error
	if operationErr != nil {
		err = *operationErr
	}
	l.record(ctx, observability.OperationEvent(operation, listenerOperationOutcome(err), time.Since(started)))
}

func listenerOperationOutcome(err error) observability.Outcome {
	if err == nil {
		return observability.OutcomeSuccess
	}
	switch database.Classify(err) {
	case database.KindValidation:
		return observability.OutcomeValidation
	case database.KindConflict:
		return observability.OutcomeConflict
	case database.KindUnavailable:
		return observability.OutcomeUnavailable
	case database.KindCanceled:
		return observability.OutcomeCanceled
	default:
		return observability.OutcomeFailure
	}
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
