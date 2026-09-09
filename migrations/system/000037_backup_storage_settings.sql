CREATE TABLE pgbase_meta.backup_storage_settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    driver text NOT NULL CHECK (driver IN ('local','s3')),
    s3_bucket text NOT NULL DEFAULT '',
    s3_prefix text NOT NULL DEFAULT '',
    s3_region text NOT NULL DEFAULT '',
    s3_endpoint text NOT NULL DEFAULT '',
    s3_access_key text NOT NULL DEFAULT '',
    s3_secret_ciphertext bytea,
    s3_use_path_style boolean NOT NULL DEFAULT false,
    retention integer NOT NULL DEFAULT 0 CHECK (retention >= 0),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
