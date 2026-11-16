# Nonperformance completion: Storage and schema 69

## Source and decision

Author-tested runtime: `a594ee953075e429286764d689411b0e6ba99cd9`, based on PR
source `a1a652289f0bc817f18db257282c758e66b4a672`. Only the existing Storage
candidate and its tests/documentation were selected from the checkpoint; its
performance changes were not imported. The original implementation was retained.

| Existing commit | Selected local commit | Scope |
| --- | --- | --- |
| `a426766a` | `098ddd7f` | Durable cleanup runtime and schema 69 |
| `06938a41` | `02b1cadf` | Independent retained-intent regressions |
| `67866b9d` | `d5d1855f` | Crash, commit ambiguity, retry and upgrade tests |
| `71dc5815` | `97cf7f30` | Historical source-bound evidence and operator guidance |
| New correction | `a594ee95` | Schema-68 backup restore compatibility |

Independent agent `recon` reviewed the selected candidate, found the restore
defect below, reviewed its correction and independently passed 17 focused
PostgreSQL/race tests. The correction is accepted for integration. The final
combined-source and hosted MinIO decisions belong to the coordinator's
`phase-nonperformance-validation.md`; this report does not replace them.

## Confirmed defect and correction

Managed restore calls `ResetRestoredStorage` after restoring the dump and before
the candidate application applies migrations. The candidate unconditionally
updated `retain_on_success`, which does not exist in a schema-68 backup. An actual
schema-68 `pg_dump` → `RestoreNew` → storage-reset regression failed with SQLSTATE
`42703` before the fix.

The reset now checks whether the restored table has the retention column before
clearing it. The transaction still discards inherited cleanup work and source
credentials and selects the new local namespace. On schema 69 it still clears
retention before deleting source intents, preserving the legacy-worker guard.

The regression now passes, preserves the restored file bytes, verifies removal
of source credentials and pending deletion work, then upgrades to schema 69 and
reapplies migrations safely. `docs/storage-settings.md` documents both restore
orders and the conservative operator procedure for migration blocked by
uncertain writes.

## Author validation

All executed integration tests used disposable loopback PostgreSQL 17.9 with
normal durability, Go 1.26.11, package parallelism one and the race detector.
The final cluster explicitly uses UTF8. No production storage or credentials
were used.

| Exact-source run | Result |
| --- | --- |
| Valid before-fix schema-68 backup regression | Failed with missing `retain_on_success` / SQLSTATE `42703` |
| Focused restore/upgrade tests at `a594ee95` | 4 passed; zero skips/failures |
| All affected packages at `a594ee95` | Filestore 75, migrate 32, system migrations 38 passed: **145 total**, zero failures |
| Optional local MinIO tests | Four existing skips; fixture not configured locally |
| Independent QA at the same runtime | 17 focused PostgreSQL/race tests passed; zero skips/failures |

The initial affected-package run passed filestore and system tests but failed an
existing Unicode migration fixture because `initdb --locale=C` selected
SQL_ASCII. The corrected UTF8 rerun passed every affected test. A preliminary
restore attempt ran after the previous disposable server stopped; several later
attempts could not compile while the home-directory tmpfs was full. These are
separately classified environment failures, not bug reproductions or passes.
The canonical before-fix failure and after-fix passes are explicitly identified
in the manifest.

A pinned standalone MinIO build was attempted and cancelled with exit 130 when
resource pressure affected other validation. Only that attempt's exact newly
downloaded MinIO source/archive was removed. Hosted nonperformance CI is expected
to provide the repository's isolated MinIO fixture. No optional test or existing
quality gate was weakened, and no actual S3 fault result is claimed here.

## Safety boundaries and evidence

- Cleanup intent is committed before Put; failed or ambiguous writes retain it.
- Staging/reference locks and atomic publication protect late commit/rollback,
  restarted processes and concurrent cleanup workers from deleting live objects.
- Delete failure state survives for retry; late bytes remain covered by retained
  intents even after an earlier successful Delete.
- Schema-68 queue upgrade, repeated migrations, schema-68 backup restoration and
  schema-69 source-intent detachment have explicit PostgreSQL regressions.
- The database guard protects retained intents from older workers; migration
  activation remains blocked until uncertainty is resolved.
- Retention has no arbitrary TTL. Remote-write quiescence must be proved before
  an operator retires retention for reviewed keys. Unprovable quiescence means
  retaining the tombstone and deferring migration activation.
- Delete cancellation remains cooperative for custom stores and blocked local
  system calls. Local PostgreSQL/fault evidence does not prove production S3
  network behavior or provider interoperability.

`evidence/phase-nonperformance-storage/manifest.json` contains source hashes,
commit mapping, exact commands, all run classifications, counts and compressed-log
hashes. The preserved runner uses task-local paths and port 56941; adapt those
when reproducing it. Historical recovery evidence remains unchanged in its own
directory and is not represented as a current-source result.

No performance investigation, change or measurement occurred. Performance is
owned by the user and remains incomplete; the overall release decision remains
**Not Ready**.
