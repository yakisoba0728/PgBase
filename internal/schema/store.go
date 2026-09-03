package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
)

type Store struct {
	db database.DBTX
}

var errCatalogIntegrity = errors.New("catalog integrity violation")

func NewStore(db database.DBTX) *Store { return &Store{db: db} }

func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	return s.schemaVersion(ctx, "")
}

// PinnedSchemaVersion takes a shared row lock for the caller transaction.
// Schema writers take the matching exclusive lock before DDL, so the catalog
// metadata and physical collection tables cannot change underneath a record
// operation. Under repeatable-read, a reader that waited behind a committed
// writer receives PostgreSQL's serialization failure and retries with a fresh
// snapshot instead of using a stale catalog.
func (s *Store) PinnedSchemaVersion(ctx context.Context) (int64, error) {
	return s.schemaVersion(ctx, " FOR SHARE")
}

// LockedSchemaVersion takes the exclusive side of the catalog/record lock
// order and must be acquired before executing collection DDL.
func (s *Store) LockedSchemaVersion(ctx context.Context) (int64, error) {
	return s.schemaVersion(ctx, " FOR UPDATE")
}

func (s *Store) schemaVersion(ctx context.Context, lockClause string) (int64, error) {
	var version int64
	if err := s.db.QueryRow(ctx, `select version from pgbase_meta.schema_state where singleton`+lockClause).Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

func (s *Store) ReadCatalog(ctx context.Context, version int64) (Catalog, error) {
	rows, err := s.db.Query(ctx, `select id, name, table_name, created_at
		from pgbase_meta.collections order by name`)
	if err != nil {
		return Catalog{}, fmt.Errorf("read collections: %w", err)
	}
	collections := make(map[string]Collection)
	collectionNames := make(map[uuid.UUID]string)
	for rows.Next() {
		var collection Collection
		if err := rows.Scan(&collection.ID, &collection.Name, &collection.TableName, &collection.CreatedAt); err != nil {
			rows.Close()
			return Catalog{}, fmt.Errorf("scan collection: %w", err)
		}
		collection.CreatedAt = collection.CreatedAt.UTC()
		collection.Fields = []Field{}
		collections[collection.Name] = collection
		collectionNames[collection.ID] = collection.Name
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Catalog{}, fmt.Errorf("iterate collections: %w", err)
	}
	rows.Close()

	type fieldLocation struct {
		collectionID   uuid.UUID
		collectionName string
		fieldIndex     int
	}
	fieldLocations := make(map[uuid.UUID]fieldLocation)
	fieldRows, err := s.db.Query(ctx, `select id, collection_id, name, column_name, field_type,
		required, is_unique, is_indexed, has_default, default_json, ordinal
		from pgbase_meta.fields order by collection_id, ordinal`)
	if err != nil {
		return Catalog{}, fmt.Errorf("read collection fields: %w", err)
	}
	for fieldRows.Next() {
		var field Field
		var collectionID uuid.UUID
		var hasDefault bool
		var defaultJSON []byte
		if err := fieldRows.Scan(
			&field.ID, &collectionID, &field.Name, &field.ColumnName, &field.Type,
			&field.Required, &field.Unique, &field.Indexed, &hasDefault, &defaultJSON, &field.Ordinal,
		); err != nil {
			fieldRows.Close()
			return Catalog{}, fmt.Errorf("scan collection field: %w", err)
		}
		if hasDefault {
			field.Default = append(json.RawMessage(nil), defaultJSON...)
		}
		name, ok := collectionNames[collectionID]
		if !ok {
			fieldRows.Close()
			return Catalog{}, fmt.Errorf("collection field references unknown collection")
		}
		collection := collections[name]
		if _, exists := fieldLocations[field.ID]; exists {
			fieldRows.Close()
			return Catalog{}, errCatalogIntegrity
		}
		fieldLocations[field.ID] = fieldLocation{
			collectionID:   collectionID,
			collectionName: name,
			fieldIndex:     len(collection.Fields),
		}
		collection.Fields = append(collection.Fields, field)
		collections[name] = collection
	}
	if err := fieldRows.Err(); err != nil {
		fieldRows.Close()
		return Catalog{}, fmt.Errorf("iterate collection fields: %w", err)
	}
	fieldRows.Close()

	incoming := make(map[uuid.UUID][]RelationRef)
	relationRows, err := s.db.Query(ctx, `select field_id, source_collection_id,
		target_collection_id, cardinality, min_select, max_select, on_delete,
		value_codec_version, storage_kind, storage_name
		from pgbase_meta.field_relations`)
	if err != nil {
		return Catalog{}, fmt.Errorf("read relation metadata: %w", err)
	}
	for relationRows.Next() {
		var fieldID, sourceCollectionID uuid.UUID
		var spec RelationSpec
		if err := relationRows.Scan(
			&fieldID,
			&sourceCollectionID,
			&spec.TargetCollectionID,
			&spec.Cardinality,
			&spec.MinSelect,
			&spec.MaxSelect,
			&spec.OnDelete,
			&spec.ValueCodecVersion,
			&spec.Storage.Kind,
			&spec.Storage.Name,
		); err != nil {
			relationRows.Close()
			return Catalog{}, errCatalogIntegrity
		}

		location, ok := fieldLocations[fieldID]
		if !ok || location.collectionID != sourceCollectionID {
			relationRows.Close()
			return Catalog{}, errCatalogIntegrity
		}
		if _, ok := collectionNames[sourceCollectionID]; !ok {
			relationRows.Close()
			return Catalog{}, errCatalogIntegrity
		}
		if _, ok := collectionNames[spec.TargetCollectionID]; !ok {
			relationRows.Close()
			return Catalog{}, errCatalogIntegrity
		}

		collection := collections[location.collectionName]
		field := collection.Fields[location.fieldIndex]
		if field.Type != TypeRelation || field.Relation != nil || !relationFieldMatchesSpec(field, spec) {
			relationRows.Close()
			return Catalog{}, errCatalogIntegrity
		}
		if err := ValidateRelationSpec("field", spec); err != nil {
			relationRows.Close()
			return Catalog{}, fmt.Errorf("%w: %w", errCatalogIntegrity, err)
		}

		field.Relation = clonePointer(&spec)
		collection.Fields[location.fieldIndex] = field
		collections[location.collectionName] = collection
		incoming[spec.TargetCollectionID] = append(incoming[spec.TargetCollectionID], RelationRef{
			SourceCollectionID: sourceCollectionID,
			SourceFieldID:      fieldID,
			Spec:               spec,
		})
	}
	if err := relationRows.Err(); err != nil {
		relationRows.Close()
		return Catalog{}, fmt.Errorf("iterate relation metadata: %w", err)
	}
	relationRows.Close()

	for name, collection := range collections {
		for _, field := range collection.Fields {
			if (field.Type == TypeRelation) != (field.Relation != nil) {
				return Catalog{}, errCatalogIntegrity
			}
		}
		collections[name] = collection
	}
	for targetCollectionID := range incoming {
		sort.Slice(incoming[targetCollectionID], func(i, j int) bool {
			left := incoming[targetCollectionID][i]
			right := incoming[targetCollectionID][j]
			if left.SourceCollectionID != right.SourceCollectionID {
				return left.SourceCollectionID.String() < right.SourceCollectionID.String()
			}
			return left.SourceFieldID.String() < right.SourceFieldID.String()
		})
	}
	return Catalog{Version: version, Collections: collections, Incoming: incoming}, nil
}

func (s *Store) InsertCollection(ctx context.Context, collection Collection, definition PlannedCollection, createdAt time.Time) error {
	if _, err := s.db.Exec(ctx, `insert into pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at)
		values ($1, $2, $3, 'active', $4, $4)`,
		collection.ID, collection.Name, collection.TableName, createdAt); err != nil {
		return fmt.Errorf("insert collection metadata: %w", err)
	}
	if err := s.insertFieldMetadata(ctx, collection, definition, createdAt); err != nil {
		return err
	}
	return s.insertMembershipMetadata(ctx, collection.ID, createdAt)
}

func (s *Store) UpdateCollection(ctx context.Context, collection Collection, definition PlannedCollection, updatedAt time.Time) error {
	if _, err := s.db.Exec(ctx, `update pgbase_meta.collections
		set updated_at = $2
		where id = $1`,
		collection.ID, updatedAt); err != nil {
		return fmt.Errorf("update collection metadata: %w", err)
	}
	if _, err := s.db.Exec(ctx, `delete from pgbase_meta.fields where collection_id = $1`, collection.ID); err != nil {
		return fmt.Errorf("clear field metadata: %w", err)
	}
	return s.insertFieldMetadata(ctx, collection, definition, updatedAt)
}

func (s *Store) DeleteCollection(ctx context.Context, collectionID uuid.UUID) error {
	if _, err := s.db.Exec(ctx, `delete from pgbase_meta.collections where id = $1`, collectionID); err != nil {
		return fmt.Errorf("delete collection metadata: %w", err)
	}
	return nil
}

func (s *Store) insertMembershipMetadata(ctx context.Context, collectionID uuid.UUID, createdAt time.Time) error {
	var capabilityTable, counterTable, backfillTable bool
	if err := s.db.QueryRow(ctx, `select
		to_regclass('pgbase_meta.record_membership_capability') is not null,
		to_regclass('pgbase_meta.collection_creation_counters') is not null,
		to_regclass('pgbase_meta.record_membership_backfills') is not null`).Scan(
		&capabilityTable, &counterTable, &backfillTable,
	); err != nil {
		return fmt.Errorf("inspect record membership metadata: %w", err)
	}
	if !capabilityTable && !counterTable && !backfillTable {
		return nil
	}
	if !capabilityTable || !counterTable || !backfillTable {
		return errCatalogIntegrity
	}
	var state string
	var generation int64
	if err := s.db.QueryRow(ctx, `select state, generation
		from pgbase_meta.record_membership_capability where singleton for share`).Scan(&state, &generation); err != nil {
		return fmt.Errorf("read record membership capability: %w", err)
	}
	backfillState := "off"
	var cutoverCount, validatedCount, completedAt any
	switch state {
	case "off":
		if generation != 0 {
			return errCatalogIntegrity
		}
	case "dual_write", "ready":
		if generation < 1 {
			return errCatalogIntegrity
		}
		backfillState = "ready"
		cutoverCount = int64(0)
		validatedCount = int64(0)
		completedAt = createdAt
	default:
		return errCatalogIntegrity
	}
	if _, err := s.db.Exec(ctx, `insert into pgbase_meta.collection_creation_counters
		(collection_id, generation, last_ordinal, updated_at) values ($1, $2, 0, $3)`,
		collectionID, generation, createdAt); err != nil {
		return fmt.Errorf("insert record creation counter: %w", err)
	}
	if _, err := s.db.Exec(ctx, `insert into pgbase_meta.record_membership_backfills
		(collection_id, generation, state, cutover_count,
		 cutover_last_created_at, cutover_last_record_id,
		 checkpoint_created_at, checkpoint_record_id, last_assigned_ordinal,
		 validated_base_count, validated_sidecar_count, updated_at, completed_at)
		values ($1, $2, $3, $4, null, null, null, null, 0, $5, $5, $6, $7)`,
		collectionID, generation, backfillState, cutoverCount, validatedCount, createdAt, completedAt); err != nil {
		return fmt.Errorf("insert record membership backfill: %w", err)
	}
	return nil
}

func (s *Store) insertFieldMetadata(ctx context.Context, collection Collection, definition PlannedCollection, createdAt time.Time) error {
	if len(collection.Fields) != len(definition.Fields) {
		return fmt.Errorf("insert field metadata: invalid definition")
	}
	for index, plannedField := range definition.Fields {
		field := collection.Fields[index]
		if field.Name != plannedField.Name || field.Type != plannedField.Type || field.Ordinal != plannedField.Ordinal {
			return fmt.Errorf("insert field metadata: invalid definition")
		}
		var defaultJSON any
		if len(plannedField.Default) != 0 {
			defaultJSON = string(plannedField.Default)
		}
		if _, err := s.db.Exec(ctx, `insert into pgbase_meta.fields
			(id, collection_id, name, column_name, field_type, required, is_unique, is_indexed,
			 has_default, default_json, ordinal, created_at)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11, $12)`,
			field.ID, collection.ID, field.Name, field.ColumnName, field.Type,
			field.Required, field.Unique, field.Indexed, len(plannedField.Default) != 0,
			defaultJSON, field.Ordinal, createdAt); err != nil {
			return fmt.Errorf("insert field metadata: %w", err)
		}
	}

	for index, plannedField := range definition.Fields {
		field := collection.Fields[index]
		if field.Type == TypeRelation {
			if plannedField.Relation == nil || len(plannedField.Default) != 0 || len(field.Default) != 0 {
				return fmt.Errorf("insert relation metadata: invalid definition")
			}
			spec, err := ResolvePlannedRelation("field", *plannedField.Relation, collection.ID)
			if err != nil {
				return fmt.Errorf("insert relation metadata: %w", err)
			}
			if !relationFieldMatchesSpec(field, *spec) {
				return fmt.Errorf("insert relation metadata: invalid definition")
			}
			if err := ValidateRelationSpec("field", *spec); err != nil {
				return fmt.Errorf("insert relation metadata: %w", err)
			}
			if _, err := s.db.Exec(ctx, `insert into pgbase_meta.field_relations
				(field_id, source_collection_id, target_collection_id, cardinality,
				 min_select, max_select, on_delete, value_codec_version, storage_kind,
				 storage_name, created_at)
				values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
				field.ID,
				collection.ID,
				spec.TargetCollectionID,
				spec.Cardinality,
				spec.MinSelect,
				spec.MaxSelect,
				spec.OnDelete,
				spec.ValueCodecVersion,
				spec.Storage.Kind,
				spec.Storage.Name,
				createdAt,
			); err != nil {
				return fmt.Errorf("insert relation metadata: %w", err)
			}
			continue
		}
		if plannedField.Relation != nil {
			return fmt.Errorf("insert field metadata: invalid definition")
		}
		if !field.Unique && !field.Indexed {
			continue
		}
		indexID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate collection index ID: %w", err)
		}
		if _, err := s.db.Exec(ctx, `insert into pgbase_meta.collection_indexes
			(id, collection_id, field_id, index_name, is_unique, created_at)
			values ($1, $2, $3, $4, $5, $6)`,
			indexID, collection.ID, field.ID, indexName(collection.Name, field.Name), field.Unique, createdAt); err != nil {
			return fmt.Errorf("insert collection index metadata: %w", err)
		}
	}
	return nil
}

func relationFieldMatchesSpec(field Field, spec RelationSpec) bool {
	if field.Required != (spec.MinSelect > 0) || len(field.Default) != 0 || !field.Indexed {
		return false
	}
	return spec.Cardinality != RelationMultiple || !field.Unique
}

func (s *Store) InsertMigration(ctx context.Context, version int64, operationID uuid.UUID, plan Plan, checksum []byte, administratorID uuid.UUID, appliedAt time.Time, executionMS int64) error {
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("marshal schema migration plan: %w", err)
	}
	if _, err := s.db.Exec(ctx, `insert into pgbase_meta.schema_migrations
		(version, operation_id, operation, checksum, plan_json, sql_statements, applied_by, applied_at, execution_ms)
		values ($1, $2, $3, $4, $5::jsonb, $6, $7, $8, $9)`,
		version, operationID, plan.Operation, checksum, string(planJSON), plan.Statements,
		administratorID, appliedAt, executionMS); err != nil {
		return fmt.Errorf("insert schema migration audit: %w", err)
	}
	return nil
}

func (s *Store) IncrementVersion(ctx context.Context) (int64, error) {
	var version int64
	if err := s.db.QueryRow(ctx, `update pgbase_meta.schema_state
		set version = version + 1, updated_at = current_timestamp
		where singleton returning version`).Scan(&version); err != nil {
		return 0, fmt.Errorf("increment schema version: %w", err)
	}
	return version, nil
}
