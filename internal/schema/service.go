package schema

import (
	"bytes"
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
	"github.com/webisnoteasy/PgBase/internal/observability"
)

const schemaAdvisoryLockKey int64 = 0x7067626173650002

var ErrConflict = errors.New("schema plan conflicts with current state")

var ErrCollectionNotFound = errors.New("collection does not exist")

// ErrDataConstraint reports an apply whose DDL failed because committed rows
// violate the requested change (for example SET NOT NULL over NULL values).
var ErrDataConstraint = errors.New("existing data violates the schema change")

type Service struct {
	pool     *database.Pool
	cache    *Cache
	planner  Planner
	recorder observability.Recorder

	beforeDDL        func(context.Context, pgx.Tx) error
	catalogWireLimit int
}

func NewService(pool *database.Pool, cache *Cache, planner Planner) *Service {
	return NewServiceWithRecorder(pool, cache, planner, nil)
}

func NewServiceWithRecorder(pool *database.Pool, cache *Cache, planner Planner, recorder observability.Recorder) *Service {
	if cache == nil {
		cache = &Cache{}
	}
	return &Service{
		pool: pool, cache: cache, planner: planner, recorder: recorder,
		catalogWireLimit: maximumCatalogWireBytes,
	}
}

func (s *Service) Preview(ctx context.Context, change Change) (plan Plan, err error) {
	operationStarted := time.Now()
	relationOperation := changeHasRelationInput(change)
	defer s.finishRelationSchemaOperation(ctx, operationStarted, &err, &relationOperation)
	catalog, err := s.Catalog(ctx)
	if err != nil {
		return Plan{}, err
	}
	plan, err = s.planner.Plan(catalog, change)
	if err != nil {
		return Plan{}, err
	}
	switch change.kind {
	case changeUpdate:
		current := catalog.Collections[change.update.Name]
		relationOperation = relationOperation || collectionHasRelationField(current) || len(catalog.Incoming[current.ID]) != 0
		if err := validateRequiredRelationAdds(ctx, s.pool, current, plan.Definition, false); err != nil {
			return Plan{}, err
		}
	case changeDelete:
		current := catalog.Collections[change.deleteName]
		relationOperation = collectionHasRelationField(current) || len(catalog.Incoming[current.ID]) != 0
	}
	projected, err := projectCatalog(catalog, change, plan)
	if err != nil {
		return Plan{}, err
	}
	if _, err := s.compileCatalog(projected); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func (s *Service) PlanCreate(ctx context.Context, input CreateCollectionInput) (Plan, error) {
	return s.Preview(ctx, NewCreateChange(input))
}

func (s *Service) PlanUpdate(ctx context.Context, input UpdateCollectionInput) (Plan, error) {
	return s.Preview(ctx, NewUpdateChange(input))
}

func (s *Service) PlanDelete(ctx context.Context, name string) (plan Plan, err error) {
	return s.Preview(ctx, NewDeleteChange(name))
}

func (s *Service) ApplyCreate(ctx context.Context, input CreateCollectionInput, checksum string, baseVersion int64, administratorID uuid.UUID) (created Collection, err error) {
	result, err := s.Apply(ctx, LegacyApply(
		NewCreateChange(input),
		Plan{Checksum: checksum, BaseVersion: baseVersion},
		administratorID,
	))
	if err != nil {
		return Collection{}, err
	}
	return result.Collection, nil
}

func (s *Service) ApplyUpdate(ctx context.Context, input UpdateCollectionInput, checksum string, baseVersion int64, administratorID uuid.UUID) (updated Collection, err error) {
	result, err := s.Apply(ctx, LegacyApply(
		NewUpdateChange(input),
		Plan{Checksum: checksum, BaseVersion: baseVersion},
		administratorID,
	))
	if err != nil {
		return Collection{}, err
	}
	return result.Collection, nil
}

func (s *Service) ApplyDelete(ctx context.Context, name string, checksum string, baseVersion int64, administratorID uuid.UUID) error {
	_, err := s.Apply(ctx, LegacyApply(
		NewDeleteChange(name),
		Plan{Checksum: checksum, BaseVersion: baseVersion},
		administratorID,
	))
	return err
}

func (s *Service) Apply(ctx context.Context, intent ApplyIntent) (result ApplyResult, err error) {
	operation, ok := schemaOperationForChange(intent.change)
	if !ok {
		return ApplyResult{}, ErrConflict
	}
	operationStarted := time.Now()
	relationOperation := changeHasRelationInput(intent.change)
	defer s.finishOperation(ctx, operation, operationStarted, &err)
	defer s.finishRelationSchemaOperation(ctx, operationStarted, &err, &relationOperation)
	if err = s.ready(); err != nil {
		return ApplyResult{}, err
	}
	var nextCatalog *compiledCatalog
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
		if version != intent.baseVersion {
			return ErrConflict
		}
		persisted, err := store.ReadCatalog(ctx, version)
		if err != nil {
			return err
		}
		currentCatalog := s.cache.find(version)
		if currentCatalog == nil {
			currentCatalog, err = s.cache.prepare(persisted, s.catalogLimit())
			if err != nil {
				return err
			}
			s.cache.publish(currentCatalog)
		} else if !sameCatalogContent(persisted, currentCatalog.catalog) {
			return ErrConflict
		}
		catalog := currentCatalog.catalog
		plan, err := s.planner.Plan(catalog, intent.change)
		if err != nil {
			return err
		}
		if plan.Checksum != intent.checksum {
			return ErrConflict
		}

		var current Collection
		switch intent.change.kind {
		case changeCreate:
			if err := validateCreateAgainstCatalog(catalog, plan.Definition); err != nil {
				return err
			}
			created, err := newCollectionMetadata(plan.Definition)
			if err != nil {
				return err
			}
			result.Collection = created
		case changeUpdate:
			current = catalog.Collections[intent.change.update.Name]
			relationOperation = relationOperation || collectionHasRelationField(current) || len(catalog.Incoming[current.ID]) != 0
			if err := validateRequiredRelationAdds(ctx, tx, current, plan.Definition, true); err != nil {
				return err
			}
			fields, err := updatedFieldMetadata(current.ID, current.Fields, plan.Definition)
			if err != nil {
				return err
			}
			result.Collection = Collection{
				ID: current.ID, Name: plan.Definition.Name, TableName: plan.Definition.TableName,
				Fields: fields, CreatedAt: current.CreatedAt,
			}
		case changeDelete:
			current = catalog.Collections[intent.change.deleteName]
			relationOperation = collectionHasRelationField(current) || len(catalog.Incoming[current.ID]) != 0
			result.Deleted = true
		}

		var appliedAt time.Time
		if err := tx.QueryRow(ctx, "select current_timestamp").Scan(&appliedAt); err != nil {
			return fmt.Errorf("read schema application timestamp: %w", err)
		}
		appliedAt = appliedAt.UTC()
		if intent.change.kind == changeCreate {
			result.Collection.CreatedAt = appliedAt
		}
		started := time.Now()
		if err := s.executePlan(ctx, tx, plan); err != nil {
			return err
		}
		switch intent.change.kind {
		case changeCreate:
			err = store.InsertCollection(ctx, result.Collection, plan.Definition, appliedAt)
		case changeUpdate:
			err = store.UpdateCollection(ctx, result.Collection, plan.Definition, appliedAt)
		case changeDelete:
			err = store.DeleteCollection(ctx, current.ID)
		}
		if err != nil {
			return classifyApplyError(err)
		}
		return s.commitSchemaChange(ctx, tx, store, plan, intent.checksum, intent.actor, version, appliedAt, started, &nextCatalog)
	})
	if err != nil {
		s.cache.discardPrepared(nextCatalog)
		return ApplyResult{}, err
	}
	s.cache.publish(nextCatalog)
	return result, nil
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

func (s *Service) executePlan(ctx context.Context, tx pgx.Tx, plan Plan) error {
	if s.beforeDDL != nil {
		if err := s.beforeDDL(ctx, tx); err != nil {
			return classifyDDLApplyError(fmt.Errorf("execute collection DDL: %w", err))
		}
	}
	for _, statement := range plan.Statements {
		if _, err := tx.Exec(ctx, statement); err != nil {
			return classifyDDLApplyError(fmt.Errorf("execute collection DDL: %w", err))
		}
	}
	return nil
}

func (s *Service) commitSchemaChange(ctx context.Context, tx pgx.Tx, store *Store, plan Plan, checksum string, administratorID uuid.UUID, version int64, createdAt time.Time, started time.Time, nextCatalog **compiledCatalog) error {
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
	compiled, err := s.cache.prepare(published, s.catalogLimit())
	if err != nil {
		return fmt.Errorf("compile committed schema catalog: %w", err)
	}
	*nextCatalog = compiled
	return nil
}

func (s *Service) compileCatalog(catalog Catalog) (*compiledCatalog, error) {
	return compileCatalogWithLimit(catalog, s.catalogLimit())
}

func (s *Service) catalogLimit() int {
	if s.catalogWireLimit > 0 {
		return s.catalogWireLimit
	}
	return maximumCatalogWireBytes
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

func changeHasRelationInput(change Change) bool {
	switch change.kind {
	case changeCreate:
		return inputHasRelationField(change.create.Fields)
	case changeUpdate:
		return inputHasRelationField(change.update.Fields)
	default:
		return false
	}
}

func schemaOperationForChange(change Change) (observability.Operation, bool) {
	switch change.kind {
	case changeCreate:
		return observability.OperationSchemaCreate, true
	case changeUpdate:
		return observability.OperationSchemaUpdate, true
	case changeDelete:
		return observability.OperationSchemaDelete, true
	default:
		return "", false
	}
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
	for _, collection := range catalog.Collections {
		if collection.Name == definition.Name || collection.TableName == definition.TableName {
			return ErrConflict
		}
	}
	return nil
}

func sameCatalogContent(left, right Catalog) bool {
	if left.Version != right.Version || len(left.Collections) != len(right.Collections) || len(left.Incoming) != len(right.Incoming) {
		return false
	}
	for name, leftCollection := range left.Collections {
		rightCollection, ok := right.Collections[name]
		if !ok || leftCollection.ID != rightCollection.ID || leftCollection.Name != rightCollection.Name ||
			leftCollection.TableName != rightCollection.TableName || !leftCollection.CreatedAt.Equal(rightCollection.CreatedAt) ||
			len(leftCollection.Fields) != len(rightCollection.Fields) {
			return false
		}
		for index, leftField := range leftCollection.Fields {
			rightField := rightCollection.Fields[index]
			if leftField.ID != rightField.ID || leftField.Name != rightField.Name || leftField.ColumnName != rightField.ColumnName ||
				leftField.Type != rightField.Type || leftField.Required != rightField.Required || leftField.Unique != rightField.Unique ||
				leftField.Indexed != rightField.Indexed || leftField.Ordinal != rightField.Ordinal ||
				!bytes.Equal(leftField.Default, rightField.Default) || !sameRelationSpec(leftField.Relation, rightField.Relation) {
				return false
			}
		}
	}
	for targetID, leftIncoming := range left.Incoming {
		rightIncoming, ok := right.Incoming[targetID]
		if !ok || len(leftIncoming) != len(rightIncoming) {
			return false
		}
		for index := range leftIncoming {
			if leftIncoming[index] != rightIncoming[index] {
				return false
			}
		}
	}
	return true
}

func sameRelationSpec(left, right *RelationSpec) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
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
			expectedStorageName := field.ColumnName
			if field.Relation.Cardinality == RelationMultiple {
				expectedStorageName = NewRelationPhysicalNames(collection.TableName, field.Name).MultiTable
			}
			if field.Relation.Storage.Name != expectedStorageName {
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

// EnsureCurrent pins the transaction's schema version and returns its compiled
// catalog in O(1). The returned value is an immutable view of the cache;
// transaction-owning callers must not mutate its maps, slices, or nested data.
func (s *Service) EnsureCurrent(ctx context.Context, tx database.DBTX) (Catalog, error) {
	if s == nil || s.cache == nil || tx == nil {
		return Catalog{}, fmt.Errorf("schema catalog is unavailable")
	}
	store := NewStore(tx)
	version, err := store.PinnedSchemaVersion(ctx)
	if err != nil {
		return Catalog{}, err
	}
	current := s.cache.find(version)
	if current != nil {
		s.cache.publish(current)
		return current.catalog, nil
	}
	catalog, err := store.ReadCatalog(ctx, version)
	if err != nil {
		return Catalog{}, err
	}
	compiled, err := s.cache.prepare(catalog, s.catalogLimit())
	if err != nil {
		return Catalog{}, err
	}
	s.cache.publish(compiled)
	return compiled.catalog, nil
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
	err := database.Retry(ctx, func() error {
		return s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
			var err error
			catalog, err = s.EnsureCurrent(ctx, tx)
			return err
		})
	}, func(cause database.RetryCause, exhausted bool) {
		s.record(ctx, observability.TransactionRetryEvent(cause, exhausted))
	})
	return catalog, err
}

func (s *Service) ready() error {
	if s == nil || s.pool == nil || s.cache == nil {
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
	if errors.Is(err, ErrCatalogLimitExceeded) {
		return observability.OutcomeBudget
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
