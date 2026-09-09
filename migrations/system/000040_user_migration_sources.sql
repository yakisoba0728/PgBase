CREATE TABLE pgbase_meta.user_migration_sources (
    operation_id uuid PRIMARY KEY,
    migration_id text NOT NULL UNIQUE,
    filename text NOT NULL UNIQUE,
    source bytea NOT NULL,
    revision bytea NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    attempts bigint NOT NULL DEFAULT 0,
    last_error text,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    written_at timestamptz,
    CONSTRAINT user_migration_sources_id_check CHECK (
        migration_id ~ '^[0-9]{14}_[a-z0-9]+(_[a-z0-9]+)*$'
        AND filename = migration_id || '.go'
    ),
    CONSTRAINT user_migration_sources_source_check CHECK (
        octet_length(source) BETWEEN 1 AND 67108864
        AND octet_length(revision) = 32
    ),
    CONSTRAINT user_migration_sources_status_check CHECK (
        status IN ('pending', 'written', 'conflict')
    ),
    CONSTRAINT user_migration_sources_attempts_check CHECK (attempts >= 0),
    CONSTRAINT user_migration_sources_error_check CHECK (
        last_error IS NULL OR octet_length(last_error) BETWEEN 1 AND 1024
    ),
    CONSTRAINT user_migration_sources_timestamp_check CHECK (
        updated_at >= created_at
        AND ((status = 'written' AND written_at IS NOT NULL AND written_at >= created_at AND last_error IS NULL)
            OR (status = 'pending' AND written_at IS NULL)
            OR (status = 'conflict' AND written_at IS NULL AND last_error IS NOT NULL))
    )
);

CREATE INDEX user_migration_sources_pending_idx
    ON pgbase_meta.user_migration_sources (created_at, operation_id)
    WHERE status = 'pending';

CREATE TABLE pgbase_meta.user_migration_schema_versions (
    version bigint PRIMARY KEY CHECK (version > 0),
    migration_id text NOT NULL,
    direction text NOT NULL CHECK (direction IN ('up', 'down')),
    revision bytea NOT NULL CHECK (octet_length(revision) = 32),
    applied_at timestamptz NOT NULL,
    UNIQUE (migration_id, direction, version)
);
