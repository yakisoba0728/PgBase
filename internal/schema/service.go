package schema

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/observability"
)

const schemaAdvisoryLockKey int64 = 0x7067626173650002

const (
	maximumCatalogRetries    = 32
	maximumCatalogRetryDelay = 100 * time.Millisecond
)

var ErrConflict = errors.New("schema plan conflicts with current state")

var ErrCollectionNotFound = errors.New("collection does not exist")

// ErrDataConstraint reports an apply whose DDL failed because committed rows
// violate the requested change (for example SET NOT NULL over NULL values).
var ErrDataConstraint = errors.New("existing data violates the schema change")

type Service struct {
	pool     *database.Pool
	cache    *Cache
	planner  PlanBuilder
	recorder observability.Recorder
}

// catalogPlanBuilder is an optional deepening of the legacy PlanBuilder seam.
// Scalar-only test doubles keep the original small interface, while the
// production Planner receives the exact pinned catalog required to resolve
// relation targets and incoming dependencies deterministically.
type catalogPlanBuilder interface {
	PlanCreateWithCatalog(int64, Catalog, CreateCollectionInput) (Plan, error)
	PlanUpdateWithCatalog(int64, Catalog, Collection, UpdateCollectionInput) (Plan, error)
	PlanDeleteWithCatalog(int64, Catalog, Collection) (Plan, error)
}

func NewService(pool *database.Pool, cache *Cache, planner PlanBuilder) *Service {
	return NewServiceWithRecorder(pool, cache, planner, nil)
}

func NewServiceWithRecorder(pool *database.Pool, cache *Cache, planner PlanBuilder, recorder observability.Recorder) *Service {
	if cache == nil {
		cache = &Cache{}
	}
	if planner == nil {
		planner = Planner{}
	}
	return &Service{pool: pool, cache: cache, planner: planner, recorder: recorder}
}

func planCreateAgainstCatalog(planner PlanBuilder, catalog Catalog, input CreateCollectionInput) (Plan, error) {
	if relationPlanner, ok := planner.(catalogPlanBuilder); ok {
		return relationPlanner.PlanCreateWithCatalog(catalog.Version, catalog, input)
	}
	return planner.PlanCreate(catalog.Version, input)
}

func planUpdateAgainstCatalog(planner PlanBuilder, catalog Catalog, current Collection, input UpdateCollectionInput) (Plan, error) {
	if relationPlanner, ok := planner.(catalogPlanBuilder); ok {
		return relationPlanner.PlanUpdateWithCatalog(catalog.Version, catalog, current, input)
	}
	return planner.PlanUpdate(catalog.Version, current, input)
}

func planDeleteAgainstCatalog(planner PlanBuilder, catalog Catalog, current Collection) (Plan, error) {
	if relationPlanner, ok := planner.(catalogPlanBuilder); ok {
		return relationPlanner.PlanDeleteWithCatalog(catalog.Version, catalog, current)
	}
	return planner.PlanDelete(catalog.Version, current)
}

func (s *Service) PlanCreate(ctx context.Context, input CreateCollectionInput) (plan Plan, err error) {
	operationStarted := time.Now()
	relationOperation := inputHasRelationField(input.Fields)
	defer s.finishRelationSchemaOperation(ctx, operationStarted, &err, &relationOperation)
	catalog, err := s.Catalog(ctx)
	if err != nil {
		return Plan{}, err
	}
	return planCreateAgainstCatalog(s.planner, catalog, input)
}

func (s *Service) PlanUpdate(ctx context.Context, input UpdateCollectionInput) (plan Plan, err error) {
	operationStarted := time.Now()
	relationOperation := inputHasRelationField(input.Fields)
	defer s.finishRelationSchemaOperation(ctx, operationStarted, &err, &relationOperation)
	catalog, err := s.Catalog(ctx)
	if err != nil {
		return Plan{}, err
	}
	current, ok := catalog.Collections[input.Name]
	if !ok {
		return Plan{}, ErrCollectionNotFound
	}
	relationOperation = relationOperation || collectionHasRelationField(current) || len(catalog.Incoming[current.ID]) != 0
	plan, err = planUpdateAgainstCatalog(s.planner, catalog, current, input)
	if err != nil {
		return Plan{}, err
	}
	if err := validateRequiredRelationAdds(ctx, s.pool, current, plan.Definition, false); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func (s *Service) PlanDelete(ctx context.Context, name string) (plan Plan, err error) {
	operationStarted := time.Now()
	relationOperation := false
	defer s.finishRelationSchemaOperation(ctx, operationStarted, &err, &relationOperation)
	catalog, err := s.Catalog(ctx)
	if err != nil {
		return Plan{}, err
	}
	current, ok := catalog.Collections[name]
	if !ok {
		return Plan{}, ErrCollectionNotFound
	}
	relationOperation = collectionHasRelationField(current) || len(catalog.Incoming[current.ID]) != 0
	return planDeleteAgainstCatalog(s.planner, catalog, current)
}

func (s *Service) ApplyCreate(ctx context.Context, input CreateCollectionInput, checksum string, baseVersion int64, administratorID uuid.UUID) (created Collection, err error) {
	operationStarted := time.Now()
	relationOperation := inputHasRelationField(input.Fields)
	defer s.finishOperation(ctx, observability.OperationSchemaCreate, operationStarted, &err)
	defer s.finishRelationSchemaOperation(ctx, operationStarted, &err, &relationOperation)
	if err = s.ready(); err != nil {
		return Collection{}, err
	}
	var nextCatalog Catalog
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "set local lock_timeout = '5s'"); err != nil {
			return fmt.Errorf("set schema lock timeout: %w", err)
		}
		if _, err := tx.Exec(ctx, "set local statement_timeout = 0"); err != nil {
			return fmt.Errorf("disable request statement timeout for schema DDL: %w", err)
		}
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", schemaAdvisoryLockKey); err != nil {
			return fmt.Errorf("acquire schema advisory lock: %w", err)
		}

		store := NewStore(tx)
		version, err := store.LockedSchemaVersion(ctx)
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
		if err := validateCatalogIntegrity(catalog); err != nil {
			return err
		}
		plan, err := planCreateAgainstCatalog(s.planner, catalog, input)
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
				return classifyDDLApplyError(fmt.Errorf("execute collection DDL: %w", err))
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

func (s *Service) ApplyUpdate(ctx context.Context, input UpdateCollectionInput, checksum string, baseVersion int64, administratorID uuid.UUID) (updated Collection, err error) {
	operationStarted := time.Now()
	relationOperation := inputHasRelationField(input.Fields)
	defer s.finishOperation(ctx, observability.OperationSchemaUpdate, operationStarted, &err)
	defer s.finishRelationSchemaOperation(ctx, operationStarted, &err, &relationOperation)
	if err = s.ready(); err != nil {
		return Collection{}, err
	}
	var nextCatalog Catalog
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "set local lock_timeout = '5s'"); err != nil {
			return fmt.Errorf("set schema lock timeout: %w", err)
		}
		if _, err := tx.Exec(ctx, "set local statement_timeout = 0"); err != nil {
			return fmt.Errorf("disable request statement timeout for schema DDL: %w", err)
		}
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", schemaAdvisoryLockKey); err != nil {
			return fmt.Errorf("acquire schema advisory lock: %w", err)
		}

		store := NewStore(tx)
		version, err := store.LockedSchemaVersion(ctx)
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
		relationOperation = relationOperation || collectionHasRelationField(current) || len(catalog.Incoming[current.ID]) != 0
		if err := validateCatalogIntegrity(catalog); err != nil {
			return err
		}
		plan, err := planUpdateAgainstCatalog(s.planner, catalog, current, input)
		if err != nil {
			return err
		}
		if plan.Checksum != checksum {
			return ErrConflict
		}
		if err := validateRequiredRelationAdds(ctx, tx, current, plan.Definition, true); err != nil {
			return err
		}

		fields, err := updatedFieldMetadata(current.ID, current.Fields, plan.Definition)
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
				return classifyDDLApplyError(fmt.Errorf("execute collection DDL: %w", err))
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

func validateRequiredRelationAdds(
	ctx context.Context,
	db database.DBTX,
	current Collection,
	desired PlannedCollection,
	lock bool,
) error {
	ordinal, added := requiredRelationAddOrdinal(current, desired)
	if !added {
		return nil
	}
	table := pgx.Identifier{"app", current.TableName}.Sanitize()
	if lock {
		// Keep writers out between the emptiness check and relation DDL.
		// The subsequent ALTER TABLE needs this lock strength anyway.
		if _, err := db.Exec(ctx, "LOCK TABLE "+table+" IN ACCESS EXCLUSIVE MODE"); err != nil {
			return fmt.Errorf("lock collection for required relation: %w", err)
		}
	}
	var hasRows bool
	if err := db.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM "+table+" LIMIT 1)").Scan(&hasRows); err != nil {
		return fmt.Errorf("inspect collection for required relation: %w", err)
	}
	if hasRows {
		return validation(fmt.Sprintf("fields.%d.required", ordinal), "relation_required_backfill_needed")
	}
	return nil
}

func requiredRelationAddOrdinal(current Collection, desired PlannedCollection) (int, bool) {
	existing := make(map[string]struct{}, len(current.Fields))
	for _, field := range current.Fields {
		existing[field.Name] = struct{}{}
	}
	for ordinal, field := range desired.Fields {
		if _, ok := existing[field.Name]; ok {
			continue
		}
		if field.Type == TypeRelation && field.Required {
			return ordinal, true
		}
	}
	return 0, false
}

func (s *Service) ApplyDelete(ctx context.Context, name string, checksum string, baseVersion int64, administratorID uuid.UUID) (err error) {
	operationStarted := time.Now()
	relationOperation := false
	defer s.finishOperation(ctx, observability.OperationSchemaDelete, operationStarted, &err)
	defer s.finishRelationSchemaOperation(ctx, operationStarted, &err, &relationOperation)
	if err = s.ready(); err != nil {
		return err
	}
	var nextCatalog Catalog
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "set local lock_timeout = '5s'"); err != nil {
			return fmt.Errorf("set schema lock timeout: %w", err)
		}
		if _, err := tx.Exec(ctx, "set local statement_timeout = 0"); err != nil {
			return fmt.Errorf("disable request statement timeout for schema DDL: %w", err)
		}
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", schemaAdvisoryLockKey); err != nil {
			return fmt.Errorf("acquire schema advisory lock: %w", err)
		}

		store := NewStore(tx)
		version, err := store.LockedSchemaVersion(ctx)
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
		relationOperation = collectionHasRelationField(current) || len(catalog.Incoming[current.ID]) != 0
		if err := validateCatalogIntegrity(catalog); err != nil {
			return err
		}
		plan, err := planDeleteAgainstCatalog(s.planner, catalog, current)
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
				return classifyDDLApplyError(fmt.Errorf("execute collection DDL: %w", err))
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
	if err := validateCatalogIntegrity(published); err != nil {
		return fmt.Errorf("validate committed schema catalog: %w", err)
	}
	*nextCatalog = published
	return nil
}

func (s *Service) finishOperation(ctx context.Context, operation observability.Operation, started time.Time, operationErr *error) {
	if recovered := recover(); recovered != nil {
		s.record(ctx, observability.OperationEvent(observability.ComponentSchema, operation, observability.OutcomeFailure, time.Since(started)))
		panic(recovered)
	}
	var err error
	if operationErr != nil {
		err = *operationErr
	}
	s.record(ctx, observability.OperationEvent(observability.ComponentSchema, operation, schemaOperationOutcome(err), time.Since(started)))
}

func (s *Service) finishRelationSchemaOperation(ctx context.Context, started time.Time, operationErr *error, enabled *bool) {
	if recovered := recover(); recovered != nil {
		if enabled != nil && *enabled {
			s.record(ctx, observability.OperationEvent(observability.ComponentRelation, observability.OperationRelationSchema, observability.OutcomeFailure, time.Since(started)))
		}
		panic(recovered)
	}
	if enabled == nil || !*enabled {
		return
	}
	var err error
	if operationErr != nil {
		err = *operationErr
	}
	s.record(ctx, observability.OperationEvent(observability.ComponentRelation, observability.OperationRelationSchema, schemaOperationOutcome(err), time.Since(started)))
}

func inputHasRelationField(fields []FieldInput) bool {
	for _, field := range fields {
		if field.Type == TypeRelation || field.Relation != nil {
			return true
		}
	}
	return false
}

func collectionHasRelationField(collection Collection) bool {
	for _, field := range collection.Fields {
		if field.Type == TypeRelation || field.Relation != nil {
			return true
		}
	}
	return false
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
	collectionIDs := make(map[uuid.UUID]struct{}, len(catalog.Collections))
	tableNames := make(map[string]struct{}, len(catalog.Collections))
	for key, collection := range catalog.Collections {
		if key != collection.Name || collection.Name != collection.TableName {
			return ErrConflict
		}
		if err := validateName("", collection.Name); err != nil {
			return ErrConflict
		}
		if collection.ID == uuid.Nil {
			return ErrConflict
		}
		if _, duplicate := collectionIDs[collection.ID]; duplicate {
			return ErrConflict
		}
		collectionIDs[collection.ID] = struct{}{}
		if _, duplicate := tableNames[collection.TableName]; duplicate {
			return ErrConflict
		}
		tableNames[collection.TableName] = struct{}{}
	}
	expectedIncoming := make(map[RelationRef]int)
	fieldIDs := make(map[uuid.UUID]struct{})
	for _, collection := range catalog.Collections {
		fieldNames := make(map[string]struct{}, len(collection.Fields))
		columnNames := make(map[string]struct{}, len(collection.Fields))
		for ordinal, field := range collection.Fields {
			if field.ID == uuid.Nil || field.Ordinal != ordinal || field.Name != field.ColumnName || !isSupportedType(field.Type) {
				return ErrConflict
			}
			if _, duplicate := fieldIDs[field.ID]; duplicate {
				return ErrConflict
			}
			fieldIDs[field.ID] = struct{}{}
			if _, duplicate := fieldNames[field.Name]; duplicate {
				return ErrConflict
			}
			fieldNames[field.Name] = struct{}{}
			if _, duplicate := columnNames[field.ColumnName]; duplicate {
				return ErrConflict
			}
			columnNames[field.ColumnName] = struct{}{}
			if err := validateName("", field.Name); err != nil {
				return ErrConflict
			}
			if len(field.Default) != 0 {
				// Stored defaults carry the bounds of the binary that wrote
				// them, not this one. A bound added after a release must not
				// reach this check: it runs on every EnsureCurrent, so one
				// legacy column would turn into ErrConflict for every schema
				// and record operation in the database.
				if _, _, err := normalizeStoredDefault(field.Type, field.Default); err != nil {
					return ErrConflict
				}
			}
			if field.Type != TypeRelation {
				if field.Relation != nil {
					return ErrConflict
				}
				continue
			}
			if field.Relation == nil || len(field.Default) != 0 || !field.Indexed ||
				field.Required != (field.Relation.MinSelect > 0) ||
				(field.Unique && field.Relation.Cardinality != RelationSingle) {
				return ErrConflict
			}
			if err := ValidateRelationSpec("field", *field.Relation); err != nil {
				return ErrConflict
			}
			if _, ok := collectionIDs[field.Relation.TargetCollectionID]; !ok {
				return ErrConflict
			}
			expectedIncoming[RelationRef{
				SourceCollectionID: collection.ID,
				SourceFieldID:      field.ID,
				Spec:               *field.Relation,
			}]++
		}
	}
	for targetCollectionID, incoming := range catalog.Incoming {
		if _, ok := collectionIDs[targetCollectionID]; !ok || len(incoming) == 0 {
			return ErrConflict
		}
		for _, relation := range incoming {
			if relation.Spec.TargetCollectionID != targetCollectionID || expectedIncoming[relation] == 0 {
				return ErrConflict
			}
			expectedIncoming[relation]--
		}
	}
	for _, count := range expectedIncoming {
		if count != 0 {
			return ErrConflict
		}
	}
	return nil
}

func (s *Service) EnsureCurrent(ctx context.Context, tx database.DBTX) (Catalog, error) {
	if s == nil || s.cache == nil || tx == nil {
		return Catalog{}, fmt.Errorf("schema catalog is unavailable")
	}
	store := NewStore(tx)
	version, err := store.PinnedSchemaVersion(ctx)
	if err != nil {
		return Catalog{}, err
	}
	current := s.cache.Load()
	if current.Version == version {
		if err := validateCatalogIntegrity(current); err == nil {
			return current, nil
		}
	}
	catalog, err := store.ReadCatalog(ctx, version)
	if err != nil {
		return Catalog{}, err
	}
	if err := validateCatalogIntegrity(catalog); err != nil {
		return Catalog{}, err
	}
	s.cache.Replace(catalog)
	return catalog, nil
}

// Catalog reads the current catalog from one pinned snapshot. An apply that
// commits its version bump while this reader waits aborts it with a
// serialization failure, so the read retries with a fresh snapshot exactly like
// the record reader's inTxCatalog instead of surfacing 40001 as unavailable.
func (s *Service) Catalog(ctx context.Context) (Catalog, error) {
	if err := s.ready(); err != nil {
		return Catalog{}, err
	}
	var catalog Catalog
	for attempt := 0; ; attempt++ {
		err := s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
			var err error
			catalog, err = s.EnsureCurrent(ctx, tx)
			return err
		})
		cause, retryable := catalogRetryCause(err)
		if err == nil || !retryable {
			return catalog, err
		}
		if attempt >= maximumCatalogRetries {
			s.record(ctx, observability.TransactionRetryEvent(cause, true))
			return Catalog{}, err
		}

		timer := time.NewTimer(catalogRetryDelay(attempt))
		select {
		case <-timer.C:
			if err := ctx.Err(); err != nil {
				return Catalog{}, err
			}
			s.record(ctx, observability.TransactionRetryEvent(cause, false))
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return Catalog{}, ctx.Err()
		}
	}
}

func catalogRetryDelay(attempt int) time.Duration {
	delay := 2 * time.Millisecond
	for step := 0; step < attempt && delay < maximumCatalogRetryDelay; step++ {
		delay *= 2
		if delay > maximumCatalogRetryDelay {
			delay = maximumCatalogRetryDelay
		}
	}
	half := delay / 2
	return half + time.Duration(rand.Int64N(int64(delay-half)+1))
}

func catalogRetryCause(err error) (observability.RetryCause, bool) {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return "", false
	}
	switch postgresError.Code {
	case "0A000":
		return observability.RetryCauseCachedPlan, true
	case "40001":
		return observability.RetryCauseSerialization, true
	case "40P01":
		return observability.RetryCauseDeadlock, true
	default:
		return "", false
	}
}

func (s *Service) ready() error {
	if s == nil || s.pool == nil || s.cache == nil || s.planner == nil {
		return fmt.Errorf("schema service is unavailable")
	}
	return nil
}

func (s *Service) record(ctx context.Context, event observability.Event) {
	if s != nil && s.recorder != nil {
		s.recorder.Record(ctx, event)
	}
}

func schemaOperationOutcome(err error) observability.Outcome {
	if err == nil {
		return observability.OutcomeSuccess
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return observability.OutcomeCanceled
	}
	if errors.Is(err, ErrConflict) {
		return observability.OutcomeConflict
	}
	if errors.Is(err, ErrDataConstraint) || errors.Is(err, ErrCollectionNotFound) {
		return observability.OutcomeValidation
	}
	var validationErr *ValidationError
	if errors.As(err, &validationErr) {
		return observability.OutcomeValidation
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

func newCollectionMetadata(definition PlannedCollection) (Collection, error) {
	collectionID, err := uuid.NewV7()
	if err != nil {
		return Collection{}, fmt.Errorf("generate collection ID: %w", err)
	}
	fields, err := newFieldMetadata(collectionID, definition)
	if err != nil {
		return Collection{}, err
	}
	return Collection{
		ID: collectionID, Name: definition.Name, TableName: definition.TableName,
		Fields: fields,
	}, nil
}

func newFieldMetadata(collectionID uuid.UUID, definition PlannedCollection) ([]Field, error) {
	fields := make([]Field, 0, len(definition.Fields))
	for ordinal, planned := range definition.Fields {
		fieldID, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("generate field ID: %w", err)
		}
		relation, err := resolvedPlannedRelation(fmt.Sprintf("fields.%d", ordinal), collectionID, planned)
		if err != nil {
			return nil, err
		}
		fields = append(fields, Field{
			ID: fieldID, Name: planned.Name, ColumnName: planned.Name, Type: planned.Type,
			Required: planned.Required, Unique: planned.Unique, Indexed: planned.Indexed,
			Default: append([]byte(nil), planned.Default...), Relation: relation, Ordinal: planned.Ordinal,
		})
	}
	return fields, nil
}

func updatedFieldMetadata(collectionID uuid.UUID, existing []Field, definition PlannedCollection) ([]Field, error) {
	existingIDs := make(map[string]uuid.UUID, len(existing))
	for _, field := range existing {
		existingIDs[field.Name] = field.ID
	}
	fields := make([]Field, 0, len(definition.Fields))
	for ordinal, planned := range definition.Fields {
		fieldID, ok := existingIDs[planned.Name]
		if !ok {
			var err error
			fieldID, err = uuid.NewV7()
			if err != nil {
				return nil, fmt.Errorf("generate field ID: %w", err)
			}
		}
		relation, err := resolvedPlannedRelation(fmt.Sprintf("fields.%d", ordinal), collectionID, planned)
		if err != nil {
			return nil, err
		}
		fields = append(fields, Field{
			ID: fieldID, Name: planned.Name, ColumnName: planned.Name, Type: planned.Type,
			Required: planned.Required, Unique: planned.Unique, Indexed: planned.Indexed,
			Default: append([]byte(nil), planned.Default...), Relation: relation, Ordinal: planned.Ordinal,
		})
	}
	return fields, nil
}

func resolvedPlannedRelation(fieldPath string, collectionID uuid.UUID, planned PlannedField) (*RelationSpec, error) {
	if planned.Type != TypeRelation {
		if planned.Relation != nil {
			return nil, ErrConflict
		}
		return nil, nil
	}
	if planned.Relation == nil {
		return nil, ErrConflict
	}
	if len(planned.Default) != 0 || !planned.Indexed ||
		planned.Required != (planned.Relation.MinSelect > 0) ||
		(planned.Unique && planned.Relation.Cardinality != RelationSingle) {
		return nil, ErrConflict
	}
	relation, err := ResolvePlannedRelation(fieldPath, *planned.Relation, collectionID)
	if err != nil {
		return nil, ErrConflict
	}
	if err := ValidateRelationSpec(fieldPath, *relation); err != nil {
		return nil, ErrConflict
	}
	return relation, nil
}

func classifyDDLApplyError(err error) error {
	var postgresError *pgconn.PgError
	if (errors.As(err, &postgresError) && postgresError.Code == "23505") || database.Classify(err) == database.KindValidation {
		return ErrDataConstraint
	}
	return classifyApplyError(err)
}

func classifyApplyError(err error) error {
	if database.Classify(err) == database.KindConflict {
		return ErrConflict
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "42P07", "42710", "42704":
			return ErrConflict
		case "23502", "23514":
			return ErrDataConstraint
		}
	}
	return err
}
