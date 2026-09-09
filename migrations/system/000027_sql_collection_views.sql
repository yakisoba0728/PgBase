ALTER TABLE pgbase_meta.collection_views
    ALTER COLUMN source_collection_id DROP NOT NULL,
    ALTER COLUMN projection DROP NOT NULL,
    ADD COLUMN sql text,
    ADD COLUMN dependencies uuid[];

ALTER TABLE pgbase_meta.collection_views
    ADD CONSTRAINT collection_views_definition_check CHECK (
        (sql IS NULL AND source_collection_id IS NOT NULL AND projection IS NOT NULL AND dependencies IS NULL)
        OR
        (sql IS NOT NULL AND length(sql) BETWEEN 1 AND 65536 AND source_collection_id IS NULL
            AND projection IS NULL AND filter_json IS NULL AND dependencies IS NOT NULL
            AND array_position(dependencies, NULL) IS NULL)
    );
