# Upgrades

pgBase is pre-v0.1. v0.x binaries, HTTP contracts, environment variables, system schemas, and backup procedures may change without backwards compatibility. Read release-specific notes and rehearse every upgrade on a restored copy of production data.

## Upgrade procedure

1. Confirm the target binary/image digest and the exact configuration changes it requires.
2. Create and verify a backup of both `pgbase_meta` and `app`; see [Backup and restore](backup-restore.md).
3. Restore that backup in staging and run the target release's diagnostics and application checks.
4. Pause administrator collection/schema changes for the production project.
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

7. Roll out `pgbase serve` instances. For each instance, require `GET /health/ready` to return HTTP 200 before admitting traffic. Multi-instance deployments must keep the same token secret and origin/proxy configuration.
8. Run `pgbase doctor --json` against the upgraded database and require `"ok":true`, then perform representative administrator and record operations.
9. Resume schema changes only after the rollout is accepted.

## Zero-downtime and rollback limitations

Do not assume a rolling upgrade is zero downtime. pgBase does not provide online DDL, down migrations, arbitrary old/new binary coexistence guarantees, replica routing, or automatic traffic orchestration. Collection DDL can take PostgreSQL locks, and a target system migration may not be compatible with the old binary. Use a stop-migrate-start maintenance window unless release-specific evidence establishes safe coexistence.

There is no `pgbase migrate down`. Do not manually delete migration rows or reverse SQL in production. If an upgrade cannot be rolled forward safely, stop writers and restore the verified pre-upgrade backup/PITR point. Rolling a binary back is safe only when that exact binary has been verified against the already-upgraded schema; v0.x provides no general guarantee.
