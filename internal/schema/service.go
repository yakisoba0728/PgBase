package schema

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/observability"
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
	recorder observability.Recorder

	operationCursors    *schemaOperationCursorCodec
	now                 func() time.Time
	commitTx            func(context.Context, pgx.Tx) error
	maintenanceRetryMin time.Duration
	maintenanceRetryMax time.Duration

	beforeDDL        func(context.Context, pgx.Tx) error
	catalogWireLimit int

	// The listener heartbeat and Apply fence share one immutable writer identity.
	instanceOnce        sync.Once
	instanceID          uuid.UUID
	instanceErr         error
	compilerVersion     int16
	collectionLifecycle CollectionMutationLifecycle
	migrationRecorder   MigrationRecorder
	authSecret          []byte
}

func NewService(pool *database.Pool, cache *Cache, recorder observability.Recorder, cursorSecret ...[]byte) *Service {
	if cache == nil {
		cache = &Cache{}
	}
	var operationCursorSecret []byte
	if len(cursorSecret) != 0 {
		operationCursorSecret = cursorSecret[0]
	}
	service := &Service{
		pool: pool, cache: cache, recorder: recorder,
		operationCursors:    newSchemaOperationCursorCodec(operationCursorSecret),
		now:                 time.Now,
		commitTx:            func(ctx context.Context, tx pgx.Tx) error { return tx.Commit(ctx) },
		maintenanceRetryMin: schemaOperationMaintenanceRetryMin,
		maintenanceRetryMax: schemaOperationMaintenanceRetryMax,
		catalogWireLimit:    maximumCatalogWireBytes,
		compilerVersion:     schemaCompilerVersion,
	}
	if len(cursorSecret) != 0 {
		service.authSecret = append([]byte(nil), cursorSecret[0]...)
	}
	return service
}

func (s *Service) Preview(ctx context.Context, change Change) (plan Plan, err error) {
	operationStarted := time.Now()
	relationOperation := changeHasRelationInput(change)
	defer s.finishOperation(ctx, observability.OperationRelationSchema, operationStarted, &err, &relationOperation)
	var catalog Catalog
	var companions bool
	if isSQLViewChange(change) {
		err = database.Retry(ctx, func() error {
			return s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
				var inner error
				catalog, inner = s.EnsureCurrent(ctx, tx)
				if inner != nil {
					return inner
				}
				companions, inner = companionCapabilityReady(ctx, tx)
				if inner != nil {
					return inner
				}
				change, inner = inspectSQLViewChange(ctx, tx, catalog, change)
				return inner
			})
		}, func(cause database.RetryCause, exhausted bool) {
			s.record(ctx, observability.TransactionRetryEvent(cause, exhausted))
		})
		if err != nil {
			return Plan{}, err
		}
	} else {
		catalog, err = s.Catalog(ctx)
		if err != nil {
			return Plan{}, err
		}
		companions, err = companionCapabilityReady(ctx, s.pool)
		if err != nil {
			return Plan{}, err
		}
	}
	// A preview must show the plan this binary would really apply, so it reads the
	// capability row D2-2 makes authoritative instead of assuming a format. Apply accepts
	// both generations, so a capability that moves in between costs the caller nothing.
	plan, err = compilePlan(catalog, change, companions)
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

func isSQLViewChange(change Change) bool {
	switch change.kind {
	case changeCreate:
		return change.create.View != nil && change.create.View.SQL != ""
	case changeUpdate:
		return change.update.View != nil && change.update.View.SQL != ""
	default:
		return false
	}
}

func inspectSQLViewChange(ctx context.Context, tx pgx.Tx, catalog Catalog, change Change) (Change, error) {
	if !isSQLViewChange(change) {
		return change, nil
	}
	view := change.create.View
	if change.kind == changeUpdate {
		view = change.update.View
	}
	inspection, err := InspectSQLView(ctx, tx, catalog, view.SQL)
	if err != nil {
		return Change{}, err
	}
	var prepared Change
	if change.kind == changeCreate {
		prepared = NewCreateChange(change.create)
		prepared.create.View.inspection = cloneSQLViewInspection(&inspection)
	} else {
		prepared = NewUpdateChange(change.update)
		prepared.update.View.inspection = cloneSQLViewInspection(&inspection)
	}
	return prepared, nil
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
		if _, exists := existing[field.Name]; !exists && field.Type == TypeRelation && field.Required {
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

func (s *Service) compileCatalog(catalog Catalog) (*compiledCatalog, error) {
	return compileCatalogWithLimit(catalog, s.catalogLimit())
}

func (s *Service) catalogLimit() int {
	if s.catalogWireLimit > 0 {
		return s.catalogWireLimit
	}
	return maximumCatalogWireBytes
}

func (s *Service) finishOperation(ctx context.Context, operation observability.Operation, started time.Time, operationErr *error, enabled ...*bool) {
	if len(enabled) != 0 && (enabled[0] == nil || !*enabled[0]) {
		return
	}
	if recovered := recover(); recovered != nil {
		s.record(ctx, observability.OperationEvent(operation, observability.OutcomeFailure, time.Since(started)))
		panic(recovered)
	}
	var err error
	if operationErr != nil {
		err = *operationErr
	}
	s.record(ctx, observability.OperationEvent(operation, schemaOperationOutcome(err), time.Since(started)))
}

func inputHasRelationField(fields []FieldInput) bool {
	return slices.ContainsFunc(fields, func(field FieldInput) bool {
		return field.Type == TypeRelation || field.Relation != nil
	})
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
	return slices.ContainsFunc(collection.Fields, func(field Field) bool {
		return field.Type == TypeRelation || field.Relation != nil
	})
}

func validateCreateAgainstCatalog(catalog Catalog, definition PlannedCollection) error {
	for _, collection := range catalog.Collections {
		if collection.Name == definition.Name || collection.TableName == definition.TableName {
			return ErrConflict
		}
		if definition.CompatibilityName != "" && (strings.EqualFold(collection.Name, definition.CompatibilityName) || strings.EqualFold(collection.CompatibilityName, definition.CompatibilityName)) {
			return validation("compatibilityName", "duplicate_name")
		}
	}
	return nil
}

func sameCatalogContent(left, right Catalog) bool {
	left, right = cloneCatalog(left), cloneCatalog(right)
	for _, catalog := range []*Catalog{&left, &right} {
		catalog.wire = nil
		for name, collection := range catalog.Collections {
			collection.CreatedAt = collection.CreatedAt.UTC()
			catalog.Collections[name] = collection
		}
	}
	return reflect.DeepEqual(left, right)
}

func validateCatalogIntegrity(catalog Catalog) error {
	collectionIDs := make(map[uuid.UUID]bool, len(catalog.Collections))
	compatibilityNames := map[string]bool{}
	for key, collection := range catalog.Collections {
		if key != collection.Name || collection.Name != collection.TableName || collection.ID == uuid.Nil || collectionIDs[collection.ID] {
			return ErrConflict
		}
		if err := validateName("", collection.Name); err != nil {
			return ErrConflict
		}
		if collection.Type != "" && collection.Type != CollectionTypeBase && collection.Type != CollectionTypeAuth && collection.Type != CollectionTypeView {
			return ErrConflict
		}
		if collection.ValidationMode != "" && collection.ValidationMode != ValidationModePostgres && collection.ValidationMode != ValidationModePocketBase {
			return ErrConflict
		}
		if collection.CompatibilityName != "" {
			normalized := strings.ToLower(collection.CompatibilityName)
			if !compatibilityCollectionName.MatchString(collection.CompatibilityName) || compatibilityNames[normalized] {
				return ErrConflict
			}
			compatibilityNames[normalized] = true
		}
		collectionIDs[collection.ID] = true
	}
	expectedIncoming := make(map[RelationRef]int)
	fieldIDs := make(map[uuid.UUID]bool)
	for _, collection := range catalog.Collections {
		fieldNames := make(map[string]bool, len(collection.Fields))
		for ordinal, field := range collection.Fields {
			if field.ID == uuid.Nil || field.Ordinal != ordinal || !isSupportedType(field.Type) ||
				fieldIDs[field.ID] || fieldNames[field.Name] {
				return ErrConflict
			}
			fieldIDs[field.ID] = true
			fieldNames[field.Name] = true
			if err := validateName("", field.Name); err != nil {
				return ErrConflict
			}
			if err := validateName("", field.ColumnName); err != nil {
				return ErrConflict
			}
			if len([]rune(field.Help)) > 300 || (field.Hidden && field.Presentable) {
				return ErrConflict
			}
			if field.Type == TypeFile {
				options := cloneFieldOptions(field.Options)
				if validateFieldOptions(TypeFile, &options) != nil || field.Unique || field.Indexed || len(field.Default) != 0 || field.Relation != nil {
					return ErrConflict
				}
				continue
			}
			options := cloneFieldOptions(field.Options)
			if err := validateFieldOptions(field.Type, &options); err != nil {
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
				expectedStorageName = NewRelationPhysicalNames(collection.TableName, field.ColumnName).MultiTable
			}
			if field.Relation.Storage.Name != expectedStorageName {
				return ErrConflict
			}
			if !collectionIDs[field.Relation.TargetCollectionID] {
				return ErrConflict
			}
			expectedIncoming[RelationRef{
				SourceCollectionID: collection.ID,
				SourceFieldID:      field.ID,
				Spec:               *field.Relation,
			}]++
		}
		indexNames := map[string]bool{}
		for _, index := range collection.Indexes {
			if index.ID == uuid.Nil || indexNames[index.Name] || index.IndexName != compoundIndexName(collection.Name, index.Name) || len(index.Fields) < 2 || len(index.Fields) > 8 {
				return ErrConflict
			}
			if err := validateName("", index.Name); err != nil {
				return ErrConflict
			}
			indexNames[index.Name] = true
			seen := map[string]bool{}
			for _, name := range index.Fields {
				if seen[name] || !fieldNames[name] {
					return ErrConflict
				}
				seen[name] = true
				for _, field := range collection.Fields {
					if field.Name == name && (field.Type == TypeFile || field.Type == TypeRelation || field.Type == TypeJSONB || field.Type == TypeGeoPoint || field.Type == TypePassword) {
						return ErrConflict
					}
				}
			}
		}
		if collection.View != nil {
			if len(collection.Indexes) > 0 || len(collection.Fields) > 128 {
				return ErrConflict
			}
			if collection.IsSQLView() {
				if collection.View.Source != "" || collection.View.SourceCollectionID != uuid.Nil || len(collection.View.Projection) != 0 || collection.View.Filter != nil || len(collection.View.SQL) > maximumSQLViewBytes {
					return ErrConflict
				}
				seenDependencies := map[uuid.UUID]bool{}
				for _, field := range collection.Fields {
					if field.Required || field.Unique || field.Indexed || len(field.Default) != 0 || field.Relation != nil || !reflect.DeepEqual(field.Options, FieldOptions{}) {
						return ErrConflict
					}
				}
				for _, dependency := range collection.View.Dependencies {
					if dependency == collection.ID || !collectionIDs[dependency] || seenDependencies[dependency] {
						return ErrConflict
					}
					seenDependencies[dependency] = true
				}
				continue
			}
			if len(collection.View.Dependencies) != 0 {
				return ErrConflict
			}
			source, ok := catalog.Collections[collection.View.Source]
			if !ok || source.ID != collection.View.SourceCollectionID || source.IsView() || len(collection.View.Projection) != len(collection.Fields) {
				return ErrConflict
			}
			for position, name := range collection.View.Projection {
				if collection.Fields[position].Name != name {
					return ErrConflict
				}
				found := false
				for _, field := range source.Fields {
					if field.Name == name && field.Type == collection.Fields[position].Type && field.Type != TypeFile && field.Type != TypeRelation && field.Type != TypePassword {
						found = true
					}
				}
				if !found {
					return ErrConflict
				}
			}
			if collection.View.Filter != nil {
				if _, err := compileStaticViewFilter(*collection.View.Filter, source, "source", 0); err != nil {
					return ErrConflict
				}
			}
		}
	}
	for targetCollectionID, incoming := range catalog.Incoming {
		if !collectionIDs[targetCollectionID] || len(incoming) == 0 {
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
	return databaseOperationOutcome(err)
}

func newCollectionMetadata(definition PlannedCollection) (Collection, error) {
	collectionID, err := uuid.NewV7()
	if err != nil {
		return Collection{}, fmt.Errorf("generate collection ID: %w", err)
	}
	return newCollectionMetadataWithID(collectionID, definition)
}

func updatedFieldMetadata(collectionID uuid.UUID, existing []Field, definition PlannedCollection) ([]Field, error) {
	existingIDs := make(map[string]uuid.UUID, len(existing))
	existingByID := make(map[uuid.UUID]Field, len(existing))
	for _, field := range existing {
		existingIDs[field.Name] = field.ID
		existingByID[field.ID] = field
	}
	fields := make([]Field, 0, len(definition.Fields))
	usedIDs := make(map[uuid.UUID]bool, len(definition.Fields))
	for ordinal, planned := range definition.Fields {
		fieldID, ok := existingIDs[planned.Name]
		if ok && usedIDs[fieldID] {
			fieldID, ok = uuid.Nil, false
		}
		columnName := planned.ColumnName
		if planned.ID != uuid.Nil {
			prior, exists := existingByID[planned.ID]
			if !exists && len(existing) > 0 {
				return nil, ErrConflict
			}
			if exists {
				fieldID, ok, columnName = prior.ID, true, prior.ColumnName
			}
		}
		if !ok {
			var err error
			fieldID, err = uuid.NewV7()
			if err != nil {
				return nil, fmt.Errorf("generate field ID: %w", err)
			}
		}
		usedIDs[fieldID] = true
		relation, err := resolvedPlannedRelation(fmt.Sprintf("fields.%d", ordinal), collectionID, planned)
		if err != nil {
			return nil, err
		}
		fields = append(fields, Field{
			ID: fieldID, Name: planned.Name, ColumnName: columnName, Type: planned.Type,
			Required: planned.Required, Unique: planned.Unique, Indexed: planned.Indexed,
			Default: append([]byte(nil), planned.Default...), Relation: relation, Options: cloneFieldOptions(planned.Options), Hidden: planned.Hidden, Presentable: planned.Presentable, Help: planned.Help, System: planned.System, Ordinal: planned.Ordinal,
		})
	}
	return fields, nil
}

func updatedIndexMetadata(collection string, existing []Index, definition PlannedCollection) ([]Index, error) {
	ids := map[string]uuid.UUID{}
	for _, index := range existing {
		ids[index.Name] = index.ID
	}
	result := make([]Index, 0, len(definition.Indexes))
	for _, planned := range definition.Indexes {
		id := ids[planned.Name]
		if id == uuid.Nil {
			var err error
			id, err = uuid.NewV7()
			if err != nil {
				return nil, err
			}
		}
		result = append(result, Index{ID: id, Name: planned.Name, Fields: append([]string(nil), planned.Fields...), IndexName: compoundIndexName(collection, planned.Name), Unique: planned.Unique})
	}
	return result, nil
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
