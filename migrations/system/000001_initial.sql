CREATE SCHEMA IF NOT EXISTS pgbase_meta;
CREATE SCHEMA IF NOT EXISTS app;

CREATE TABLE pgbase_meta.schema_state (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    version bigint NOT NULL CHECK (version >= 0),
    updated_at timestamptz NOT NULL
);
INSERT INTO pgbase_meta.schema_state (singleton, version, updated_at)
VALUES (true, 0, CURRENT_TIMESTAMP);

CREATE TABLE pgbase_meta.admins (
    id uuid PRIMARY KEY,
    email text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE pgbase_meta.admin_sessions (
    id uuid PRIMARY KEY,
    admin_id uuid NOT NULL REFERENCES pgbase_meta.admins(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL
);
CREATE INDEX admin_sessions_admin_id_idx ON pgbase_meta.admin_sessions(admin_id);
CREATE INDEX admin_sessions_expires_at_idx ON pgbase_meta.admin_sessions(expires_at);

CREATE TABLE pgbase_meta.admin_rate_limits (
    subject_hash bytea PRIMARY KEY CHECK (octet_length(subject_hash) = 32),
    window_started_at timestamptz NOT NULL,
    failures integer NOT NULL CHECK (failures >= 0),
    blocked_until timestamptz,
    updated_at timestamptz NOT NULL
);

CREATE TABLE pgbase_meta.collections (
    id uuid PRIMARY KEY,
    name text NOT NULL UNIQUE,
    table_name text NOT NULL UNIQUE,
    status text NOT NULL CHECK (status = 'active'),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE pgbase_meta.fields (
    id uuid PRIMARY KEY,
    collection_id uuid NOT NULL REFERENCES pgbase_meta.collections(id) ON DELETE CASCADE,
    name text NOT NULL,
    column_name text NOT NULL,
    field_type text NOT NULL CHECK (field_type IN ('boolean','integer','bigint','numeric','text','timestamptz','uuid','jsonb')),
    required boolean NOT NULL,
    is_unique boolean NOT NULL,
    is_indexed boolean NOT NULL,
    has_default boolean NOT NULL,
    default_json jsonb,
    ordinal integer NOT NULL CHECK (ordinal >= 0),
    created_at timestamptz NOT NULL,
    UNIQUE (collection_id, name),
    UNIQUE (collection_id, column_name),
    UNIQUE (collection_id, ordinal)
);

CREATE TABLE pgbase_meta.collection_indexes (
    id uuid PRIMARY KEY,
    collection_id uuid NOT NULL REFERENCES pgbase_meta.collections(id) ON DELETE CASCADE,
    field_id uuid NOT NULL REFERENCES pgbase_meta.fields(id) ON DELETE CASCADE,
    index_name text NOT NULL UNIQUE,
    is_unique boolean NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (collection_id, field_id)
);

CREATE TABLE pgbase_meta.schema_migrations (
    version bigint PRIMARY KEY CHECK (version > 0),
    operation_id uuid NOT NULL UNIQUE,
    operation text NOT NULL CHECK (operation = 'collection_create'),
    checksum bytea NOT NULL CHECK (octet_length(checksum) = 32),
    plan_json jsonb NOT NULL,
    sql_statements text[] NOT NULL,
    applied_by uuid NOT NULL REFERENCES pgbase_meta.admins(id) ON DELETE RESTRICT,
    applied_at timestamptz NOT NULL,
    execution_ms bigint NOT NULL CHECK (execution_ms >= 0)
);
