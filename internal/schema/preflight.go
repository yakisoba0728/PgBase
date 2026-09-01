package schema

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
)

// ErrCatalogInvalid identifies persisted catalog metadata that cannot be
// compiled by this binary. It deliberately carries no catalog values so
// rollout and diagnostic callers can fail closed without leaking metadata.
var ErrCatalogInvalid = errors.New("schema catalog is invalid")

// PreflightCatalog verifies that the current persisted catalog is valid and
// fits the rollout wire budget. When a cache is supplied, the compiled
// snapshot is published only after the read transaction commits so startup
// listeners and request readers can reuse it without compiling the version a
// second time.
func PreflightCatalog(ctx context.Context, pool *database.Pool, cache *Cache) error {
	return preflightCatalogWithLimit(ctx, pool, cache, maximumCatalogWireBytes)
}

func preflightCatalogWithLimit(ctx context.Context, pool *database.Pool, cache *Cache, wireLimit int) error {
	return preflightCatalogWithLimitAndHook(ctx, pool, cache, wireLimit, nil)
}

func preflightCatalogWithLimitAndHook(
	ctx context.Context,
	pool *database.Pool,
	cache *Cache,
	wireLimit int,
	afterPrepare func(),
) error {
	if pool == nil {
		return fmt.Errorf("preflight schema catalog: database pool is nil")
	}

	var compiled *compiledCatalog
	err := pool.InTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	}, func(tx pgx.Tx) error {
		available, err := catalogTablesAvailable(ctx, tx)
		if err != nil {
			return err
		}
		if !available {
			return nil
		}
		store := NewStore(tx)
		version, err := store.SchemaVersion(ctx)
		if err != nil {
			return err
		}
		compiled = cache.find(version)
		if compiled != nil {
			return nil
		}
		catalog, err := store.ReadCatalog(ctx, version)
		if err != nil {
			return classifyCatalogPreflightError(err)
		}
		compiled, err = cache.prepare(catalog, wireLimit)
		err = classifyCatalogPreflightError(err)
		if err == nil && afterPrepare != nil {
			afterPrepare()
		}
		return err
	})
	if err != nil {
		cache.discardPrepared(compiled)
		return fmt.Errorf("preflight schema catalog: %w", err)
	}
	cache.publish(compiled)
	return nil
}

func classifyCatalogPreflightError(err error) error {
	if err == nil || errors.Is(err, ErrCatalogLimitExceeded) {
		return err
	}
	var validationErr *ValidationError
	if errors.Is(err, errCatalogIntegrity) || errors.Is(err, ErrConflict) || errors.As(err, &validationErr) {
		return fmt.Errorf("%w: %w", ErrCatalogInvalid, err)
	}
	return err
}

func catalogTablesAvailable(ctx context.Context, db database.DBTX) (bool, error) {
	var available bool
	if err := db.QueryRow(ctx, `select
		to_regclass('pgbase_meta.schema_state') is not null
		and to_regclass('pgbase_meta.collections') is not null
		and to_regclass('pgbase_meta.fields') is not null
		and to_regclass('pgbase_meta.field_relations') is not null`).Scan(&available); err != nil {
		return false, fmt.Errorf("inspect schema catalog tables: %w", err)
	}
	return available, nil
}
