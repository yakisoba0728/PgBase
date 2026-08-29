package schema

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/webisnoteasy/PgBase/internal/database"
)

const schemaAdvisoryLockKey int64 = 0x7067626173650002

var ErrConflict = errors.New("schema plan conflicts with current state")

var ErrCollectionNotFound = errors.New("collection does not exist")

// ErrDataConstraint reports an apply whose DDL failed because committed rows
// violate the requested change (for example SET NOT NULL over NULL values).
var ErrDataConstraint = errors.New("existing data violates the schema change")

type Service struct {
	pool    *database.Pool
	cache   *Cache
	planner PlanBuilder
}

func NewService(pool *database.Pool, cache *Cache, planner PlanBuilder) *Service {
	if cache == nil {
		cache = &Cache{}
	}
	if planner == nil {
		planner = Planner{}
	}
	return &Service{pool: pool, cache: cache, planner: planner}
}

func (s *Service) PlanCreate(ctx context.Context, input CreateCollectionInput) (Plan, error) {
	catalog, err := s.Catalog(ctx)
	if err != nil {
		return Plan{}, err
	}
	return s.planner.PlanCreate(catalog.Version, input)
}

func (s *Service) PlanUpdate(ctx context.Context, input UpdateCollectionInput) (Plan, error) {
	catalog, err := s.Catalog(ctx)
	if err != nil {
		return Plan{}, err
	}
	current, ok := catalog.Collections[input.Name]
	if !ok {
		return Plan{}, ErrCollectionNotFound
	}
	return s.planner.PlanUpdate(catalog.Version, current, input)
}

func (s *Service) PlanDelete(ctx context.Context, name string) (Plan, error) {
	catalog, err := s.Catalog(ctx)
	if err != nil {
		return Plan{}, err
	}
	current, ok := catalog.Collections[name]
	if !ok {
		return Plan{}, ErrCollectionNotFound
	}
	return s.planner.PlanDelete(catalog.Version, current)
}

func (s *Service) ApplyCreate(ctx context.Context, input CreateCollectionInput, checksum string, baseVersion int64, administratorID uuid.UUID) (Collection, error) {
	if err := s.ready(); err != nil {
		return Collection{}, err
	}
	var created Collection
	var nextCatalog Catalog
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "set local lock_timeout = '5s'"); err != nil {
			return fmt.Errorf("set schema lock timeout: %w", err)
		}
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", schemaAdvisoryLockKey); err != nil {
			return fmt.Errorf("acquire schema advisory lock: %w", err)
		}

		store := NewStore(tx)
		version, err := store.SchemaVersion(ctx)
		if err != nil {
			return err
		}
		if version != baseVersion {
			return ErrConflict
		}
		catalog, err := store.ReadCatalog(ctx, version)
		if err != nil {
			return err
		}
		plan, err := s.planner.PlanCreate(catalog.Version, input)
		if err != nil {
			return err
		}
		if err := validateCreateAgainstCatalog(catalog, plan.Definition); err != nil {
			return err
		}
		if plan.Checksum != checksum {
			return ErrConflict
		}

		created, err = newCollectionMetadata(plan.Definition)
		if err != nil {
			return err
		}
		var createdAt time.Time
		if err := tx.QueryRow(ctx, "select current_timestamp").Scan(&createdAt); err != nil {
			return fmt.Errorf("read schema application timestamp: %w", err)
		}
		createdAt = createdAt.UTC()
		created.CreatedAt = createdAt
		started := time.Now()
		for _, statement := range plan.Statements {
			if _, err := tx.Exec(ctx, statement); err != nil {
				return classifyApplyError(fmt.Errorf("execute collection DDL: %w", err))
			}
		}
		if err := store.InsertCollection(ctx, created, plan.Definition, createdAt); err != nil {
			return classifyApplyError(err)
		}
		return s.commitSchemaChange(ctx, tx, store, plan, checksum, administratorID, version, createdAt, started, &nextCatalog)
	})
	if err != nil {
		return Collection{}, err
	}
	s.cache.Replace(nextCatalog)
	return created, nil
}

func (s *Service) ApplyUpdate(ctx context.Context, input UpdateCollectionInput, checksum string, baseVersion int64, administratorID uuid.UUID) (Collection, error) {
	if err := s.ready(); err != nil {
		return Collection{}, err
	}
	var updated Collection
	var nextCatalog Catalog
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "set local lock_timeout = '5s'"); err != nil {
			return fmt.Errorf("set schema lock timeout: %w", err)
		}
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", schemaAdvisoryLockKey); err != nil {
			return fmt.Errorf("acquire schema advisory lock: %w", err)
		}

		store := NewStore(tx)
		version, err := store.SchemaVersion(ctx)
		if err != nil {
			return err
		}
		if version != baseVersion {
			return ErrConflict
		}
		catalog, err := store.ReadCatalog(ctx, version)
		if err != nil {
			return err
		}
		current, ok := catalog.Collections[input.Name]
		if !ok {
			return ErrCollectionNotFound
		}
		if err := validateCatalogIntegrity(catalog); err != nil {
			return err
		}
		plan, err := s.planner.PlanUpdate(catalog.Version, current, input)
		if err != nil {
			return err
		}
		if plan.Checksum != checksum {
			return ErrConflict
		}

		fields, err := newFieldMetadata(plan.Definition)
		if err != nil {
			return err
		}
		updated = Collection{ID: current.ID, Name: plan.Definition.Name, TableName: plan.Definition.TableName, Fields: fields, CreatedAt: current.CreatedAt}
		var createdAt time.Time
		if err := tx.QueryRow(ctx, "select current_timestamp").Scan(&createdAt); err != nil {
			return fmt.Errorf("read schema application timestamp: %w", err)
		}
		createdAt = createdAt.UTC()
		started := time.Now()
		for _, statement := range plan.Statements {
			if _, err := tx.Exec(ctx, statement); err != nil {
				return classifyApplyError(fmt.Errorf("execute collection DDL: %w", err))
			}
		}
		if err := store.UpdateCollection(ctx, updated, plan.Definition, createdAt); err != nil {
			return classifyApplyError(err)
		}
		return s.commitSchemaChange(ctx, tx, store, plan, checksum, administratorID, version, createdAt, started, &nextCatalog)
	})
	if err != nil {
		return Collection{}, err
	}
	s.cache.Replace(nextCatalog)
	return updated, nil
}

func (s *Service) ApplyDelete(ctx context.Context, name string, checksum string, baseVersion int64, administratorID uuid.UUID) error {
	if err := s.ready(); err != nil {
		return err
	}
	var nextCatalog Catalog
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "set local lock_timeout = '5s'"); err != nil {
			return fmt.Errorf("set schema lock timeout: %w", err)
		}
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", schemaAdvisoryLockKey); err != nil {
			return fmt.Errorf("acquire schema advisory lock: %w", err)
		}

		store := NewStore(tx)
		version, err := store.SchemaVersion(ctx)
		if err != nil {
			return err
		}
		if version != baseVersion {
			return ErrConflict
		}
		catalog, err := store.ReadCatalog(ctx, version)
		if err != nil {
			return err
		}
		current, ok := catalog.Collections[name]
		if !ok {
			return ErrCollectionNotFound
		}
		if err := validateCatalogIntegrity(catalog); err != nil {
			return err
		}
		plan, err := s.planner.PlanDelete(catalog.Version, current)
		if err != nil {
			return err
		}
		if plan.Checksum != checksum {
			return ErrConflict
		}

		var createdAt time.Time
		if err := tx.QueryRow(ctx, "select current_timestamp").Scan(&createdAt); err != nil {
			return fmt.Errorf("read schema application timestamp: %w", err)
		}
		createdAt = createdAt.UTC()
		started := time.Now()
		for _, statement := range plan.Statements {
			if _, err := tx.Exec(ctx, statement); err != nil {
				return classifyApplyError(fmt.Errorf("execute collection DDL: %w", err))
			}
		}
		if err := store.DeleteCollection(ctx, current.ID); err != nil {
			return classifyApplyError(err)
		}
		return s.commitSchemaChange(ctx, tx, store, plan, checksum, administratorID, version, createdAt, started, &nextCatalog)
	})
	if err != nil {
		return err
	}
	s.cache.Replace(nextCatalog)
	return nil
}

func (s *Service) commitSchemaChange(ctx context.Context, tx pgx.Tx, store *Store, plan Plan, checksum string, administratorID uuid.UUID, version int64, createdAt time.Time, started time.Time, nextCatalog *Catalog) error {
	checksumBytes, err := hex.DecodeString(checksum)
	if err != nil || len(checksumBytes) != 32 {
		return ErrConflict
	}
	operationID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate schema operation ID: %w", err)
	}
	newVersion := version + 1
	executionMS := time.Since(started).Milliseconds()
	if err := store.InsertMigration(ctx, newVersion, operationID, plan, checksumBytes, administratorID, createdAt, executionMS); err != nil {
		return classifyApplyError(err)
	}
	persistedVersion, err := store.IncrementVersion(ctx)
	if err != nil {
		return err
	}
	if persistedVersion != newVersion {
		return fmt.Errorf("schema version increment mismatch")
	}
	if _, err := tx.Exec(ctx, "select pg_notify('pgbase_schema_changed', $1)", strconv.FormatInt(newVersion, 10)); err != nil {
		return fmt.Errorf("notify schema change: %w", err)
	}
	published, err := store.ReadCatalog(ctx, newVersion)
	if err != nil {
		return fmt.Errorf("build committed schema catalog: %w", err)
	}
	*nextCatalog = published
	return nil
}

func validateCreateAgainstCatalog(catalog Catalog, definition PlannedCollection) error {
	if err := validateCatalogIntegrity(catalog); err != nil {
		return err
	}
	for _, collection := range catalog.Collections {
		if collection.Name == definition.Name || collection.TableName == definition.TableName {
			return ErrConflict
		}
	}
	return nil
}

func validateCatalogIntegrity(catalog Catalog) error {
	for key, collection := range catalog.Collections {
		if key != collection.Name || collection.Name != collection.TableName {
			return ErrConflict
		}
		if err := validateName("", collection.Name); err != nil {
			return ErrConflict
		}
		for ordinal, field := range collection.Fields {
			if field.Ordinal != ordinal || field.Name != field.ColumnName || !isSupportedType(field.Type) {
				return ErrConflict
			}
			if err := validateName("", field.Name); err != nil {
				return ErrConflict
			}
			if len(field.Default) != 0 {
				if _, _, err := normalizeDefault(field.Type, field.Default); err != nil {
					return ErrConflict
				}
			}
		}
	}
	return nil
}

func (s *Service) EnsureCurrent(ctx context.Context, tx database.DBTX) (Catalog, error) {
	if s == nil || s.cache == nil || tx == nil {
		return Catalog{}, fmt.Errorf("schema catalog is unavailable")
	}
	store := NewStore(tx)
	version, err := store.SchemaVersion(ctx)
	if err != nil {
		return Catalog{}, err
	}
	current := s.cache.Load()
	if current.Version == version {
		return current, nil
	}
	catalog, err := store.ReadCatalog(ctx, version)
	if err != nil {
		return Catalog{}, err
	}
	s.cache.Replace(catalog)
	return catalog, nil
}

func (s *Service) Catalog(ctx context.Context) (Catalog, error) {
	if err := s.ready(); err != nil {
		return Catalog{}, err
	}
	var catalog Catalog
	err := s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		var err error
		catalog, err = s.EnsureCurrent(ctx, tx)
		return err
	})
	return catalog, err
}

func (s *Service) ready() error {
	if s == nil || s.pool == nil || s.cache == nil || s.planner == nil {
		return fmt.Errorf("schema service is unavailable")
	}
	return nil
}

func newCollectionMetadata(definition PlannedCollection) (Collection, error) {
	collectionID, err := uuid.NewV7()
	if err != nil {
		return Collection{}, fmt.Errorf("generate collection ID: %w", err)
	}
	fields, err := newFieldMetadata(definition)
	if err != nil {
		return Collection{}, err
	}
	return Collection{
		ID: collectionID, Name: definition.Name, TableName: definition.TableName,
		Fields: fields,
	}, nil
}

func newFieldMetadata(definition PlannedCollection) ([]Field, error) {
	fields := make([]Field, 0, len(definition.Fields))
	for _, planned := range definition.Fields {
		fieldID, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("generate field ID: %w", err)
		}
		fields = append(fields, Field{
			ID: fieldID, Name: planned.Name, ColumnName: planned.Name, Type: planned.Type,
			Required: planned.Required, Unique: planned.Unique, Indexed: planned.Indexed,
			Default: append([]byte(nil), planned.Default...), Ordinal: planned.Ordinal,
		})
	}
	return fields, nil
}

func classifyApplyError(err error) error {
	if database.Classify(err) == database.KindConflict {
		return ErrConflict
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "42P07", "42710":
			return ErrConflict
		case "23502", "23514":
			return ErrDataConstraint
		}
	}
	return err
}
