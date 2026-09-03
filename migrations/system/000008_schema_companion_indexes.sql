-- Serialize the additive metadata install with collection DDL. Dynamic record
-- tables are deliberately not inspected or changed here: the companion indexes
-- this capability gates are built by resumable CREATE INDEX CONCURRENTLY
-- maintenance, which PostgreSQL refuses to run inside a transaction block.
SELECT version
FROM pgbase_meta.schema_state
WHERE singleton
FOR UPDATE;

-- The capability row is the only source of truth for companion-index rollout.
-- State alone decides both halves of the gate: 'ready' means the schema writer
-- may issue the v2 plan format and every consumer may treat a companion as a
-- required physical contract; any other state means neither. There is
-- deliberately no separate compiler-version column, because a second value that
-- could disagree with state is a stranding bug waiting to happen.
--
-- Adding a nullable indexed field to an existing collection cycles the state
-- ready -> pending -> building -> ready, so 'pending' must stay legal after a
-- previous rollout completed. Only per-state invariants are constrained here;
-- nothing asserts that pending is a first-ever state.
CREATE TABLE pgbase_meta.schema_companion_index_capability (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    state text NOT NULL CHECK (state IN ('pending', 'building', 'ready')),
    generation bigint NOT NULL CHECK (generation >= 0),
    catalog_version bigint CHECK (catalog_version >= 0),
    building_at timestamptz,
    ready_at timestamptz,
    ready_index_count bigint CHECK (ready_index_count >= 0),
    updated_at timestamptz NOT NULL,
    CONSTRAINT schema_companion_index_capability_rollout_state_check CHECK ((
        (state = 'pending'
            AND catalog_version IS NULL
            AND building_at IS NULL
            AND ready_at IS NULL
            AND ready_index_count IS NULL)
        OR
        (state = 'building'
            AND generation >= 1
            AND catalog_version IS NOT NULL
            AND building_at IS NOT NULL
            AND ready_at IS NULL
            AND ready_index_count IS NULL)
        OR
        (state = 'ready'
            AND generation >= 1
            AND catalog_version IS NOT NULL
            AND building_at IS NOT NULL
            AND ready_at IS NOT NULL
            AND ready_index_count IS NOT NULL)
    ) IS TRUE),
    CONSTRAINT schema_companion_index_capability_timeline_check CHECK ((
        (building_at IS NULL OR updated_at >= building_at)
        AND
        (ready_at IS NULL OR updated_at >= ready_at)
        AND
        (ready_at IS NULL OR (building_at IS NOT NULL AND ready_at >= building_at))
    ) IS TRUE)
);

INSERT INTO pgbase_meta.schema_companion_index_capability (
    singleton,
    state,
    generation,
    updated_at
)
VALUES (true, 'pending', 0, transaction_timestamp());

-- A separate durable lease for the schema writer. The record writer lease in
-- migration 7 proves which record-mutation generation is live and says nothing
-- about which plan compiler a process would issue, so reusing it would let an
-- old schema writer survive an activation that assumes the new plan format.
-- An external drain is a procedure, not a proof; this table is the proof.
--
-- compiler_version has no upper bound on purpose: a bounded CHECK would force a
-- later migration to rewrite this constraint just to name a newer compiler.
CREATE TABLE pgbase_meta.schema_writer_leases (
    instance_id uuid PRIMARY KEY,
    compiler_version smallint NOT NULL CHECK (compiler_version >= 1),
    started_at timestamptz NOT NULL,
    heartbeat_at timestamptz NOT NULL,
    lease_expires_at timestamptz NOT NULL,
    CONSTRAINT schema_writer_leases_timeline_check CHECK ((
        heartbeat_at >= started_at
        AND lease_expires_at >= started_at
        AND lease_expires_at <= heartbeat_at + interval '2 minutes'
    ) IS TRUE)
);

CREATE INDEX schema_writer_leases_expiry_idx
    ON pgbase_meta.schema_writer_leases (lease_expires_at, compiler_version);
