ALTER TABLE pgbase_meta.fields
    DROP CONSTRAINT fields_field_type_check,
    ADD CONSTRAINT fields_field_type_check CHECK (
        field_type IN ('boolean','integer','bigint','numeric','text','timestamptz','uuid','jsonb','relation','file')
    );

CREATE TABLE pgbase_meta.record_files (
    id uuid PRIMARY KEY,
    collection_id uuid NOT NULL,
    record_id uuid NOT NULL,
    owner_id uuid REFERENCES pgbase_meta.users(id) ON DELETE RESTRICT,
    field_name text NOT NULL CHECK (field_name ~ '^[a-z][a-z0-9_]{0,47}$'),
    filename text NOT NULL CHECK (
        octet_length(filename) BETWEEN 1 AND 255
        AND filename !~ '[[:cntrl:]\\/]'
    ),
    content_type text NOT NULL CHECK (octet_length(content_type) BETWEEN 1 AND 127),
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    sha256 bytea NOT NULL CHECK (octet_length(sha256) = 32),
    object_key text NOT NULL UNIQUE CHECK (object_key ~ '^objects/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
    thumbnail_object_key text UNIQUE CHECK (
        thumbnail_object_key IS NULL OR thumbnail_object_key ~ '^thumbnails/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
    ),
    status text NOT NULL CHECK (status IN ('pending','ready','deleted')),
    created_at timestamptz NOT NULL,
    ready_at timestamptz,
    deleted_at timestamptz,
    FOREIGN KEY (collection_id, record_id)
        REFERENCES pgbase_meta.record_revisions(collection_id, record_id) ON DELETE CASCADE,
    CHECK ((
        (status = 'pending' AND ready_at IS NULL AND deleted_at IS NULL)
        OR (status = 'ready' AND ready_at IS NOT NULL AND deleted_at IS NULL)
        OR (status = 'deleted' AND deleted_at IS NOT NULL)
    ) IS TRUE)
);

CREATE INDEX record_files_record_idx
    ON pgbase_meta.record_files(collection_id, record_id, field_name, created_at, id)
    WHERE status = 'ready';
CREATE INDEX record_files_pending_idx
    ON pgbase_meta.record_files(created_at, id)
    WHERE status = 'pending';

CREATE TABLE pgbase_meta.file_object_deletions (
    object_key text PRIMARY KEY CHECK (
        object_key ~ '^(objects|thumbnails)/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
    ),
    queued_at timestamptz NOT NULL
);

CREATE FUNCTION pgbase_meta.queue_record_file_objects()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO pgbase_meta.file_object_deletions(object_key, queued_at)
    VALUES (OLD.object_key, transaction_timestamp())
    ON CONFLICT (object_key) DO NOTHING;
    IF OLD.thumbnail_object_key IS NOT NULL THEN
        INSERT INTO pgbase_meta.file_object_deletions(object_key, queued_at)
        VALUES (OLD.thumbnail_object_key, transaction_timestamp())
        ON CONFLICT (object_key) DO NOTHING;
    END IF;
    RETURN OLD;
END;
$$;

CREATE TRIGGER record_files_queue_objects_after_delete
AFTER DELETE ON pgbase_meta.record_files
FOR EACH ROW EXECUTE FUNCTION pgbase_meta.queue_record_file_objects();

CREATE FUNCTION pgbase_meta.delete_record_files_for_field()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    DELETE FROM pgbase_meta.record_files
    WHERE collection_id = OLD.collection_id AND field_name = OLD.name;
    RETURN OLD;
END;
$$;

CREATE TRIGGER fields_delete_record_files
AFTER DELETE ON pgbase_meta.fields
FOR EACH ROW EXECUTE FUNCTION pgbase_meta.delete_record_files_for_field();
