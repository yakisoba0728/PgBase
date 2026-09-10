package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
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
	var hasViews bool
	if err := s.db.QueryRow(ctx, `select to_regclass('pgbase_meta.collection_views') is not null`).Scan(&hasViews); err != nil {
		return Catalog{}, err
	}
	if hasViews {
		var hasSQLViews bool
		if err := s.db.QueryRow(ctx, `select exists(select 1 from information_schema.columns where table_schema='pgbase_meta' and table_name='collection_views' and column_name='sql')`).Scan(&hasSQLViews); err != nil {
			return Catalog{}, err
		}
		query := `select collection_id,source_collection_id,projection,filter_json,null::text,null::uuid[] from pgbase_meta.collection_views`
		if hasSQLViews {
			query = `select collection_id,source_collection_id,projection,filter_json,sql,dependencies from pgbase_meta.collection_views`
		}
		viewRows, err := s.db.Query(ctx, query)
		if err != nil {
			return Catalog{}, fmt.Errorf("read collection views: %w", err)
		}
		for viewRows.Next() {
			var collectionID uuid.UUID
			var sourceID *uuid.UUID
			var projection []string
			var raw []byte
			var sql *string
			var dependencies []uuid.UUID
			if err := viewRows.Scan(&collectionID, &sourceID, &projection, &raw, &sql, &dependencies); err != nil {
				viewRows.Close()
				return Catalog{}, err
			}
			name, ok := collectionNames[collectionID]
			if !ok {
				viewRows.Close()
				return Catalog{}, errCatalogIntegrity
			}
			if sql != nil {
				collection := collections[name]
				collection.View = &ViewSpec{SQL: *sql, Dependencies: append([]uuid.UUID(nil), dependencies...)}
				collections[name] = collection
				continue
			}
			if sourceID == nil {
				viewRows.Close()
				return Catalog{}, errCatalogIntegrity
			}
			sourceName, sourceOK := collectionNames[*sourceID]
			if !sourceOK {
				viewRows.Close()
				return Catalog{}, errCatalogIntegrity
			}
			var filter *ViewFilter
			if len(raw) > 0 {
				var value ViewFilter
				if json.Unmarshal(raw, &value) != nil {
					viewRows.Close()
					return Catalog{}, errCatalogIntegrity
				}
				filter = &value
			}
			collection := collections[name]
			collection.View = &ViewSpec{SourceCollectionID: *sourceID, Source: sourceName, Projection: append([]string(nil), projection...), Filter: filter}
			collections[name] = collection
		}
		if err := viewRows.Err(); err != nil {
			viewRows.Close()
			return Catalog{}, err
		}
		viewRows.Close()
	}

	type fieldLocation struct {
		collectionID   uuid.UUID
		collectionName string
		fieldIndex     int
	}
	fieldLocations := make(map[uuid.UUID]fieldLocation)
	var hasFieldOptions, hasFieldPresentation bool
	if err := s.db.QueryRow(ctx, `select
		exists(select 1 from information_schema.columns where table_schema='pgbase_meta' and table_name='fields' and column_name='options_json'),
		exists(select 1 from information_schema.columns where table_schema='pgbase_meta' and table_name='fields' and column_name='hidden')`).Scan(&hasFieldOptions, &hasFieldPresentation); err != nil {
		return Catalog{}, fmt.Errorf("inspect field metadata capabilities: %w", err)
	}
	optionsExpression := "'{}'::jsonb"
	if hasFieldOptions {
		optionsExpression = "options_json"
	}
	presentationExpression := "false, false, ''::text"
	if hasFieldPresentation {
		presentationExpression = "hidden, presentable, help"
	}
	fieldRows, err := s.db.Query(ctx, `select id, collection_id, name, column_name, field_type,
		required, is_unique, is_indexed, has_default, default_json, ordinal, `+optionsExpression+`, `+presentationExpression+`
		from pgbase_meta.fields order by collection_id, ordinal`)
	if err != nil {
		return Catalog{}, fmt.Errorf("read collection fields: %w", err)
	}
	for fieldRows.Next() {
		var field Field
		var collectionID uuid.UUID
		var hasDefault bool
		var defaultJSON []byte
		var optionsJSON []byte
		if err := fieldRows.Scan(
			&field.ID, &collectionID, &field.Name, &field.ColumnName, &field.Type,
			&field.Required, &field.Unique, &field.Indexed, &hasDefault, &defaultJSON, &field.Ordinal, &optionsJSON, &field.Hidden, &field.Presentable, &field.Help,
		); err != nil {
			fieldRows.Close()
			return Catalog{}, fmt.Errorf("scan collection field: %w", err)
		}
		if hasDefault {
			field.Default = append(json.RawMessage(nil), defaultJSON...)
		}
		if err := json.Unmarshal(optionsJSON, &field.Options); err != nil {
			fieldRows.Close()
			return Catalog{}, errCatalogIntegrity
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
	}
	if err := relationRows.Err(); err != nil {
		relationRows.Close()
		return Catalog{}, fmt.Errorf("iterate relation metadata: %w", err)
	}
	relationRows.Close()
	var hasCompoundIndexes bool
	if err := s.db.QueryRow(ctx, `select to_regclass('pgbase_meta.compound_indexes') is not null and to_regclass('pgbase_meta.compound_index_fields') is not null`).Scan(&hasCompoundIndexes); err != nil {
		return Catalog{}, fmt.Errorf("inspect compound index metadata: %w", err)
	}
	if hasCompoundIndexes {
		indexRows, err := s.db.Query(ctx, `select i.id,i.collection_id,i.name,i.index_name,i.is_unique,f.name
		from pgbase_meta.compound_indexes i join pgbase_meta.compound_index_fields m on m.index_id=i.id
		join pgbase_meta.fields f on f.id=m.field_id order by i.collection_id,i.name,m.ordinal`)
		if err != nil {
			return Catalog{}, fmt.Errorf("read compound indexes: %w", err)
		}
		indexLocations := map[uuid.UUID]struct {
			collection string
			position   int
		}{}
		for indexRows.Next() {
			var id, collectionID uuid.UUID
			var name, physical, field string
			var unique bool
			if err := indexRows.Scan(&id, &collectionID, &name, &physical, &unique, &field); err != nil {
				indexRows.Close()
				return Catalog{}, err
			}
			collectionName, ok := collectionNames[collectionID]
			if !ok {
				indexRows.Close()
				return Catalog{}, errCatalogIntegrity
			}
			collection := collections[collectionName]
			location, ok := indexLocations[id]
			if !ok {
				collection.Indexes = append(collection.Indexes, Index{ID: id, Name: name, IndexName: physical, Unique: unique})
				location = struct {
					collection string
					position   int
				}{collectionName, len(collection.Indexes) - 1}
				indexLocations[id] = location
			}
			if location.collection != collectionName {
				indexRows.Close()
				return Catalog{}, errCatalogIntegrity
			}
			collection.Indexes[location.position].Fields = append(collection.Indexes[location.position].Fields, field)
			collections[collectionName] = collection
		}
		if err := indexRows.Err(); err != nil {
			indexRows.Close()
			return Catalog{}, err
		}
		indexRows.Close()
	}

	for _, collection := range collections {
		for _, field := range collection.Fields {
			if (field.Type == TypeRelation) != (field.Relation != nil) {
				return Catalog{}, errCatalogIntegrity
			}
		}
	}
	return Catalog{Version: version, Collections: collections, Incoming: buildIncomingIndex(collections)}, nil
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
	if collection.View != nil {
		encoded, err := encodeViewFilter(collection.View.Filter)
		if err != nil {
			return err
		}
		var source any = collection.View.SourceCollectionID
		var projection any = collection.View.Projection
		if collection.View.SQL != "" {
			source, projection = nil, nil
		}
		if _, err := s.db.Exec(ctx, `insert into pgbase_meta.collection_views(collection_id,source_collection_id,projection,filter_json,sql,dependencies,created_at) values($1,$2,$3,$4::jsonb,$5,$6,$7)`, collection.ID, source, projection, encoded, nullableString(collection.View.SQL), nullableUUIDs(collection.View.Dependencies), createdAt); err != nil {
			return fmt.Errorf("insert collection view metadata: %w", err)
		}
	}
	return s.insertMembershipMetadata(ctx, collection.ID, createdAt)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableUUIDs(values []uuid.UUID) any {
	if values == nil {
		return nil
	}
	return values
}

func (s *Store) UpdateCollection(ctx context.Context, collection Collection, definition PlannedCollection, updatedAt time.Time) error {
	if _, err := s.db.Exec(ctx, `update pgbase_meta.collections
		set updated_at = $2
		where id = $1`,
		collection.ID, updatedAt); err != nil {
		return fmt.Errorf("update collection metadata: %w", err)
	}
	if collection.IsSQLView() {
		if _, err := s.db.Exec(ctx, `update pgbase_meta.collection_views set sql=$2,dependencies=$3 where collection_id=$1`, collection.ID, collection.View.SQL, collection.View.Dependencies); err != nil {
			return fmt.Errorf("update SQL view metadata: %w", err)
		}
	}
	var hasCompoundIndexes bool
	if err := s.db.QueryRow(ctx, `select to_regclass('pgbase_meta.compound_indexes') is not null`).Scan(&hasCompoundIndexes); err != nil {
		return err
	}
	if hasCompoundIndexes {
		if _, err := s.db.Exec(ctx, `delete from pgbase_meta.compound_indexes where collection_id = $1`, collection.ID); err != nil {
			return fmt.Errorf("clear compound index metadata: %w", err)
		}
	}
	if err := s.renameFieldReferences(ctx, collection); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `delete from pgbase_meta.collection_indexes where collection_id=$1`, collection.ID); err != nil {
		return fmt.Errorf("clear field indexes: %w", err)
	}
	if _, err := s.db.Exec(ctx, `delete from pgbase_meta.field_relations where source_collection_id=$1`, collection.ID); err != nil {
		return fmt.Errorf("clear field relations: %w", err)
	}
	retained := make([]uuid.UUID, len(collection.Fields))
	for i, field := range collection.Fields {
		retained[i] = field.ID
	}
	if _, err := s.db.Exec(ctx, `delete from pgbase_meta.fields where collection_id=$1 and not(id=any($2::uuid[]))`, collection.ID, retained); err != nil {
		return fmt.Errorf("delete removed field metadata: %w", err)
	}
	if len(retained) > 0 {
		if _, err := s.db.Exec(ctx, `update pgbase_meta.fields set ordinal=ordinal+2000 where collection_id=$1 and id=any($2::uuid[])`, collection.ID, retained); err != nil {
			return fmt.Errorf("stage field ordinals: %w", err)
		}
	}
	return s.insertFieldMetadata(ctx, collection, definition, updatedAt)
}

func (s *Store) renameFieldReferences(ctx context.Context, collection Collection) error {
	for _, field := range collection.Fields {
		var oldName string
		err := s.db.QueryRow(ctx, `select name from pgbase_meta.fields where id=$1 and collection_id=$2`, field.ID, collection.ID).Scan(&oldName)
		if errors.Is(err, pgx.ErrNoRows) || oldName == field.Name {
			continue
		}
		if err != nil {
			return fmt.Errorf("read prior field name: %w", err)
		}
		if _, err = s.db.Exec(ctx, `update pgbase_meta.collection_views set projection=array(select case when value=$2 then $3 else value end from unnest(projection) value), filter_json=case when filter_json is null then null else pgbase_meta.rename_field_references(filter_json,$2,$3) end where source_collection_id=$1`, collection.ID, oldName, field.Name); err != nil {
			return fmt.Errorf("rename view references: %w", err)
		}
		if _, err = s.db.Exec(ctx, `update pgbase_meta.fields set name=$3 where name=$2 and collection_id in(select collection_id from pgbase_meta.collection_views where source_collection_id=$1)`, collection.ID, oldName, field.Name); err != nil {
			return fmt.Errorf("rename view fields: %w", err)
		}
		if _, err = s.db.Exec(ctx, `update pgbase_meta.collection_rules set rules=pgbase_meta.rename_field_references(rules,$2,$3),revision=revision+1,updated_at=clock_timestamp() where collection_id=$1`, collection.ID, oldName, field.Name); err != nil {
			return fmt.Errorf("rename rule references: %w", err)
		}
		var hasFiles bool
		if err = s.db.QueryRow(ctx, `select to_regclass('pgbase_meta.record_files') is not null`).Scan(&hasFiles); err != nil {
			return err
		}
		if hasFiles {
			if _, err = s.db.Exec(ctx, `update pgbase_meta.record_files set field_name=$3 where collection_id=$1 and field_name=$2`, collection.ID, oldName, field.Name); err != nil {
				return fmt.Errorf("rename file references: %w", err)
			}
		}
	}
	return nil
}

func (s *Store) DeleteCollection(ctx context.Context, collectionID uuid.UUID) error {
	// A service credential outlives any one collection, but a scope cannot.
	// This runs inside the schema apply transaction, after the table DDL and
	// while the schema advisory lock is held, so authorization either observes
	// the collection and its scope or neither of them.
	var hasServiceTokenScopes bool
	if err := s.db.QueryRow(ctx, `select to_regclass('pgbase_meta.service_token_scopes') is not null`).Scan(&hasServiceTokenScopes); err != nil {
		return fmt.Errorf("check collection service token scopes: %w", err)
	}
	if hasServiceTokenScopes {
		if _, err := s.db.Exec(ctx, `delete from pgbase_meta.service_token_scopes where collection_id = $1`, collectionID); err != nil {
			return fmt.Errorf("delete collection service token scopes: %w", err)
		}
	}
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
		optionsJSON, err := json.Marshal(plannedField.Options)
		if err != nil {
			return fmt.Errorf("encode field options: %w", err)
		}
		if len(plannedField.Default) != 0 {
			defaultJSON = string(plannedField.Default)
		}
		if _, err := s.db.Exec(ctx, `insert into pgbase_meta.fields
			(id, collection_id, name, column_name, field_type, required, is_unique, is_indexed,
			 has_default, default_json, ordinal, created_at, options_json, hidden, presentable, help)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11, $12, $13::jsonb, $14, $15, $16)
			on conflict(id) do update set name=excluded.name,column_name=excluded.column_name,field_type=excluded.field_type,required=excluded.required,is_unique=excluded.is_unique,is_indexed=excluded.is_indexed,has_default=excluded.has_default,default_json=excluded.default_json,ordinal=excluded.ordinal,options_json=excluded.options_json,hidden=excluded.hidden,presentable=excluded.presentable,help=excluded.help`,
			field.ID, collection.ID, field.Name, field.ColumnName, field.Type,
			field.Required, field.Unique, field.Indexed, len(plannedField.Default) != 0,
			defaultJSON, field.Ordinal, createdAt, string(optionsJSON), plannedField.Hidden, plannedField.Presentable, plannedField.Help); err != nil {
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
	fieldIDs := make(map[string]uuid.UUID, len(collection.Fields))
	for _, field := range collection.Fields {
		fieldIDs[field.Name] = field.ID
	}
	indexMetadata := make(map[string]Index, len(collection.Indexes))
	for _, index := range collection.Indexes {
		indexMetadata[index.Name] = index
	}
	if len(indexMetadata) != len(definition.Indexes) {
		return errCatalogIntegrity
	}
	for _, planned := range definition.Indexes {
		stored, ok := indexMetadata[planned.Name]
		if !ok || stored.ID == uuid.Nil || stored.IndexName != compoundIndexName(collection.Name, planned.Name) || stored.Unique != planned.Unique || !stringSlicesEqual(stored.Fields, planned.Fields) {
			return errCatalogIntegrity
		}
		indexID := stored.ID
		if _, err := s.db.Exec(ctx, `insert into pgbase_meta.compound_indexes(id,collection_id,name,index_name,is_unique,created_at) values($1,$2,$3,$4,$5,$6)`, indexID, collection.ID, planned.Name, compoundIndexName(collection.Name, planned.Name), planned.Unique, createdAt); err != nil {
			return fmt.Errorf("insert compound index metadata: %w", err)
		}
		for ordinal, name := range planned.Fields {
			fieldID, ok := fieldIDs[name]
			if !ok {
				return errCatalogIntegrity
			}
			if _, err := s.db.Exec(ctx, `insert into pgbase_meta.compound_index_fields(index_id,field_id,ordinal) values($1,$2,$3)`, indexID, fieldID, ordinal); err != nil {
				return fmt.Errorf("insert compound index field: %w", err)
			}
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
