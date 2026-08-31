ALTER TABLE pgbase_meta.fields
    DROP CONSTRAINT fields_field_type_check;

ALTER TABLE pgbase_meta.fields
    ADD CONSTRAINT fields_field_type_check
    CHECK (field_type IN (
        'boolean',
        'integer',
        'bigint',
        'numeric',
        'text',
        'timestamptz',
        'uuid',
        'jsonb',
        'relation'
    ));

CREATE SCHEMA pgbase_relation;

ALTER TABLE pgbase_meta.fields
    ADD CONSTRAINT fields_id_collection_id_unique
    UNIQUE (id, collection_id);

CREATE TABLE pgbase_meta.field_relations (
    field_id uuid PRIMARY KEY,
    source_collection_id uuid NOT NULL,
    target_collection_id uuid NOT NULL
        REFERENCES pgbase_meta.collections(id) ON DELETE RESTRICT,
    cardinality text NOT NULL
        CHECK (cardinality IN ('single', 'multiple')),
    min_select integer NOT NULL
        CHECK (min_select BETWEEN 0 AND 1000),
    max_select integer NOT NULL
        CHECK (max_select BETWEEN 0 AND 1000),
    on_delete text NOT NULL
        CHECK (on_delete IN ('restrict', 'cascade', 'set_null')),
    value_codec_version integer NOT NULL
        CHECK (value_codec_version = 1),
    storage_kind text NOT NULL
        CHECK (storage_kind IN ('inline_fk_v1', 'edge_table_v1')),
    storage_name text NOT NULL
        CHECK (
            octet_length(storage_name) BETWEEN 1 AND 63
            AND storage_name ~ '^[a-z][a-z0-9_]{0,62}$'
        ),
    created_at timestamptz NOT NULL,
    CONSTRAINT field_relations_source_field_fk
        FOREIGN KEY (field_id, source_collection_id)
        REFERENCES pgbase_meta.fields(id, collection_id)
        ON DELETE CASCADE,
    CONSTRAINT field_relations_cardinality_bounds_check CHECK (
        (cardinality = 'single'
            AND min_select IN (0, 1)
            AND max_select = 1
            AND storage_kind = 'inline_fk_v1')
        OR
        (cardinality = 'multiple'
            AND min_select <= max_select
            AND max_select BETWEEN 2 AND 1000
            AND storage_kind = 'edge_table_v1')
    ),
    CONSTRAINT field_relations_set_null_minimum_check CHECK (
        on_delete <> 'set_null' OR min_select = 0
    ),
    CONSTRAINT field_relations_source_storage_unique
        UNIQUE (source_collection_id, storage_name)
);

CREATE INDEX field_relations_source_collection_idx
    ON pgbase_meta.field_relations (source_collection_id, field_id);

CREATE INDEX field_relations_incoming_collection_idx
    ON pgbase_meta.field_relations (target_collection_id, field_id);

-- A self relation is both an outgoing row owned by the collection and an
-- incoming target reference to that same collection. PostgreSQL's immediate
-- RESTRICT action is checked before the source-side cascade can remove the
-- metadata row, which would otherwise make a self-related collection
-- impossible to delete. Remove only those self-owned metadata rows first;
-- external incoming rows remain protected by the target RESTRICT FK.
CREATE FUNCTION pgbase_meta.remove_self_relation_metadata_before_collection_delete()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    DELETE FROM pgbase_meta.field_relations
    WHERE source_collection_id = OLD.id
      AND target_collection_id = OLD.id;
    RETURN OLD;
END;
$$;

CREATE TRIGGER collections_remove_self_relation_metadata
BEFORE DELETE ON pgbase_meta.collections
FOR EACH ROW
EXECUTE FUNCTION pgbase_meta.remove_self_relation_metadata_before_collection_delete();
