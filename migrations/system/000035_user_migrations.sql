CREATE TABLE pgbase_meta.user_migrations (
    id text PRIMARY KEY CHECK (id ~ '^[0-9]{4,14}_[a-z0-9]+(?:_[a-z0-9]+)*$'),
    revision bytea NOT NULL CHECK (octet_length(revision) = 32),
    applied_at timestamptz NOT NULL,
    execution_ms bigint NOT NULL CHECK (execution_ms >= 0)
);
