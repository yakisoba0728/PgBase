CREATE TABLE pgbase_meta.collection_views (
    collection_id uuid PRIMARY KEY REFERENCES pgbase_meta.collections(id) ON DELETE CASCADE,
    source_collection_id uuid NOT NULL REFERENCES pgbase_meta.collections(id) ON DELETE RESTRICT,
    projection text[] NOT NULL CHECK (cardinality(projection) BETWEEN 1 AND 128 AND array_position(projection,NULL) IS NULL),
    filter_json jsonb,
    created_at timestamptz NOT NULL,
    CHECK (collection_id <> source_collection_id),
    CHECK (filter_json IS NULL OR jsonb_typeof(filter_json)='object')
);
