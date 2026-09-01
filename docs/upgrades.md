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

## One-migration compatibility rollout

When release notes declare one additive migration as optional, use the release's preparatory artifact and the `P0 -> M -> F` sequence below. This exception supports only the two checksum-pinned histories named by that release: the history immediately before the migration and the history immediately after it. It does not make arbitrary old and new binaries compatible.

1. **P0 (prepare):** keep `PGBASE_AUTO_MIGRATE=false`, verify the immutable P0 image digest, and deploy P0 to every instance with the new feature disabled. P0 must be ready on both declared histories. Drain the previous binary and all of its in-flight schema transactions before continuing.
2. **M (migrate):** keep application auto-migration disabled and run one designated job from the same P0 artifact:

   ```console
   pgbase migrate up --through TARGET_VERSION > migration-receipt.json
   ```

   Substitute the exact numeric version and checksum published with the release. The command accepts only the latest embedded migration, refuses to cross more than one pending migration, rechecks history while holding the migration lock, and writes its JSON receipt only after committed history and the schema catalog have been read back successfully. A retry at an already-applied target is safe and records the target as both the before and after checkpoint.
3. Validate and retain `migration-receipt.json` with the deployment record. Require `receiptVersion=1`, the published target version and checksum, `after.version` equal to the target, `after.checksum` equal to the target checksum, and `after.dirty=false`. The `before` checkpoint must be either the declared previous history or the same target history on a retry. Also retain the P0 image digest, job exit status, target database identity, and execution time; keep structured stderr logs separate from stdout so they do not corrupt the receipt.
4. **F (activate):** deploy the feature artifact that requires the post-migration history. Enable the capability or feature only after every instance is on the approved artifact and all release-specific writer fences and drain hold points have passed.

The exact-target command is a rollout guard, not a general migration selector. Do not substitute the legacy `pgbase migrate up` command in a release procedure that specifies `--through`.

### Schema-operation ledger v6 preparation

The preparatory artifact in this source state recognizes exactly the following two clean histories while the schema-operation ledger feature remains disabled:

- previous version `5`, checksum `576df4a578efbd1259cf5dbf22da8d7669bd729e33d2ef1d1b4ed30e1d16197a`;
- optional target version `6`, checksum `6c3f728926d04b9bbeb81152df6eede97de155ddd2bb503a43faba4af0d6ec7a`.

After the full P0 fleet and in-flight schema writers are drained, the designated migration job is:

```console
pgbase migrate up --through 6 > migration-receipt.json
```

Do not activate ledger-backed schema writes merely because v6 is present. That requires the later feature artifact to make v6 mandatory and to pass its own fleet and writer-drain gate.

## Rollback actions are different operations

| Action | Before M commits | After M commits |
| --- | --- | --- |
| Code revert | Revert or discard the unshipped code stack and rebuild through the normal gate. | Do not recreate a pre-P0 artifact. A source revert is allowed only if its resulting artifact still recognizes the migrated checksum and keeps the feature off. |
| Deployment rollback | Redeploy the previously verified artifact while the database is still on its pre-migration history. | Redeploy the exact verified P0 artifact, with the feature disabled. Never deploy a pre-P0 binary that treats the new migration as unknown. Forward-fix from P0. |
| Database migration rollback | No migration has committed, so no database rollback exists. | There is no down migration and the added objects remain. Do not delete history rows, drop objects, or run reverse SQL. If forward recovery is impossible, stop every writer and restore the verified whole-database backup or PITR point so `pgbase_meta`, `app`, and `pgbase_relation` return to one consistent instant. |

After a feature-specific cutover, use only the newer rollback target named by that release. The generic P0 target is no longer sufficient unless the release procedure explicitly says otherwise.

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
