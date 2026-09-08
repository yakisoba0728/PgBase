CREATE TABLE pgbase_meta.compound_indexes (
    id uuid PRIMARY KEY,
    collection_id uuid NOT NULL REFERENCES pgbase_meta.collections(id) ON DELETE CASCADE,
    name text NOT NULL,
    index_name text NOT NULL UNIQUE,
    is_unique boolean NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (collection_id, name)
);

CREATE TABLE pgbase_meta.compound_index_fields (
    index_id uuid NOT NULL REFERENCES pgbase_meta.compound_indexes(id) ON DELETE CASCADE,
    field_id uuid NOT NULL REFERENCES pgbase_meta.fields(id) ON DELETE CASCADE,
    ordinal integer NOT NULL CHECK (ordinal >= 0),
    PRIMARY KEY (index_id, ordinal),
    UNIQUE (index_id, field_id)
);
