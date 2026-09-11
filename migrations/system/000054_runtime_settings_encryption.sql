ALTER TABLE pgbase_meta.runtime_settings
    ADD COLUMN document_ciphertext bytea;

ALTER TABLE pgbase_meta.runtime_settings
    ALTER COLUMN document DROP NOT NULL;
