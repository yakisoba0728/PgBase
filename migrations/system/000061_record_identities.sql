ALTER TABLE pgbase_meta.collections
    ADD COLUMN record_id jsonb NOT NULL DEFAULT '{"kind":"uuid"}'::jsonb,
    ADD CONSTRAINT collections_record_id_object
        CHECK (jsonb_typeof(record_id) = 'object'),
    ADD CONSTRAINT collections_record_id_kind
        CHECK (record_id->>'kind' IN ('uuid', 'text'));

CREATE TABLE pgbase_meta.record_identities (
    collection_id uuid NOT NULL
        REFERENCES pgbase_meta.collections(id) ON DELETE CASCADE,
    internal_id uuid NOT NULL,
    external_id text COLLATE "C" NOT NULL,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (collection_id, internal_id),
    UNIQUE (collection_id, external_id),
    CONSTRAINT record_identities_external_id_length
        CHECK (char_length(external_id) BETWEEN 1 AND 255)
);

REVOKE ALL ON pgbase_meta.record_identities FROM PUBLIC;
