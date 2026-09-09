CREATE TABLE pgbase_meta.file_storage_migrations (
    id uuid PRIMARY KEY,
    status text NOT NULL CHECK (status IN ('copying','canceling','canceled','failed','ready','activating','committed')),
    base_revision bigint NOT NULL CHECK (base_revision > 0),
    source_config_ciphertext bytea NOT NULL,
    destination_config_ciphertext bytea NOT NULL,
    request_hash bytea NOT NULL,
    cancel_requested boolean NOT NULL DEFAULT false,
    copied_objects bigint NOT NULL DEFAULT 0,
    total_objects bigint NOT NULL DEFAULT 0,
    failure text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    committed_at timestamptz
);

CREATE UNIQUE INDEX file_storage_migrations_active_idx
ON pgbase_meta.file_storage_migrations ((true))
WHERE status IN ('copying','canceling','ready','activating');

CREATE TABLE pgbase_meta.file_storage_migration_objects (
    migration_id uuid NOT NULL REFERENCES pgbase_meta.file_storage_migrations(id) ON DELETE CASCADE,
    object_key text NOT NULL,
    object_kind text NOT NULL CHECK (object_kind IN ('original','thumbnail','variant','cleanup')),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','copying','verified','failed')),
    size_bytes bigint,
    sha256 bytea,
    attempts integer NOT NULL DEFAULT 0,
    claim_token uuid,
    claim_expires_at timestamptz,
    created_destination boolean NOT NULL DEFAULT false,
    failure text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (migration_id, object_key)
);

CREATE INDEX file_storage_migration_pending_idx
ON pgbase_meta.file_storage_migration_objects (migration_id, status, object_key);

CREATE TABLE pgbase_meta.file_storage_source_cleanup (
    migration_id uuid NOT NULL REFERENCES pgbase_meta.file_storage_migrations(id) ON DELETE CASCADE,
    object_key text NOT NULL,
    source_config_ciphertext bytea NOT NULL,
    not_before timestamptz NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    failure text NOT NULL DEFAULT '',
    completed_at timestamptz,
    PRIMARY KEY (migration_id, object_key)
);
