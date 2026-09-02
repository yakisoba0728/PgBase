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

### Schema-operation ledger v6 feature release

The preparatory P0 artifact recognizes exactly the following two clean histories while the schema-operation ledger feature remains disabled:

- previous version `5`, checksum `576df4a578efbd1259cf5dbf22da8d7669bd729e33d2ef1d1b4ed30e1d16197a`;
- optional target version `6`, checksum `6c3f728926d04b9bbeb81152df6eede97de155ddd2bb503a43faba4af0d6ec7a`.

The feature artifact requires the second history exactly: clean version `6` with the published checksum. It rejects clean v5 and older histories at startup and readiness, and `pgbase doctor --json` reports `migration_not_current` on those histories. Setting application auto-migration does not cross this boundary: it may bootstrap an empty database, but it rejects any nonempty pre-v6 history without changing that history. A checksum mismatch, gap, duplicate, or unknown version remains a distinct dirty-history failure.

Use this exact v5-to-v6 sequence:

1. Deploy the verified P0 artifact with the feature disabled to the full fleet. Require every P0 instance to be ready on clean v5, then drain the previous binary and every in-flight schema writer.
2. Keep application auto-migration disabled. From one designated job using that same P0 artifact, run:

   ```console
   pgbase migrate up --through 6 > migration-receipt.json
   ```

3. Require the receipt to contain `receiptVersion=1`, `target.version=6`, `target.checksum=6c3f728926d04b9bbeb81152df6eede97de155ddd2bb503a43faba4af0d6ec7a`, `before.version=5`, `before.checksum=576df4a578efbd1259cf5dbf22da8d7669bd729e33d2ef1d1b4ed30e1d16197a`, `after.version=6`, `after.checksum=6c3f728926d04b9bbeb81152df6eede97de155ddd2bb503a43faba4af0d6ec7a`, and `after.dirty=false`. An exact retry may report version 6 in both checkpoints.
4. Before deploying the feature artifact, use the P0 artifact to rerun `pgbase migrate status` and require `current=latest=6` and `dirty=false`. Retain the receipt and the P0 image digest with the deployment record.
5. Deploy the feature artifact only after the migration receipt and schema-writer drain are accepted. Require `pgbase migrate status`, `pgbase doctor --json`, and `GET /health/ready` to succeed on clean v6 before admitting schema traffic.

Do not deploy the feature artifact before v6 commits: unlike P0, it intentionally treats v5 as incompatible. After v6 commits, do not deploy a pre-P0 binary or reverse the migration; disable the feature and redeploy the exact verified P0 artifact if a deployment rollback is required, then forward-fix.

### Record membership v7 feature release

Record membership adds a per-collection creation ordinal so list pagination cannot silently omit a record. It ships as four artifacts — A0, A, B and C — and only A0 recognizes both histories.

- previous version `6`, checksum `6c3f728926d04b9bbeb81152df6eede97de155ddd2bb503a43faba4af0d6ec7a`;
- optional target version `7`, checksum `8c8f2a4b0506d4abaffc19097a090f84f424ef521ec9575ac4577ecaa1b3432e`.

Run the `P0 -> M -> F` sequence above with A0 as the preparatory artifact and `--through 7`, then continue with the release-specific hold points:

1. **Release A** admits only the post-migration history. Every record create takes the collection creation fence regardless of request headers or capability state, but the capability stays off and cursors stay v4.
2. **Cutover** is not a lease check. Drain every A0 instance and confirm zero in-flight pre-A transactions before enabling dual-write; the cutover snapshot and counters are initialized under the creation fence.
3. **Backfill** runs outside the migration transaction, is resumable, and records per-collection checkpoints. Promote to ready only after every catalog collection has a validated backfill row, including any collection created during the backfill window.
4. **Release B** enables v5 cursor issuance and the high-water read. Deploy it to every instance before enabling the capability, then drain the 15-minute cursor TTL before Release C.
5. **Retirement** is the point of no return. Confirm every active writer lease is at Release B or later, confirm the cursor TTL has drained, then retire cursor v4. Only after that is it safe to deploy Release C, which no longer carries a v4 cursor at all.

#### Driving the rollout from the operator CLI

Each step above is an operator command, not a deploy and not a configuration
value. The capability row is the only source of truth, so no environment
variable, HTTP route, or `pgbase.Options` field can move it:

```console
pgbase membership status --json
pgbase membership cutover --witness INSTANCE_ID:WRITER_VERSION [--witness ...]
pgbase membership backfill
pgbase membership promote-ready
pgbase membership enable-v5 --witness INSTANCE_ID:WRITER_VERSION [--witness ...]
pgbase membership retire-v4 --witness INSTANCE_ID:WRITER_VERSION [--witness ...]
```

Run every command as one designated job against the upgraded database, with the
fleet already deployed and serving: the transitions require live writer leases,
and only a running instance holds one.

1. `status` reports the capability state, generation, writer floor, cursor issue
   version, every rollout timestamp, and every live record writer in the exact
   `--witness INSTANCE_ID:WRITER_VERSION` spelling the next transition expects
   back. The writer version is the release generation — `0` for A0, `1` for A,
   `2` for B, `3` for C; a fleet of the current artifact reports `:3` for every
   instance. `status` never moves the capability; run it before and after every
   step and keep the JSON with the deployment record.
2. `cutover` starts the barrier and initializes the counters and per-collection
   backfill rows under the creation fence. It requires the durable lease set to
   be exactly the witnesses you name, each at Release A or later. You name the
   release as well as the instance; the command does not fill it in from the
   rows the transition is about to re-read, because a proof assembled that way
   would prove nothing about what you confirmed.
3. `backfill` runs the resumable backfill to completion, printing one line per
   pass and committing a checkpoint each time. It is safe to kill and safe to
   rerun; rerun it until it reports `done=true`. It validates each collection's
   base and sidecar counts before marking it ready, and it stops with a failure
   rather than looping if a pass can make no progress.
4. `promote-ready` refuses until every catalogued collection — including one
   created during the backfill window — has a validated, completed backfill row
   at the current generation.
5. `enable-v5` turns on v5 cursor issuance and the high-water read, raises the
   writer floor to Release B, and stamps the 15-minute v4 decode deadline. It
   takes the same fleet proof, and requires every witness to be at Release B or
   later.
6. `retire-v4` is irreversible. It refuses with a drain-pending failure until
   `v4_decode_until` has elapsed, and it re-verifies the fleet proof under its
   own locks. Do not attempt to shorten that window.

`pgbase membership disable-v5` is the rollback for step 5: it returns issuance
to v4 and the writer floor to Release A. It is available only while
`v4_retired_at` is null, so the window closes permanently at retirement. There
is no rollback for cutover or for promotion; both are forward-only, and both
are safe to rerun.

Every step reports the receipt it already stamped if it has already run, so a
retried job is not a second transition. A failed step exits 1 with a redacted
diagnostic on stderr and leaves the capability unchanged; an invocation the
command itself rejects exits 2 without connecting to the database.

Do not move the capability with SQL. The transitions take the schema pin, the
creation counters and the capability row in a fixed order and validate counts
before each promotion; an `UPDATE` that sets the row to `ready` skips all of it
and can leave records with no membership sidecar, which lists then silently omit.

#### Release B requires the exact v7 history on every instance

Unlike earlier releases, Release B validates the installation on **every list request**, not only at startup. A list fails closed with `record_unavailable` (HTTP 503) when the database is not on the exact history this binary was built for:

- **behind** — a clean v6 or older database. Release B is not compatible with it in either direction; use A0 for the migration window.
- **ahead** — the database carries a migration this binary does not know, which happens if a newer peer migrates first during a rolling deploy.

This is deliberate. The binary cannot verify checksums for a migration it does not embed, and a later migration may alter the membership objects the resolver depends on. The `P0 -> M -> F` sequence is the supported mitigation: the preparatory artifact for the *next* migration goes to the whole fleet before that migration is applied, so a Release B instance never meets a database ahead of it.

Never apply a migration while any instance predates the artifact that recognizes it. Writes and single-record reads keep working in this state, so the fleet does not look broken — only lists fail — and the operator-facing error is intentionally redacted. `pgbase migrate status` and `pgbase doctor --json` are the tools that name the actual history problem.

#### Release C requires a database that has retired cursor v4

Release C removes the v4 cursor entirely: it neither decodes one nor issues
one. That makes retirement its prerequisite rather than its consequence, and
the two have to happen in this order.

Retire first, with the fleet still on Release B:

1. Require every active row in `pgbase_meta.runtime_writer_leases` to be at
   Release B or later, and require the fleet proof you send to match that
   active lease set exactly. Retirement re-reads the lease set under its own
   locks and refuses a proof that does not match.
2. Require `v4_decode_until` to have elapsed. Retirement returns a drain-pending
   failure while any v4 cursor could still be live, and that deadline is
   15 minutes after v5 issuance was enabled.
3. Retire cursor v4. This is irreversible. It stamps `v4_retired_at`, and from
   that moment the rollback to v4 issuance is refused permanently, so
   `cursor_issue_version` can never leave 5 again.

Only then deploy Release C.

The order matters because the rollback to v4 issuance is still available until
retirement stamps the row. If Release C were deployed first, invoking that
documented rollback would set `cursor_issue_version` back to 4 under a fleet
with no v4 encoder, and no instance could issue a continuation for any page
that has more results. Retiring first closes that window before the binary
that depends on it arrives.

Retirement does **not** raise the minimum writer version, which stays at
Release B. That is deliberate and is what keeps Release B a valid rollback
target afterwards: a Release B instance can still take a writer lease against a
retired database, and it honours the retirement by refusing a v4 cursor even
though it still carries the decoder.

A Release C instance fails every list closed with `record_unavailable`
(HTTP 503) against a database that has not retired v4. This is the same
fail-closed shape Release B uses for the wrong history, and for the same
reason: there is no cursor the binary could mint for a page that has more
results, and serving a truncated first page with no continuation is the silent
omission this whole rollout exists to remove. Writes and single-record reads
keep working, so the fleet does not look broken; only lists fail.

A v4 cursor replayed against Release C returns the same `invalid_cursor`
(HTTP 400) a caller already handles for a tampered or expired token. There is
no distinct code for a retired format, and there is deliberately no way for a
caller to tell the two apart.

##### `GET /health/ready` stays 200 during the rollout

An instance whose lists all fail with `record_unavailable` still reports ready.
That is deliberate, and it is not a gap in the check.

Readiness answers whether this instance should be admitted to the load
balancer, and the honest answer during the rollout window is yes. Writes,
single-record reads, the administrator API and every schema route serve
normally; only list is fenced. Every instance in the fleet is in the same
state, so failing readiness would not route traffic to a healthy peer — it
would take the whole deployment out of service and replace "lists fail" with
"nothing answers".

It would also deadlock the rollout it is supposed to warn about. `cutover`,
`enable-v5` and `retire-v4` require a live writer lease from each instance, and
an orchestrator that refuses to admit an instance that never reports ready will
stall or roll back the very deploy that has to be in place before the
transitions can run. A deployment that is proceeding correctly through a
legitimate mid-rollout window must not be reported as failing.

The rollout state is therefore surfaced where an operator looks for it rather
than through readiness. `pgbase membership status --json` is that place, and it
is the only one: it names the capability state, the cursor issue version, every
rollout timestamp and the live fleet. `pgbase doctor --json` does **not** cover
this — it reports membership drift and an invalid membership installation, but a
database whose rollout simply has not been run is a clean doctor report. Treat a
fleet that is ready but cannot list as an unfinished rollout, and run `pgbase
membership status`.

After Release C the rollback target is the exact verified Release B artifact
with the capability left retired. Do not deploy Release A0 or Release A on a
retired database, and do not attempt to clear `v4_retired_at`.

#### A new install starts retired, not off

The steps above describe upgrading a deployment that already carries data. A
brand new install has nothing to migrate, and the rollout it would have to run
has no work in any of its steps, so Release C takes such a database straight to
the retired terminal state when it registers its writer. There is no operator
sequence to run and no command to invoke on a new install; lists work from the
first record.

The shortcut applies **only** where it is provably vacuous. It is refused, and
the full sequence above remains the only way forward, unless all of the
following hold in the same database transaction:

- the capability is still exactly the row migration 7 seeded — `off`,
  generation `0`, writer floor `0`, issuing v4, every rollout timestamp null;
- no collection holds a record, and `pgbase_meta.record_creation_ordinals` is
  empty. The test is records, not collections: an install whose administrator
  has already created collections still has nothing to backfill;
- no rollout has started — every creation counter is at generation `0` with
  ordinal `0`, and every backfill row is `off` at generation `0`;
- every catalogued collection has its table, and its counter and backfill rows;
- every active writer lease is at Release B or later. This is the same fleet
  condition retirement itself requires, because the terminal state raises the
  writer floor to Release B and a live older writer must not be promoted out of
  its own database.

The transaction takes the exclusive schema pin and the capability row lock
before it proves any of that, so a collection create or a record write cannot
commit between the proof and the promotion. Several replicas may start at once:
exactly one performs the promotion and the rest observe it as already done. A
database that has any rollout history or any data is left untouched — not an
error, so a mid-rollout fleet still starts — and its operator runs the real
sequence above.

The bootstrap is independent of `PGBASE_AUTO_MIGRATE`. It is a capability
transition on an already-migrated database, not a migration, and it runs where
the writer registers, which is after startup has already required a compatible
history. An operator who applies migrations by hand with auto-migration off
still ends up on a database whose seeded capability is `off`, and gets the same
result.

#### A base record without its membership sidecar disappears from lists

Once the capability is ready, a list applies the membership boundary, so a base record with no `record_creation_ordinals` row is not returned and **no error is raised**. The same record is still returned by `GET /api/v1/collections/{collection}/records/{id}`. A caller cannot tell the two cases apart.

A healthy deployment cannot reach this state: the base row and its sidecar are written in one transaction, delete and cascade hold the same order, and promotion validates the counts. Treat any report of a record that is visible by id but missing from its list as a membership drift symptom and run `pgbase doctor --json` first. Doctor reports the condition as `record_membership_sidecar_missing`, and reports the mirror case, a sidecar whose base record is gone, as `record_membership_sidecar_orphaned`. Both are bounded findings that name no identifiers.

Do not enable the capability on a database whose doctor report is not clean.


### Schema companion index v8 preparatory release

A descending page over a nullable column is ordered `NULLS LAST`, and no default
btree can serve that ordering in either scan direction. Migration 8 opens the
rollout that adds the companion indexes which can, and it adds **only** two
pieces of bounded metadata:

- `pgbase_meta.schema_companion_index_capability`, a single row seeded `pending`;
- `pgbase_meta.schema_writer_leases`, the separate durable lease that proves
  which plan-compiler version every live schema writer would issue.

It deliberately creates **no** index on any collection table. The companion
indexes themselves are built afterwards by resumable `CREATE INDEX CONCURRENTLY`
maintenance, which PostgreSQL cannot run inside the migration's transaction.
Migration 8 therefore takes no lock on a collection table and does not depend on
collection size.

The preparatory P0 artifact recognizes exactly the following two clean histories
while the companion-index capability stays `pending`:

- previous version `7`, checksum `8c8f2a4b0506d4abaffc19097a090f84f424ef521ec9575ac4577ecaa1b3432e`;
- optional target version `8`, checksum `e1cf6ca6e847d1b0593c6def69c5c3a65c0916efe1011fbdacedb87f72177cff`.

Use this exact v7-to-v8 sequence:

1. Deploy the verified P0 artifact to the **full** fleet first. This ordering is
   not optional: a Release B instance validates the exact v7 history on every
   list request and fails closed with `record_unavailable` against a database
   that carries a migration it does not embed. Require every P0 instance to be
   ready on clean v7, then drain the previous binary and every in-flight schema
   writer.
2. Keep application auto-migration disabled. From one designated job using that
   same P0 artifact, run:

   ```console
   pgbase migrate up --through 8 > migration-receipt.json
   ```

3. Require the receipt to contain `receiptVersion=1`, `target.version=8`,
   `target.checksum=e1cf6ca6e847d1b0593c6def69c5c3a65c0916efe1011fbdacedb87f72177cff`,
   `before.version=7`,
   `before.checksum=8c8f2a4b0506d4abaffc19097a090f84f424ef521ec9575ac4577ecaa1b3432e`,
   `after.version=8`,
   `after.checksum=e1cf6ca6e847d1b0593c6def69c5c3a65c0916efe1011fbdacedb87f72177cff`,
   and `after.dirty=false`. An exact retry may report version 8 in both
   checkpoints.
4. Rerun `pgbase migrate status` with the P0 artifact and require
   `current=latest=8` and `dirty=false`. Retain the receipt and the P0 image
   digest with the deployment record.
5. Require `pgbase doctor --json` to report `"ok":true` before continuing. While
   the capability is `pending`, doctor does not treat a companion index as a
   required physical contract, so a clean report here does not assert that any
   companion exists yet.

The capability stays `pending` across the whole migration window. Until it
reaches `ready`, no schema writer issues the v2 plan format and nothing treats a
companion index as required, so committing migration 8 changes no behaviour on
its own.

An older binary redeployed on top of this history does not misread the extra
objects: a companion index carries the planner's owned `idx_` name prefix on a
catalogued collection table, so it is classified as PgBase-owned rather than as
an orphan, and doctor's collection checks never require an index the catalog
does not name.

Do not apply migration 8 while any instance predates this P0 artifact. After
version 8 commits, do not deploy a pre-P0 binary or reverse the migration;
redeploy the exact verified P0 artifact with the capability left `pending` if a
deployment rollback is required, then forward-fix.
### Relation sort ordering release

This release changes how two query shapes are ordered and paginated. It needs
no migration and no capability, but it invalidates cursors that were issued by
the previous binary.

A sort on a bare single relation, such as `sort=owner`, now orders on the
root's own foreign-key column instead of joining the target and ordering on
its id. The foreign key guarantees those are the same value, and the root
column is already indexed, so the result order is unchanged while the plan
loses a join, a private cursor and the 100,000-row budget. A sort on a
multiple relation still returns the same `relation_sort_unsupported` error.

A relation filter combined with a root `-created_at` sort now uses the
`(created_at, id)` page index backward, instead of sorting the whole table. On
a 100,001-row collection this moved the plan from `Limit -> Sort` over a
sequential scan reading 936 shared blocks to a backward index scan reading 5,
with no sort node.

**In-flight cursors for those two shapes stop working when the new binary
starts serving.** The v5 descriptor's order path changes for `sort=owner`, and
the null placement changes for a relation filter with `-created_at`. Both
surface as `400 invalid_cursor`, which every client already handles as
"restart from the first page", so no client change is required. Nothing else
about the wire changes: `NULLS LAST` remains the public contract for a
nullable descending sort, and pages themselves are byte-identical.

There is no hold point. A mixed fleet answers each request from whichever
binary receives it, and a client that receives `invalid_cursor` simply
restarts its pagination.


#### Enabling the companion indexes

Activation is an operator command, not a deploy and not a configuration value.
The capability row is the only source of truth, so no environment variable, HTTP
route, or `pgbase.Options` field can move it:

```console
pgbase schema-index status --json
pgbase schema-index activate --witness INSTANCE_ID [--witness INSTANCE_ID ...]
pgbase schema-index build
pgbase schema-index status --json
```

1. `status` reports the state, how many companion indexes the catalog implies,
   how many exist, and every live schema writer with the plan-compiler version it
   would issue. A process claims that lease in the same transaction as a catalog
   change, so a writer that has proven nothing has also changed nothing.
2. `activate` requires the durable leases to be exactly the witnesses you name
   and every one of them to be at the current compiler version. It refuses an
   empty fleet: an activation must observe at least one writer that has proven it
   understands the companion contract. It moves `pending` to `building` only.
3. `build` runs `CREATE INDEX CONCURRENTLY` outside any transaction, resumes from
   whatever PostgreSQL's catalog already holds, drops and rebuilds the invalid
   index an interrupted run leaves behind, and promotes `building` to `ready`
   only after revalidating every companion against the current catalog. It is
   safe to rerun and safe to kill; rerun it until it reports `state=ready`.
4. Adding a field or index to an existing collection drops the capability back to
   `pending` in the same transaction as the catalog change, because the new field
   may imply a companion nothing has built. Rerun `activate` and `build` after
   such a change. This is the only schema change that costs an operator a
   rebuild.
5. Creating or deleting a collection does **not** drop the capability. While the
   state is `ready` the writer issues the v2 plan format, whose create plan builds
   the new collection's companion indexes in the same transaction as its table; a
   delete drops its table with every index on it. Neither can leave a companion
   unbuilt, so both carry the capability onto the catalog they commit. A new
   collection is therefore servable the moment it exists, with no operator step.
6. The capability row is the only thing that decides which plan format is issued.
   Across an activation or a rollback the fleet is briefly mixed, so `apply`
   accepts a plan checksum from either format: a preview taken just before the
   transition still applies, and the writer executes its own plan. Operators need
   do nothing about this window.

`pgbase schema-index rollback` turns the capability off from any state. It does
not drop the indexes: the rollout rules keep new physical objects in place after
a downgrade so a forward fix does not have to rebuild them.

`pgbase doctor --json` distinguishes the three states:

| Finding | Meaning | Action |
| --- | --- | --- |
| none | `pending`: no companion is a required contract | none |
| `schema_companion_index_build_incomplete` | `building`: the rollout is unfinished | rerun `pgbase schema-index build` |
| `schema_companion_index_missing` | `ready`, but a companion is absent or has the wrong key order | rerun `activate` then `build` |
| `schema_companion_index_invalid` | an interrupted concurrent build left an unusable index; reported in every state | rerun `pgbase schema-index build` |

`schema_companion_index_invalid` is recoverable and never requires editing the
PostgreSQL catalog by hand.

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
