CREATE TABLE pgbase_meta.collection_rules (
    collection_id uuid PRIMARY KEY REFERENCES pgbase_meta.collections(id) ON DELETE CASCADE,
    rules jsonb NOT NULL CHECK (jsonb_typeof(rules)='object'),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision>0),
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
