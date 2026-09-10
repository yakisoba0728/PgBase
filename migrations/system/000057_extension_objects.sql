CREATE TABLE pgbase_meta.extension_objects (
    namespace_hash text NOT NULL CHECK (namespace_hash ~ '^[0-9a-f]{64}$'),
    key_hash text NOT NULL CHECK (key_hash ~ '^[0-9a-f]{64}$'),
    object_key text NOT NULL UNIQUE CHECK (object_key ~ '^extensions/[0-9a-f]{64}/[0-9a-f]{64}$'),
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(namespace_hash,key_hash)
);

ALTER TABLE pgbase_meta.file_object_deletions DROP CONSTRAINT file_object_deletions_object_key_check;
ALTER TABLE pgbase_meta.file_object_deletions ADD CONSTRAINT file_object_deletions_object_key_check CHECK (
    object_key ~ '^(?:(objects|thumbnails)/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|extensions/[0-9a-f]{64}/[0-9a-f]{64})$'
);
ALTER TABLE pgbase_meta.file_storage_migration_objects DROP CONSTRAINT file_storage_migration_objects_object_kind_check;
ALTER TABLE pgbase_meta.file_storage_migration_objects ADD CONSTRAINT file_storage_migration_objects_object_kind_check CHECK (object_kind IN ('original','thumbnail','variant','extension','cleanup'));

CREATE FUNCTION pgbase_meta.queue_extension_object_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO pgbase_meta.file_object_deletions(object_key,queued_at)
    VALUES(OLD.object_key,CURRENT_TIMESTAMP) ON CONFLICT (object_key) DO NOTHING;
    RETURN OLD;
END;
$$;

CREATE TRIGGER extension_objects_queue_delete AFTER DELETE ON pgbase_meta.extension_objects
FOR EACH ROW EXECUTE FUNCTION pgbase_meta.queue_extension_object_delete();
