-- Logical names are retained for namespace-scoped filesystem listing. Existing
-- hashed-only objects remain readable and are intentionally omitted from lists.
ALTER TABLE pgbase_meta.extension_objects
    ADD COLUMN logical_namespace text,
    ADD COLUMN logical_key text;

CREATE INDEX extension_objects_namespace_key_idx
    ON pgbase_meta.extension_objects(logical_namespace, logical_key)
    WHERE logical_namespace IS NOT NULL AND logical_key IS NOT NULL;
