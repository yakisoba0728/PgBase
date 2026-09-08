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

const (
	schemaChangeChannel           = "pgbase_schema_changed"
	schemaWriterHeartbeatInterval = 30 * time.Second
)

var ErrSynchronizationUnavailable = errors.New("schema catalog synchronization is unavailable")

type Listener struct {
	pool   *database.Pool
	cache  *Cache
	writer *Service
	ready  atomic.Bool

	pid                     atomic.Uint32
	retryMin                time.Duration
	retryMax                time.Duration
	writerHeartbeatInterval time.Duration
	recorder                observability.Recorder

	afterInitialReload func(Catalog)
}

func NewListener(pool *database.Pool, cache *Cache, recorder observability.Recorder, writer *Service) *Listener {
	if cache == nil {
		cache = &Cache{}
	}
	return &Listener{pool: pool, cache: cache, writer: writer,
		retryMin: 100 * time.Millisecond, retryMax: 5 * time.Second,
		writerHeartbeatInterval: schemaWriterHeartbeatInterval,
		recorder:                recorder}
}

func (l *Listener) Run(ctx context.Context) error {
	if l == nil || l.pool == nil || l.cache == nil || l.writer == nil || l.writerHeartbeatInterval <= 0 {
		return ErrSynchronizationUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	l.setReady(ctx, false)
	defer l.setReady(ctx, false)

	backoff := l.retryMin
	for {
		becameReady, err := l.listenOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if becameReady {
			backoff = l.retryMin
		}
		if err == nil {
			return nil
		}
		if err := waitForContext(ctx, backoff); err != nil {
			return ctx.Err()
		}
		backoff = min(backoff*2, l.retryMax)
	}
}

func waitForContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
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
		waitCtx, cancelWait := context.WithTimeout(ctx, l.writerHeartbeatInterval)
		_, waitErr := connection.Conn().WaitForNotification(waitCtx)
		cancelWait()
		if waitErr != nil && !errors.Is(waitErr, context.DeadlineExceeded) {
			return becameReady, fmt.Errorf("wait for schema change notification: %w", waitErr)
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
	if err := l.writer.refreshSchemaWriterLease(ctx, tx); err != nil {
		return Catalog{}, err
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
	return databaseOperationOutcome(err)
}

func databaseOperationOutcome(err error) observability.Outcome {
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
