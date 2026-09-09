CREATE TABLE pgbase_meta.sql_console_executions (
    execution_id uuid PRIMARY KEY,
    administrator_id uuid NOT NULL REFERENCES pgbase_meta.admins(id) ON DELETE RESTRICT,
    mode text NOT NULL,
    dry_run boolean NOT NULL,
    idempotency_hash bytea,
    request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
    query_fingerprint bytea NOT NULL CHECK (octet_length(query_fingerprint) = 32),
    status text NOT NULL,
    statement_count smallint NOT NULL CHECK (statement_count BETWEEN 1 AND 10),
    row_count integer NOT NULL DEFAULT 0 CHECK (row_count BETWEEN 0 AND 1000),
    affected_rows integer NOT NULL DEFAULT 0 CHECK (affected_rows BETWEEN 0 AND 1000),
    result_bytes integer NOT NULL DEFAULT 0 CHECK (result_bytes BETWEEN 0 AND 4194304),
    sql_state text CHECK (sql_state ~ '^[0-9A-Z]{5}$'),
    failure_code text CHECK (failure_code IS NULL OR octet_length(failure_code) BETWEEN 1 AND 64),
    committed boolean,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    started_at timestamptz,
    finished_at timestamptz,
    result_expires_at timestamptz,
    CONSTRAINT sql_console_executions_mode_check CHECK ((
        (mode = 'read' AND NOT dry_run AND idempotency_hash IS NULL)
        OR (mode = 'write' AND octet_length(idempotency_hash) = 32)
    ) IS TRUE),
    CONSTRAINT sql_console_executions_status_check CHECK (
        status IN ('pending','running','succeeded','failed','canceled','timed_out','unknown')
    ),
    CONSTRAINT sql_console_executions_time_check CHECK (
        updated_at >= created_at
        AND (started_at IS NULL OR started_at >= created_at)
        AND (finished_at IS NULL OR (finished_at >= created_at AND (started_at IS NULL OR finished_at >= started_at)))
        AND (result_expires_at IS NULL OR (finished_at IS NOT NULL AND result_expires_at >= finished_at))
    ),
    CONSTRAINT sql_console_executions_state_check CHECK ((
        (status = 'pending'
            AND started_at IS NULL AND finished_at IS NULL AND result_expires_at IS NULL
            AND sql_state IS NULL AND failure_code IS NULL AND committed IS NULL)
        OR (status = 'running'
            AND started_at IS NOT NULL AND finished_at IS NULL AND result_expires_at IS NULL
            AND sql_state IS NULL AND failure_code IS NULL AND committed IS NULL)
        OR (status = 'succeeded'
            AND started_at IS NOT NULL AND finished_at IS NOT NULL AND result_expires_at IS NOT NULL
            AND sql_state IS NULL AND failure_code IS NULL
            AND committed = (mode = 'write' AND NOT dry_run))
        OR (status IN ('failed','canceled','timed_out')
            AND finished_at IS NOT NULL AND result_expires_at IS NOT NULL
            AND failure_code IS NOT NULL AND committed = false)
        OR (status = 'unknown'
            AND mode = 'write' AND NOT dry_run AND started_at IS NOT NULL AND finished_at IS NOT NULL
            AND result_expires_at IS NOT NULL AND failure_code = 'commit_unknown' AND committed IS NULL)
    ) IS TRUE),
    UNIQUE (execution_id, administrator_id, idempotency_hash, request_hash)
);

CREATE UNIQUE INDEX sql_console_executions_idempotency_idx
    ON pgbase_meta.sql_console_executions (administrator_id, idempotency_hash)
    WHERE idempotency_hash IS NOT NULL;

CREATE INDEX sql_console_executions_history_idx
    ON pgbase_meta.sql_console_executions (administrator_id, created_at DESC, execution_id DESC);

CREATE INDEX sql_console_executions_reconcile_idx
    ON pgbase_meta.sql_console_executions (updated_at, execution_id)
    WHERE status IN ('pending','running','unknown');

CREATE INDEX sql_console_executions_cleanup_idx
    ON pgbase_meta.sql_console_executions (result_expires_at, execution_id)
    WHERE result_expires_at IS NOT NULL;

CREATE TABLE pgbase_meta.sql_console_write_receipts (
    execution_id uuid PRIMARY KEY,
    administrator_id uuid NOT NULL,
    idempotency_hash bytea NOT NULL CHECK (octet_length(idempotency_hash) = 32),
    request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
    row_count integer NOT NULL CHECK (row_count BETWEEN 0 AND 1000),
    affected_rows integer NOT NULL CHECK (affected_rows BETWEEN 0 AND 1000),
    result_bytes integer NOT NULL CHECK (result_bytes BETWEEN 0 AND 4194304),
    committed_at timestamptz NOT NULL,
    CONSTRAINT sql_console_write_receipts_execution_fk FOREIGN KEY (
        execution_id, administrator_id, idempotency_hash, request_hash
    ) REFERENCES pgbase_meta.sql_console_executions (
        execution_id, administrator_id, idempotency_hash, request_hash
    ) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX sql_console_write_receipts_idempotency_idx
    ON pgbase_meta.sql_console_write_receipts (administrator_id, idempotency_hash);

CREATE INDEX sql_console_write_receipts_committed_idx
    ON pgbase_meta.sql_console_write_receipts (committed_at, execution_id);
