# Nonperformance security and independent QA

## Decision and source boundaries

The bounded review accepts the selected Storage and SDK/UI changes after the
schema-68 restore compatibility correction. It does not qualify a release.
No performance experiment, profiler, benchmark or sustained workload was run.

Independent QA used local combination `e2d347db968c30ad08de9791bed741b1ac07c3ce`,
based on PR source `a1a652289f0bc817f18db257282c758e66b4a672`. It contains the
selected Storage runtime/tests, Storage correction
`a594ee953075e429286764d689411b0e6ba99cd9`, SDK original
`33de5dbbe574315ae77d1790f4203548121db415` and administrator original
`8721239dc370f12e139b92ffec7a482edc70f0ef`, plus the authored avatar correction.
Publication identities and final CI source/merge/tree belong in the coordinator's
validation report; local equivalent trees are not silently relabeled.

The reviewer also inspected browser opt-in `0118f4c5777ae5943c75bb1a860b7d401d805057`
and the additive nonperformance workflow slice `1bebfb18`. Original backend,
E2E and full-qualification workflow bytes remain unchanged. The new backend
copy differs in its jobs only by omitting the two benchmark-baseline steps;
the new workflow has a distinct name and never reports a performance pass.

## Independently found Storage regression and correction

Managed restore calls `ResetRestoredStorage` before `New` upgrades the restored
database. The candidate unconditionally updated schema-69's `retain_on_success`
column, so a valid schema-68 restore failed with SQLSTATE `42703` before it could
upgrade. A separate minimal schema-68 reset fixture reproduced that failure.
The initial linker resource failure and an invalid fixture-key attempt are
preserved separately and are not counted as defect reproductions.

Storage correction `a594ee95` checks for the column in PostgreSQL's catalog,
clears retention only when available, and retains the same transactional
namespace detachment. Its author added an actual `pg_dump`/`RestoreNew` case:
reset before upgrade, migrate/reapply to 69, verify file bytes, clear inherited
credentials and cleanup intents. Independent race execution passed that case
and the smaller reviewer fixture. This restores an existing backup contract;
it does not access or mutate the source namespace.

## Durable cleanup invariants reviewed

| Boundary | Independent result |
| --- | --- |
| Intent before Put | `reserveObject` commits a retained queue row before the variant Put; actual partial-write regression checks it inside Put. |
| Unknown Put/COMMIT | Unknown remote completion keeps retention; reference insertion and intent deletion share the publishing transaction. Lost real COMMIT reply preserves referenced bytes. |
| Late outcomes/restart/workers | Staging plus reference locks fence deletion. Late COMMIT/ROLLBACK, killed sessions/process, restart and eight-cleaner regressions preserve live objects or durable intent. |
| Delete failure | Persisted retry state survives errors; a later pass physically deletes abandoned bytes. Successful deletion does not retire unresolved remote-write uncertainty. |
| Schema upgrade/reapply | Existing queue rows survive 68→69; the legacy-worker trigger prevents deletion of uncertain intent; reapplication stays compatible. |
| Ordinary uploads/restore | Existing upload ownership and reference checks remain; schema-68 and schema-69 restore reset cases pass. |
| Migration/operator policy | Retained uncertainty blocks source cutover. Reviewed recovery instructions require proof of all remote writes ending before exact-key retention retirement; no TTL or bulk queue deletion. |

Focused independent PostgreSQL 17.9/race execution passed **17 top-level tests**,
zero skips/failures: 14 filestore, two repository migrate cases, and one temporary
independent reset fixture retained as an evidence patch instead of a duplicate
permanent test. This private cluster used SQL_ASCII; these focused cases do not
qualify Unicode behavior. The separate security-contract cluster used UTF8.
MinIO and final PostgreSQL-version matrix results are owned by their actual
executions in the Storage/coordinator reports, not inferred from these local tests.

Uncertain tombstones may remain indefinitely. Delete cancellation remains
cooperative. No claim of bounded tombstone count, actual cloud-provider fault
semantics, or automatic proof of remote quiescence is made.

## Authored avatar address correction

The pre-existing special-use-address TODO was reproducible: the prior
`publicIP` helper accepted 24 tested special-use destinations, and also accepted
invalid IP values. Public-address controls passed. The correction normalizes
IPv4-mapped IPv6, rejects invalid/private/non-unicast addresses and applies a
conservative public-avatar policy covering shared, reserved, documentation,
benchmarking, protocol and translation/tunnel ranges.

The policy and its compatibility restriction are documented in
[`oauth-provider-verification.md`](../../oauth-provider-verification.md).
The primary [IANA IPv4 registry](https://www.iana.org/assignments/iana-ipv4-special-registry/)
and [IANA IPv6 registry](https://www.iana.org/assignments/iana-ipv6-special-registry/)
were read to verify the listed ranges. This is a destination-policy defect,
not a demonstrated request to an internal service or an account takeover.

All DNS answers still pass the policy before dialing the checked literal.
Redirect policy, existing hop bound and explicit loopback-only fixture exception
remain. IPv6 avatar destinations are deliberately restricted to `2000::/3`
excluding listed special subranges; globally reachable protocol exceptions and
translation-only deployments are outside this conservative avatar contract.

Author-side full unit/race execution passed **86 top-level tests** with no skips:
userauth 34, backup 40, mailer 12. This includes signed fake-provider OIDC claims,
key constraints, archive boundaries, local mail fixtures and avatar controls.
The distinct SDK/UI agent independently reviewed the exact avatar patch and ran
its six focused top-level tests under race; all passed. Its evidence is in
`evidence/phase-nonperformance-sdk-ui/independent-avatar-review.json` and matching
compressed log. That reviewer is separate from this correction's author.

## Independent SDK/UI and bounded security contracts

SDK source review verified transport identity and auth-generation ownership for
both synchronous listener failures and delayed promise/future rejections.
Administrator source review traced `scope.run` admission and `isCurrent` checks
through progress, notifications, caches, tracked jobs and URL selection.
Same-session error/cancellation reconciliation remains intact. No API/schema
change or visual redesign occurs in this slice.

Independent execution passed TypeScript lifecycle **4**, Dart lifecycle **4**
and administrator backup **12** tests, with no skips. Browser source review
verified unchanged default projects, distinct backend origins, strict engine
configuration, global smoke serialization, collection-flow prerequisites and
unique collection names. Discovery/configuration is not browser execution;
actual Chromium/Firefox/WebKit outcomes belong in final CI evidence.

On the combined runtime, a fixed selection of **28 PostgreSQL/race tests** passed
with zero skips across userauth, adminauth, Realtime, Jobs and the real HTTP app:

- Actual auth method/header/query enforcement; realm/session isolation.
- TOTP enrollment/replay, recovery gating, password/session revocation, login
  versus deactivation and administrator-factor rotation concurrency.
- Local OAuth state/PKCE/replay and actual HTTP session issuance.
- Multipart record/file atomicity, denial rollback, attachment retargeting and
  preservation of the authenticated principal across mutable batch hooks.
- Extension precommit JSON serialization, savepoint rollback failure,
  bounded lost-response rollback and lost-COMMIT object integrity.
- Realtime committed queue identity, late publication, delivery retries,
  retention/policy fencing and Jobs expired-worker/lease completion fencing.

The exact test-name selection, source hashes, original failed outputs,
environment/setup failures, successful outputs and digests are in
[`evidence/phase-nonperformance-security/manifest.json`](evidence/phase-nonperformance-security/manifest.json).
Go's debug DWARF was omitted during local linking to avoid a resource failure;
race instrumentation and assertions remained enabled.

## Explicit residual scope

| Status | Boundary | Next action |
| --- | --- | --- |
| Deferred | Arbitrary policy-expression combinations and every per-operation multipart/batch body/header/query variant | Add a specified contract and narrow reproductions before changing semantics; selected principal/atomicity tests are not exhaustive expression verification. |
| Deferred | Extension VM/transport failures after a successful DB commit | Preserve ambiguous-outcome/idempotency semantics; a later VM or transport error is not proof that SQL rolled back. Exhaustive host/VM adapters were not tested. |
| Deferred | Real external OAuth/SMTP provider interoperability | User-supplied isolated provider account/application and explicit follow-up scope; this session used local fixtures only. |
| Open | Native-helper/image vulnerability reachability and platform/language scan exclusions | Reconcile latest final-source artifacts; preserve every unresolved record and existing applicability limit. |
| Coordinator gate | Fresh CodeQL/dependency/image scans, platform matrix and final browser/MinIO status | Use final exact-source CI outcomes and artifact identities in the validation report. |
| User-owned deferred | Performance | No performance work executed. Existing 547 dropped requests remain unresolved; overall release decision stays **Not Ready**. |

Historical 24 Go/one JS CodeQL findings and 32 image records remain historical,
not replaced with zero or claimed to be a fresh scan by this document. This
bounded source review does not certify every module, extension, policy or
provider. No suppression, required-check removal, main push, release or
deployment occurred in this QA slice.
