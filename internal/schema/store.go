package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
)

type Store struct {
	db database.DBTX
}

func NewStore(db database.DBTX) *Store { return &Store{db: db} }

func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	var version int64
	if err := s.db.QueryRow(ctx, `select version from pgbase_meta.schema_state where singleton`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

func (s *Store) CollectionExists(ctx context.Context, name, tableName string) (bool, error) {
	var exists bool
	if err := s.db.QueryRow(ctx, `select exists (
		select 1 from pgbase_meta.collections where name = $1 or table_name = $2
	)`, name, tableName).Scan(&exists); err != nil {
		return false, fmt.Errorf("check collection existence: %w", err)
	}
	return exists, nil
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

	fieldRows, err := s.db.Query(ctx, `select id, collection_id, name, column_name, field_type,
		required, is_unique, is_indexed, has_default, default_json, ordinal
		from pgbase_meta.fields order by collection_id, ordinal`)
	if err != nil {
		return Catalog{}, fmt.Errorf("read collection fields: %w", err)
	}
	defer fieldRows.Close()
	for fieldRows.Next() {
		var field Field
		var collectionID uuid.UUID
		var hasDefault bool
		var defaultJSON []byte
		if err := fieldRows.Scan(
			&field.ID, &collectionID, &field.Name, &field.ColumnName, &field.Type,
			&field.Required, &field.Unique, &field.Indexed, &hasDefault, &defaultJSON, &field.Ordinal,
		); err != nil {
			return Catalog{}, fmt.Errorf("scan collection field: %w", err)
		}
		if hasDefault {
			field.Default = append(json.RawMessage(nil), defaultJSON...)
		}
		name, ok := collectionNames[collectionID]
		if !ok {
			return Catalog{}, fmt.Errorf("collection field references unknown collection")
		}
		collection := collections[name]
		collection.Fields = append(collection.Fields, field)
		collections[name] = collection
	}
	if err := fieldRows.Err(); err != nil {
		return Catalog{}, fmt.Errorf("iterate collection fields: %w", err)
	}
	return Catalog{Version: version, Collections: collections}, nil
}

func (s *Store) InsertCollection(ctx context.Context, collection Collection, definition PlannedCollection, createdAt time.Time) error {
	if _, err := s.db.Exec(ctx, `insert into pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at)
		values ($1, $2, $3, 'active', $4, $4)`,
		collection.ID, collection.Name, collection.TableName, createdAt); err != nil {
		return fmt.Errorf("insert collection metadata: %w", err)
	}
	for index, plannedField := range definition.Fields {
		field := collection.Fields[index]
		var defaultJSON any
		if len(plannedField.Default) != 0 {
			defaultJSON = string(plannedField.Default)
		}
		if _, err := s.db.Exec(ctx, `insert into pgbase_meta.fields
			(id, collection_id, name, column_name, field_type, required, is_unique, is_indexed,
			 has_default, default_json, ordinal, created_at)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11, $12)`,
			field.ID, collection.ID, field.Name, field.ColumnName, field.Type,
			field.Required, field.Unique, field.Indexed, len(field.Default) != 0,
			defaultJSON, field.Ordinal, createdAt); err != nil {
			return fmt.Errorf("insert field metadata: %w", err)
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
