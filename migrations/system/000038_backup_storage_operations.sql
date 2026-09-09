CREATE TABLE pgbase_meta.backup_storage_operations (
    backup_id uuid PRIMARY KEY,
    settings_revision bigint NOT NULL CHECK (settings_revision > 0),
    status text NOT NULL CHECK (status IN ('pending','committed')),
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
