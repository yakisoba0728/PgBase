ALTER TABLE pgbase_meta.extension_objects DROP CONSTRAINT extension_objects_object_key_check;
ALTER TABLE pgbase_meta.extension_objects ADD CONSTRAINT extension_objects_object_key_check CHECK (
    object_key ~ '^extensions/[0-9a-f]{64}/[0-9a-f]{64}(?:/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})?$'
);

ALTER TABLE pgbase_meta.file_object_deletions DROP CONSTRAINT file_object_deletions_object_key_check;
ALTER TABLE pgbase_meta.file_object_deletions ADD CONSTRAINT file_object_deletions_object_key_check CHECK (
    object_key ~ '^(?:(objects|thumbnails)/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|extensions/[0-9a-f]{64}/[0-9a-f]{64}(?:/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})?)$'
);
