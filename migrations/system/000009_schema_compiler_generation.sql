-- Drain any pre-P0 CREATE INDEX CONCURRENTLY maintenance before changing its
-- durable protocol. The old command takes this as a session lock before it
-- inspects or changes a companion index. Taking the transaction-scoped form of
-- the same lock makes migration 9 wait for that work to finish and prevents a
-- new old-generation build from entering until this transaction commits. This
-- must be the first product lock: old maintenance releases this lock before its
-- later promotion takes the schema lock, so the ordering cannot form a cycle.
SELECT pg_advisory_xact_lock(8099550625536475144);

-- Drain any schema writer or rollout transition that entered before M. Taking
-- the same advisory lock used by Apply and capability transitions before the
-- schema-state row and lease-table locks preserves their lock order and avoids
-- a schema-state/lease cycle with an in-flight writer.
SELECT pg_advisory_xact_lock(8099550625536475138);

-- Serialize with collection DDL, as migration 8 does. Once both drains above
-- have completed, nothing here inspects or changes a dynamic record table; the
-- companion indexes stay exactly where the previous rollout left them.
SELECT version
FROM pgbase_meta.schema_state
WHERE singleton
FOR UPDATE;

-- A generation number alone cannot fence an already-running pre-P0 binary: its
-- old lease upsert omits writer_protocol. Preserve existing leases while an
-- exclusive table lock excludes those upserts, then require an explicit protocol
-- token with no default. Their original expiry keeps every live generation-2
-- writer visible to activation. PostgreSQL validates the proposed INSERT before
-- taking ON CONFLICT's update path, so an old process that wakes after M receives
-- a NOT NULL violation inside its schema-write transaction before DDL can commit.
LOCK TABLE pgbase_meta.schema_writer_leases IN ACCESS EXCLUSIVE MODE;
ALTER TABLE pgbase_meta.schema_writer_leases
	ADD COLUMN writer_protocol smallint;
UPDATE pgbase_meta.schema_writer_leases SET writer_protocol = 1;
ALTER TABLE pgbase_meta.schema_writer_leases
	ALTER COLUMN writer_protocol SET NOT NULL,
	ADD CONSTRAINT schema_writer_leases_protocol_check
		CHECK (writer_protocol = 1);

-- Which compiler generation proved this rollout, recorded at the moment the
-- fleet was proved.
--
-- Migration 8 argued against exactly this column: "a second value that could
-- disagree with state is a stranding bug waiting to happen". The argument was
-- sound and the premise was wrong. It assumed the schema writer's declared
-- compiler_version identified a binary, so that verifying every live lease at or
-- above the writer's own generation proved a converged fleet. It did not: the
-- binaries that shipped before the companion contract already declared
-- compiler_version 2 while issuing v1 plans only, and the binary that issues v2
-- declared the same 2. A lease at 2 therefore names two different compilers, the
-- activation check could not tell a mixed fleet from a converged one, and every
-- 'ready' reached under it rests on a proof that proved nothing.
--
-- So the fix has two halves and needs both. The writer generation advances to 3,
-- which makes the declared value name a binary again -- but that alone would let
-- a row already in 'ready' keep issuing v2 forever without ever being re-proved,
-- because state carries across the upgrade and nothing in it records which
-- generation the proof was against. Recording the proved generation beside the
-- state is what makes the gate re-checkable. It cannot disagree with state: only
-- opening a rollout writes it, and every path to 'pending' clears it, which is
-- what the constraint below pins.
ALTER TABLE pgbase_meta.schema_companion_index_capability
    ADD COLUMN proven_compiler_version smallint;

-- Demote whatever this database was left in, before the constraint exists to
-- describe it.
--
-- A 'ready' row here was promoted by a binary that could not distinguish itself
-- from a v1-only writer, so nothing about its activation proof stands: the fleet
-- it verified may have contained writers still issuing v1 plans, which is the
-- single condition the proof existed to exclude. The row cannot be repaired by
-- backfilling a generation, because there is no honest value to backfill -- the
-- proof did not observe one. A 'building' row was opened by the same check and
-- is demoted for the same reason.
--
-- The companion indexes already built are deliberately kept. They are correct
-- physical objects; only the claim that the fleet is converged is void. A
-- re-activation re-proves the fleet and the following build finds them valid and
-- promotes without rebuilding, so the cost is an operator round, not an index
-- rebuild. Until that round completes every binary, old and new, issues v1 --
-- which is the safety property, not a regression.
UPDATE pgbase_meta.schema_companion_index_capability
SET state = 'pending',
    catalog_version = NULL,
    building_at = NULL,
    ready_at = NULL,
    ready_index_count = NULL,
    proven_compiler_version = NULL,
    updated_at = transaction_timestamp()
WHERE singleton AND state <> 'pending';

-- The v8 storage labels are also an executable protocol understood by the
-- predecessor's maintenance command. If v9 kept "building", that command could
-- run its older physical checker after a generation-3 activation and then write
-- "ready" without naming the compiler generation at all. Give the v9 protocol
-- distinct non-pending labels. Old activation/promotion SQL then violates the
-- database constraint, and an old build rejects building_v3 before touching an
-- index. The Go API maps these storage labels back to the stable logical states.
ALTER TABLE pgbase_meta.schema_companion_index_capability
    DROP CONSTRAINT schema_companion_index_capability_state_check,
    DROP CONSTRAINT schema_companion_index_capability_rollout_state_check,
    ADD CONSTRAINT schema_companion_index_capability_state_check
        CHECK (state IN ('pending', 'building_v3', 'ready_v3')),
    ADD CONSTRAINT schema_companion_index_capability_rollout_state_check CHECK ((
        (state = 'pending'
            AND catalog_version IS NULL
            AND building_at IS NULL
            AND ready_at IS NULL
            AND ready_index_count IS NULL)
        OR
        (state = 'building_v3'
            AND generation >= 1
            AND catalog_version IS NOT NULL
            AND building_at IS NOT NULL
            AND ready_at IS NULL
            AND ready_index_count IS NULL)
        OR
        (state = 'ready_v3'
            AND generation >= 1
            AND catalog_version IS NOT NULL
            AND building_at IS NOT NULL
            AND ready_at IS NOT NULL
            AND ready_index_count IS NOT NULL)
    ) IS TRUE);

-- The lower bound has no upper companion for the reason v8 gave for
-- compiler_version: naming a newest generation here would force every later
-- migration to rewrite this line.
ALTER TABLE pgbase_meta.schema_companion_index_capability
    ADD CONSTRAINT schema_companion_index_capability_proven_generation_check CHECK ((
        (state = 'pending') = (proven_compiler_version IS NULL)
        AND
        (proven_compiler_version IS NULL OR proven_compiler_version >= 1)
    ) IS TRUE);
