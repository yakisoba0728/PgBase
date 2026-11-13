ALTER TABLE pgbase_meta.record_files
    DROP CONSTRAINT record_files_field_name_check,
    ADD CONSTRAINT record_files_field_name_check
        CHECK (field_name ~ '^[a-z][a-z0-9_]{0,63}$');
