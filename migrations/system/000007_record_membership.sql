-- Serialize the additive metadata install with collection DDL. Dynamic record
-- tables are deliberately not inspected or changed by this migration.
SELECT version
FROM pgbase_meta.schema_state
WHERE singleton
FOR UPDATE;

CREATE TABLE pgbase_meta.record_membership_capability (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    state text NOT NULL CHECK (state IN ('off', 'cutover', 'dual_write', 'ready')),
    generation bigint NOT NULL CHECK (generation >= 0),
    minimum_writer_version smallint NOT NULL CHECK (minimum_writer_version BETWEEN 0 AND 3),
    cursor_issue_version smallint NOT NULL CHECK (cursor_issue_version IN (4, 5)),
    cutover_at timestamptz,
    cutover_collection_count bigint CHECK (cutover_collection_count >= 0),
    cutover_record_count bigint CHECK (cutover_record_count >= 0),
    ready_at timestamptz,
    ready_collection_count bigint CHECK (ready_collection_count >= 0),
    v5_enabled_at timestamptz,
    v4_decode_until timestamptz,
    v4_retired_at timestamptz,
    updated_at timestamptz NOT NULL,
    CONSTRAINT record_membership_capability_rollout_state_check CHECK ((
        (state = 'off'
            AND generation = 0
            AND minimum_writer_version = 0
            AND cursor_issue_version = 4
            AND cutover_at IS NULL
            AND cutover_collection_count IS NULL
            AND cutover_record_count IS NULL
            AND ready_at IS NULL
            AND ready_collection_count IS NULL
            AND v5_enabled_at IS NULL
            AND v4_decode_until IS NULL
            AND v4_retired_at IS NULL)
        OR
        (state = 'cutover'
            AND generation >= 1
            AND minimum_writer_version = 1
            AND cursor_issue_version = 4
            AND cutover_at IS NOT NULL
            AND cutover_collection_count IS NULL
            AND cutover_record_count IS NULL
            AND ready_at IS NULL
            AND ready_collection_count IS NULL
            AND v5_enabled_at IS NULL
            AND v4_decode_until IS NULL
            AND v4_retired_at IS NULL)
        OR
        (state = 'dual_write'
            AND generation >= 1
            AND minimum_writer_version = 1
            AND cursor_issue_version = 4
            AND cutover_at IS NOT NULL
            AND cutover_collection_count IS NOT NULL
            AND cutover_record_count IS NOT NULL
            AND ready_at IS NULL
            AND ready_collection_count IS NULL
            AND v5_enabled_at IS NULL
            AND v4_decode_until IS NULL
            AND v4_retired_at IS NULL)
        OR
        (state = 'ready'
            AND generation >= 1
            AND cutover_at IS NOT NULL
            AND cutover_collection_count IS NOT NULL
            AND cutover_record_count IS NOT NULL
            AND ready_at IS NOT NULL
            AND ready_collection_count IS NOT NULL
            AND ((cursor_issue_version = 4
                    AND minimum_writer_version = 1
                    AND v5_enabled_at IS NULL
                    AND v4_decode_until IS NULL
                    AND v4_retired_at IS NULL)
                OR
                (cursor_issue_version = 5
                    AND minimum_writer_version = 2
                    AND v5_enabled_at IS NOT NULL
                    AND v4_decode_until IS NOT NULL)))
    ) IS TRUE),
    CONSTRAINT record_membership_capability_timeline_check CHECK ((
        (ready_at IS NULL OR (cutover_at IS NOT NULL AND ready_at >= cutover_at))
        AND
        (v5_enabled_at IS NULL
            OR (ready_at IS NOT NULL AND v5_enabled_at >= ready_at))
        AND
        (cursor_issue_version = 4 OR v5_enabled_at IS NOT NULL)
        AND
        (v4_decode_until IS NULL
            OR (v5_enabled_at IS NOT NULL
                AND v4_decode_until >= v5_enabled_at))
        AND
        (v4_retired_at IS NULL
            OR (v4_decode_until IS NOT NULL
                AND v4_retired_at >= v4_decode_until))
        AND
        (cutover_at IS NULL OR updated_at >= cutover_at)
        AND
        (ready_at IS NULL OR updated_at >= ready_at)
        AND
        (v5_enabled_at IS NULL OR updated_at >= v5_enabled_at)
        AND
        (v4_retired_at IS NULL OR updated_at >= v4_retired_at)
    ) IS TRUE)
);

INSERT INTO pgbase_meta.record_membership_capability (
    singleton,
    state,
    generation,
    minimum_writer_version,
    cursor_issue_version,
    updated_at
)
VALUES (true, 'off', 0, 0, 4, transaction_timestamp());

CREATE TABLE pgbase_meta.collection_creation_counters (
    collection_id uuid PRIMARY KEY
        REFERENCES pgbase_meta.collections(id) ON DELETE CASCADE,
    generation bigint NOT NULL CHECK (generation >= 0),
    last_ordinal bigint NOT NULL CHECK (last_ordinal >= 0),
    updated_at timestamptz NOT NULL,
    CONSTRAINT collection_creation_counters_generation_unique
        UNIQUE (collection_id, generation)
);

CREATE TABLE pgbase_meta.record_creation_ordinals (
    collection_id uuid NOT NULL,
    record_id uuid NOT NULL,
    ordinal bigint NOT NULL CHECK (ordinal >= 1),
    PRIMARY KEY (collection_id, record_id),
    CONSTRAINT record_creation_ordinals_counter_fk
        FOREIGN KEY (collection_id)
        REFERENCES pgbase_meta.collection_creation_counters(collection_id)
        ON DELETE CASCADE,
    CONSTRAINT record_creation_ordinals_collection_ordinal_unique
        UNIQUE (collection_id, ordinal)
);

CREATE TABLE pgbase_meta.record_membership_backfills (
    collection_id uuid PRIMARY KEY,
    generation bigint NOT NULL CHECK (generation >= 0),
    state text NOT NULL
        CHECK (state IN ('off', 'backfilling', 'ready')),
    cutover_count bigint CHECK (cutover_count >= 0),
    cutover_last_created_at timestamptz,
    cutover_last_record_id uuid,
    checkpoint_created_at timestamptz,
    checkpoint_record_id uuid,
    last_assigned_ordinal bigint NOT NULL DEFAULT 0
        CHECK (last_assigned_ordinal >= 0),
    validated_base_count bigint CHECK (validated_base_count >= 0),
    validated_sidecar_count bigint CHECK (validated_sidecar_count >= 0),
    updated_at timestamptz NOT NULL,
    completed_at timestamptz,
    CONSTRAINT record_membership_backfills_counter_fk
        FOREIGN KEY (collection_id, generation)
        REFERENCES pgbase_meta.collection_creation_counters(collection_id, generation)
        ON DELETE CASCADE
        DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT record_membership_backfills_cutover_bound_check CHECK ((
        (cutover_last_created_at IS NULL AND cutover_last_record_id IS NULL)
        OR
        (cutover_last_created_at IS NOT NULL AND cutover_last_record_id IS NOT NULL)
    ) IS TRUE),
    CONSTRAINT record_membership_backfills_checkpoint_check CHECK ((
        (checkpoint_created_at IS NULL AND checkpoint_record_id IS NULL)
        OR
        (checkpoint_created_at IS NOT NULL AND checkpoint_record_id IS NOT NULL)
    ) IS TRUE),
    CONSTRAINT record_membership_backfills_checkpoint_bound_check CHECK ((
        checkpoint_created_at IS NULL
        OR
        (cutover_last_created_at IS NOT NULL
            AND cutover_last_record_id IS NOT NULL
            AND (checkpoint_created_at, checkpoint_record_id)
                <= (cutover_last_created_at, cutover_last_record_id))
    ) IS TRUE),
    CONSTRAINT record_membership_backfills_validation_counts_check CHECK ((
        (validated_base_count IS NULL AND validated_sidecar_count IS NULL)
        OR
        (validated_base_count IS NOT NULL AND validated_sidecar_count IS NOT NULL)
    ) IS TRUE),
    CONSTRAINT record_membership_backfills_state_machine_check CHECK ((
        (state = 'off'
            AND generation = 0
            AND cutover_count IS NULL
            AND cutover_last_created_at IS NULL
            AND cutover_last_record_id IS NULL
            AND checkpoint_created_at IS NULL
            AND checkpoint_record_id IS NULL
            AND last_assigned_ordinal = 0
            AND validated_base_count IS NULL
            AND validated_sidecar_count IS NULL
            AND completed_at IS NULL)
        OR
        (state = 'backfilling'
            AND generation >= 1
            AND cutover_count IS NOT NULL
            AND last_assigned_ordinal <= cutover_count
            AND ((cutover_count = 0
                    AND cutover_last_created_at IS NULL
                    AND cutover_last_record_id IS NULL)
                OR
                (cutover_count > 0
                    AND cutover_last_created_at IS NOT NULL
                    AND cutover_last_record_id IS NOT NULL))
            AND validated_base_count IS NULL
            AND validated_sidecar_count IS NULL
            AND completed_at IS NULL)
        OR
        (state = 'ready'
            AND generation >= 1
            AND cutover_count IS NOT NULL
            AND last_assigned_ordinal <= cutover_count
            AND ((cutover_count = 0
                    AND cutover_last_created_at IS NULL
                    AND cutover_last_record_id IS NULL)
                OR
                (cutover_count > 0
                    AND cutover_last_created_at IS NOT NULL
                    AND cutover_last_record_id IS NOT NULL))
            AND validated_base_count IS NOT NULL
            AND validated_sidecar_count = validated_base_count
            AND completed_at = updated_at)
    ) IS TRUE)
);

CREATE INDEX record_membership_backfills_work_idx
    ON pgbase_meta.record_membership_backfills (generation, collection_id)
    WHERE state = 'backfilling';

CREATE TABLE pgbase_meta.runtime_writer_leases (
    instance_id uuid PRIMARY KEY,
    writer_version smallint NOT NULL CHECK (writer_version BETWEEN 0 AND 3),
    started_at timestamptz NOT NULL,
    heartbeat_at timestamptz NOT NULL,
    lease_expires_at timestamptz NOT NULL,
    CONSTRAINT runtime_writer_leases_timeline_check CHECK ((
        heartbeat_at >= started_at
        AND lease_expires_at >= started_at
        AND lease_expires_at <= heartbeat_at + interval '2 minutes'
    ) IS TRUE)
);

CREATE INDEX runtime_writer_leases_expiry_idx
    ON pgbase_meta.runtime_writer_leases (lease_expires_at, writer_version);

-- Seed only bounded catalog metadata. Record ordinals are populated later by
-- the resumable runtime backfill after the explicit cutover fence is enabled.
INSERT INTO pgbase_meta.collection_creation_counters (
    collection_id,
    generation,
    last_ordinal,
    updated_at
)
SELECT id, 0, 0, transaction_timestamp()
FROM pgbase_meta.collections
ORDER BY id;

INSERT INTO pgbase_meta.record_membership_backfills (
    collection_id,
    generation,
    state,
    updated_at
)
SELECT id, 0, 'off', transaction_timestamp()
FROM pgbase_meta.collections
ORDER BY id;
