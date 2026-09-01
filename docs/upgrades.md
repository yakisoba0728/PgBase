# Upgrades

pgBase is pre-v0.1. v0.x binaries, HTTP contracts, environment variables, system schemas, and backup procedures may change without backwards compatibility. Read release-specific notes and rehearse every upgrade on a restored copy of production data.

## Upgrade procedure

1. Confirm the target binary/image digest and the exact configuration changes it requires.
2. Create and verify one consistent backup of `pgbase_meta`, `app`, and `pgbase_relation`; see [Backup and restore](backup-restore.md).
3. Restore that backup in staging and run the target release's diagnostics and application checks.
4. Pause administrator collection/schema changes and drain every old record writer for the production project.
   This is mandatory for both the record-operations v4 boundary and the relation-schema v5 boundary. An old binary can mutate `app.*` without the current revision, audit, outbox, relation-storage, lock-order, and logical delete-action invariants. Migration v5 adds relation metadata plus the `pgbase_relation` edge/constraint schema; do not allow an old process to serve against it unless a release-specific compatibility test explicitly permits that combination.
5. With the currently deployed release, run:

   ```console
   pgbase migrate status
   ```

   Resolve any dirty, unavailable, or unexpected state before proceeding.

6. With the target release and `PGBASE_AUTO_MIGRATE=false`, run:

   ```console
   pgbase migrate status
   pgbase migrate up
   pgbase migrate status
   ```

   Require `current=latest` and `dirty=false`. Run migration as one nominated job even though the database advisory lock serializes concurrent attempts.

7. Roll out only target-version `pgbase serve` instances. For each instance, require `GET /health/ready` to return HTTP 200 before admitting traffic. Multi-instance deployments must keep the same token secret and origin/proxy configuration.
8. Run `pgbase doctor --json` against the upgraded database and require `"ok":true`. Doctor verifies relation metadata and the expected single-relation columns/FKs/indexes plus multiple-relation tables, constraints, functions, triggers, and indexes without exposing their physical names in client-facing findings.
9. Through the administrator-authenticated HTTP record API, perform representative scalar and relation operations: create/read an ordered multiple relation, read an expanded single relation, and execute the configured `restrict`, `cascade`, or `set_null` path applicable to the project. Use `Idempotency-Key` and `If-Match`; do not validate the upgrade by writing pgBase-owned tables directly.
10. Resume schema changes only after the rollout is accepted.

## Legacy catalog reduction preflight

The candidate binary refuses to start when the authenticated canonical schema catalog is larger than 16 MiB. `pgbase doctor` reports this as `catalog_limit_exceeded`, and `pgbase migrate up` runs the same check before and after migrations so the rollout cannot continue silently.

If either command reports that finding:

1. Keep application traffic and every schema writer stopped.
2. Use the previous binary only if it is verified compatible with the database's current system schema. During the maintenance window, use its administrator-authenticated schema API to remove unused fields or collections until the catalog is smaller. If no compatible binary is available, restore the verified pre-upgrade backup, perform the same reduction there, and retry the upgrade.
3. Do not update or delete rows in `pgbase_meta` directly. Raw metadata changes can separate the catalog from `app` or `pgbase_relation` physical objects.
4. With the candidate binary, rerun:

   ```console
   pgbase doctor --json
   pgbase migrate up
   pgbase doctor --json
   ```

   Require both doctor runs to report `"ok":true` and migration status to report `current=latest dirty=false`.
5. Start the candidate binary and admit traffic only after `GET /health/ready` returns HTTP 200.

## Zero-downtime and rollback limitations

Do not assume a rolling upgrade is zero downtime. pgBase does not provide online DDL, down migrations, arbitrary old/new binary coexistence guarantees, replica routing, or automatic traffic orchestration. Collection DDL can take PostgreSQL locks, and a target system migration may not be compatible with the old binary. Use a stop-migrate-start maintenance window unless release-specific evidence establishes safe coexistence.

There is no `pgbase migrate down`. Do not manually delete migration rows, relation metadata, edge tables, or reverse SQL in production. If an upgrade cannot be rolled forward safely, stop writers and restore the verified pre-upgrade whole-database backup/PITR point so all three schemas return to the same instant. Rolling a binary back is safe only when that exact binary has been verified against the already-upgraded schema; v0.x provides no general guarantee.
