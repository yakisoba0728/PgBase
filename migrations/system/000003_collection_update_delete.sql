ALTER TABLE pgbase_meta.schema_migrations
    DROP CONSTRAINT schema_migrations_operation_check;

ALTER TABLE pgbase_meta.schema_migrations
    ADD CONSTRAINT schema_migrations_operation_check
    CHECK (operation IN ('collection_create', 'collection_update', 'collection_delete'));
