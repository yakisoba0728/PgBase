-- Durable schema operation claims and receipts.
CREATE TABLE pgbase_meta.schema_operations (
    operation_id uuid PRIMARY KEY,
    mode text NOT NULL,
    actor_kind text NOT NULL,
    actor_id uuid NOT NULL,
    resource_kind text NOT NULL,
    resource_id uuid NOT NULL,
    resource_key_hash bytea NOT NULL,
    operation text NOT NULL,
    idempotency_key_hash bytea,
    request_hash bytea NOT NULL,
    plan_checksum bytea NOT NULL,
    base_version bigint NOT NULL,
    status text NOT NULL,
    fence_generation bigint NOT NULL,
    lease_owner uuid,
    lease_expires_at timestamptz,
    reconcile_after timestamptz,
    committed_version bigint,
    failure_code text,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    completed_at timestamptz
);

ALTER TABLE pgbase_meta.schema_migrations
    ADD CONSTRAINT schema_migrations_receipt_unique
    UNIQUE (version, operation_id, operation, checksum, applied_by);

ALTER TABLE pgbase_meta.schema_operations
    ADD CONSTRAINT schema_operations_actor_fk
    FOREIGN KEY (actor_id)
    REFERENCES pgbase_meta.admins(id)
    ON DELETE RESTRICT;

ALTER TABLE pgbase_meta.schema_operations
    ADD CONSTRAINT schema_operations_migration_fk
    FOREIGN KEY (
        committed_version,
        operation_id,
        operation,
        plan_checksum,
        actor_id
    )
    REFERENCES pgbase_meta.schema_migrations (
        version,
        operation_id,
        operation,
        checksum,
        applied_by
    )
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE pgbase_meta.schema_operations
    ADD CONSTRAINT schema_operations_mode_check
        CHECK (mode IN ('legacy', 'guarded')),
    ADD CONSTRAINT schema_operations_actor_kind_check
        CHECK (actor_kind = 'administrator'),
    ADD CONSTRAINT schema_operations_resource_kind_check
        CHECK (resource_kind = 'collection'),
    ADD CONSTRAINT schema_operations_resource_key_hash_check
        CHECK (octet_length(resource_key_hash) = 32),
    -- The ledger uses the existing audit vocabulary so the deferred receipt
    -- foreign key compares exact persisted values without rewriting history.
    ADD CONSTRAINT schema_operations_operation_check
        CHECK (operation IN (
            'collection_create',
            'collection_update',
            'collection_delete'
        )),
    ADD CONSTRAINT schema_operations_idempotency_check
        CHECK ((
            (mode = 'legacy' AND idempotency_key_hash IS NULL)
            OR
            (mode = 'guarded' AND octet_length(idempotency_key_hash) = 32)
        ) IS TRUE),
    ADD CONSTRAINT schema_operations_request_hash_check
        CHECK (octet_length(request_hash) = 32),
    ADD CONSTRAINT schema_operations_plan_checksum_check
        CHECK (octet_length(plan_checksum) = 32),
    ADD CONSTRAINT schema_operations_base_version_check
        CHECK (base_version >= 0),
    ADD CONSTRAINT schema_operations_status_check
        CHECK (status IN ('running', 'unknown', 'committed', 'failed')),
    ADD CONSTRAINT schema_operations_fence_generation_check
        CHECK (fence_generation >= 1),
    ADD CONSTRAINT schema_operations_failure_code_check
        CHECK (
            failure_code IS NULL
            OR failure_code IN (
                'plan_conflict',
                'apply_failed',
                'lease_lost',
                'reconcile_failed'
            )
        ),
    ADD CONSTRAINT schema_operations_timestamp_order_check
        CHECK (updated_at >= created_at),
    ADD CONSTRAINT schema_operations_state_check
        CHECK ((
            (status = 'running'
                AND lease_owner IS NOT NULL
                AND lease_expires_at > updated_at
                AND lease_expires_at <= updated_at + interval '2 minutes'
                AND reconcile_after >= lease_expires_at
                AND reconcile_after <= lease_expires_at + interval '10 seconds'
                AND committed_version IS NULL
                AND failure_code IS NULL
                AND completed_at IS NULL)
            OR
            (status = 'unknown'
                AND lease_owner IS NULL
                AND lease_expires_at IS NULL
                AND reconcile_after >= updated_at
                AND reconcile_after <= updated_at + interval '10 seconds'
                AND committed_version IS NULL
                AND failure_code IS NULL
                AND completed_at IS NULL)
            OR
            (status = 'committed'
                AND lease_owner IS NULL
                AND lease_expires_at IS NULL
                AND reconcile_after IS NULL
                AND committed_version = base_version + 1
                AND failure_code IS NULL
                AND completed_at = updated_at)
            OR
            (status = 'failed'
                AND lease_owner IS NULL
                AND lease_expires_at IS NULL
                AND reconcile_after IS NULL
                AND committed_version IS NULL
                AND failure_code IS NOT NULL
                AND completed_at = updated_at)
        ) IS TRUE);

CREATE UNIQUE INDEX schema_operations_guarded_identity_unique
    ON pgbase_meta.schema_operations (
        actor_kind,
        actor_id,
        resource_kind,
        resource_key_hash,
        idempotency_key_hash
    )
    WHERE mode = 'guarded';

CREATE INDEX schema_operations_owner_history_idx
    ON pgbase_meta.schema_operations (
        actor_kind,
        actor_id,
        created_at DESC,
        operation_id DESC
    );

CREATE INDEX schema_operations_reconcile_idx
    ON pgbase_meta.schema_operations (reconcile_after, operation_id)
    WHERE status IN ('running', 'unknown');

CREATE INDEX schema_operations_cleanup_idx
    ON pgbase_meta.schema_operations (completed_at, operation_id)
    WHERE status IN ('committed', 'failed');
