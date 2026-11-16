# Schema 69 migration, recovery and rollback boundaries

Status: **IN_PROGRESS — current integrated CI pending**. This is a source-based
operator summary and a map to existing executable qualification. It records no
production execution. Follow the repository's [deployment](../../../deployment.md),
[upgrade](../../../upgrades.md), [backup/restore](../../../backup-restore.md) and
[Storage](../../../storage-settings.md) procedures with the selected artifact.

## What changes

The preserved main source embeds migrations through **67**. PR #148 adds
[68, Realtime pending queue](../../../../migrations/system/000068_realtime_pending_queue.sql)
and [69, durable variant cleanup](../../../../migrations/system/000069_durable_variant_cleanup.sql).
The final-master local corrections add no new migration, rename no public API
and retain schema **69**.

Migration 68 adds immutable pending-delivery ordering identity and transactional
queue admission, including an old-writer-compatible insertion trigger/backfill.
Dead predecessors intentionally retain ordering pressure. Migration 69 adds
retention/retry metadata and a due index to object-deletion intents, plus a
trigger preventing older workers from silently deleting unresolved uncertainty
tombstones. Those limited compatibility defenses do not establish arbitrary
mixed-version fleet compatibility or permission to downgrade.

## Supported deployment sequence

1. Select and record the exact reviewed source/artifact; verify backup and
   capacity in isolated staging before touching a production project. Preserve
   all three schemas (`pgbase_meta`, `app`, `pgbase_relation`), file bytes and
   separately protected configuration/secrets needed for recovery.
2. Stop admitting new writes and drain old application, job, schema and Storage
   workers. Upgrade or stop every old cleanup worker. For a namespace cutover,
   also establish actual remote Put completion; process exit or cancellation
   alone is not sufficient.
3. Keep `PGBASE_AUTO_MIGRATE=false`. Against the nominated target, run the target
   release's `pgbase migrate status`, then one nominated `pgbase migrate up`,
   then `pgbase migrate status` again. Require `current=latest=69` and
   `dirty=false`. Do not use the old optional v6/v7 compatibility procedure as
   evidence that a new 67/68/69 mixed rollout is supported.
4. Start only the chosen schema-69-capable instances, retaining the same token
   secret, origin/proxy policy and project namespace configuration. Require
   `pgbase doctor --json` to report `ok:true` and `/health/ready` to return 200
   before admitting traffic.
5. Exercise API-level owner-isolated create/read/update/delete, conditional
   ETags, files/variants, relevant relation operations, Realtime and Jobs. Inspect
   retained-intent and migration state; preserve diagnostic evidence. Resume
   normal writes only after the checks are accepted.

This is the ordinary explicit stop/drain/migrate/start sequence. It does not
apply migrations from this review session or establish production approval.

## Backup and restore

Existing integration contracts include fresh install/idempotence,
`TestVariantCleanupUpgradePreservesQueueAndReapplies`, and
`TestVariantCleanupRestoresSchema68BeforeUpgrade`. The last creates an actual
schema-68 dump, restores to a new database, detaches inherited Storage before
migration, preserves bytes, and upgrades/reapplies schema 69. Separate S3
snapshot contracts restore to independent local files. Source-bound coverage is
explained in the [Storage CI report](../phase-nonperformance-storage-ci.md);
the later [historical full qualification](../final-integrated-qualification.md)
also passed the configured PostgreSQL 16.13/17.9/18.3 matrix. Those are old-source
results; the current integrated matrix remains a mandatory final gate.

Restore to an empty, isolated target with a PostgreSQL client matching the
server major and restore all three schemas coherently. Do not restore over a
running project or combine schemas from different recovery points. Managed
restore detaches source pending intents and credentials before applying the
restored application's migrations, leaving the source namespace unchanged.
Schema-68 backups lack the retention column; schema-69 backups can contain
retained intents. Neither case justifies deleting the source's cleanup queue.

Validate actual record/file bytes, ETags, ownership/authorization, relation
invariants, settings, readiness and expected session/token behavior after
restore. A logical DB dump alone does not establish object-store recovery.
Restoring an earlier backup can lose accepted writes since that point; that
recovery-point consequence must be an operator decision.

## Rollback matrix

| Situation | Permitted recovery within this task / documented boundary |
| --- | --- |
| Current Git main | **NO_MAIN_ROLLBACK_NEEDED**. main is unchanged and is the PR merge-base. Do not revert unrelated previously merged work. |
| New local final-master code regression before merge | Reverse the isolated correction on the PR/work branch and requalify affected behavior. No new DB schema accompanies these corrections. |
| Target changes discovered already in main | Reconcile exact ancestry/patch/data effects first. Prefer removing duplicate PR diff. Direct main reset/revert/force push is outside this task; necessary cross-branch inverse work is `MAIN_REVERT_REQUIRES_APPROVAL`. |
| Deployment failure before migrations commit | Keep writers stopped, inspect migration/dirty state, and return to the verified prior artifact only when its unchanged schema/data contract is still valid. |
| Schema 68/69 committed | Do not drop new metadata/triggers or run an arbitrary old binary. Preserve queue/intents and forward-fix. A binary rollback requires an independently verified compatibility contract; none is newly established here. |
| Recovery to an older backup | Restore coherently into a separate database/namespace, verify the matching artifact and restore contract, then plan cutover. This is data recovery with an explicit recovery point, not a code-only downgrade. |
| Uncertain Storage intents block migration | Keep source active and retain intents. Prove completion for exact keys before clearing retention under the existing maintenance procedure; leave rows for current cleanup/reference checks. |

History rewrite, main mutation, releases and deployment are not performed by
this final-master round. Git reversibility does not imply data reversibility.

## Database connection budget

At pool4, the historical diagnostic fixture's persistent baseline is
**4 pooled request/background sessions + 1 dedicated schema listener + 1 backup
writer-gate session + 1 optional diagnostic observer = 7**. Without the observer
it is six, before additional activity. Listener reconnect briefly acquires a
pool slot before detaching its initialized connection.

This is **not** a total `max_connections` cap. The actual
[Storage admission code](../../../../internal/filestore/storage_settings.go)
allows **64 concurrent Storage operations per controller**, each opening an
outside-pool coordination session while its lease is active. The
[pool contract](../../../../internal/database/pool.go) explicitly separates
those sessions from request capacity. Nested operations reusing the same owned
lease do not imply a second lease; a foreign lease must not substitute another
controller's session after the final-master fix.

For one controller, the illustrative client budget can therefore reach the
seven-session diagnostic baseline **plus up to 64 active Storage-operation
sessions**, before other migration, backup, monitoring, operator and multi-
instance headroom. This is a source-derived capacity planning bound for those
paths, not an observed simultaneous-71-session benchmark or a universal cluster
maximum. Multiply per-instance/controller usage according to the actual fleet;
include background server workers separately from client-session counts.

No connection pool setting, durability option or acceptance threshold was
weakened to obtain the earlier performance result. Final qualification must
retain the fixed four-request-pool workload and record any diagnostic overhead.
