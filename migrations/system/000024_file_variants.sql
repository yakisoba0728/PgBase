CREATE TABLE pgbase_meta.record_file_variants (
    file_id uuid NOT NULL REFERENCES pgbase_meta.record_files(id) ON DELETE CASCADE,
    variant text NOT NULL CHECK (variant ~ '^(?:0|[1-9][0-9]{0,3})x(?:0|[1-9][0-9]{0,3})[tbf]?$'),
    object_key text NOT NULL UNIQUE CHECK (object_key ~ '^thumbnails/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (file_id, variant)
);

CREATE FUNCTION pgbase_meta.queue_record_file_variant_object()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO pgbase_meta.file_object_deletions(object_key, queued_at)
    VALUES (OLD.object_key, transaction_timestamp()) ON CONFLICT DO NOTHING;
    RETURN OLD;
END;
$$;

CREATE TRIGGER record_file_variants_queue_object_after_delete
AFTER DELETE ON pgbase_meta.record_file_variants
FOR EACH ROW EXECUTE FUNCTION pgbase_meta.queue_record_file_variant_object();
