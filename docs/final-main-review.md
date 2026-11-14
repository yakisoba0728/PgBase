# Independent main review — 2026-11-14 KST

Base: `92fa9eb51a97c0dd2b2ac227dfa3832aa6068689` (merged PR 136). Two fresh reviewers independently inspected data/auth/transaction/realtime paths and runtime/storage/backup/client paths. The integrating reviewer additionally traced coordination-session ownership and current documentation.

PgBase is a single-project PostgreSQL backend. This review assumed concurrent requests and multiple application instances sharing that project's database. It focused on connected high-impact paths; it was not an exhaustive line-by-line audit or a universal security assurance.

## Confirmed and corrected

1. **Authentication creation used a stale schema and password policy.** Pausing password hashing while changing the realm schema allowed text to be stored under an integer definition. Create now locks and validates the current contract inside its transaction after the expensive hash, preserving missing-realm error behavior.
2. **System-only auth updates discarded generated dates.** Email/password/status changes computed `OnUpdate` values but did not save them. The merged profile is now persisted for these updates too, while schema definitions are pinned during validation and write.
3. **Auth referential deletion stopped at one level.** A cascade into a second realm could bypass that record's incoming restrict, further cascade or set-null behavior. A complete deletion closure now checks restrictions and current delete authorization before effects. Caller-owned roots are preserved, duplicate roots are handled once, and internal references between roots do not cause false restrictions.
4. **New references could escape target deletion.** Validation only checked existence. A deletion paused after its incoming-reference snapshot allowed a new profile to reference the soon-deleted target. Publication now locks the actual auth user or native record row with `FOR KEY SHARE`; it waits and rejects if deletion wins. Separate PostgreSQL and HTTP concurrency tests verify both branches.
5. **Native deletion ignored auth-profile references.** HTTP deletion returned 204 despite an incoming auth `restrict`, leaving a dangling profile reference. Native record operations now call the same auth relation engine for their whole native deletion closure before storage/artifact effects. The fix covers ordinary deletion, native cascades, truncate and batch, plus authorized auth cascades and full rollback on denial. Unrelated native deletes use a metadata-only gate and do not lock unrelated profiles.
6. **SQL-console leadership could remain busy after Close returned.** pgx socket closure does not acknowledge server-side lock removal. A real connection with delayed Terminate processing reproduced an immediate coordinator handoff failure. Dedicated coordination sessions now share one bounded unlock-and-close helper, reused by SQL console, administrator setup and filestore paths. Borrowed staging connections retain their enclosing lease. The delayed-termination test fixture is shared rather than copied.

The README and quickstart also now describe schema 67 and the shipped PostgreSQL 16.16 development image. Historical deployment/release records remain distinct from current source.

All behavioral findings above had concrete failing reproductions before their fixes. Tests keep the original rollback, authorization and cleanup assertions.

## Verification

- Whole Go unit suite with race detector: PASS; blocking lint: 0 issues; vet: PASS.
- Coordinator handoff plus filestore ownership/admission regressions: 20 race-enabled repetitions, PASS.
- Focused auth schema/policy/date, transitive relation, multi-root and publication races: PASS.
- Eight native/auth HTTP cases with race detector: PASS, including restrict, set-null, multi-hop cascade, denied rollback, native descendant restriction, truncate, batch and concurrent publication.
- Candidate runtime inputs stayed unchanged during artifact verification.
- Same-digest E2E twice shuffled: PASS, **72.905s**.
- Same-digest restore/managed-backup/TLS suite on PostgreSQL 16/17/18: PASS, **38.910s**.
- Isolated release-phase image managed-backup/TLS checks: PASS, **18.273s**.
- Image scan: **0 HIGH/CRITICAL**, no exceptions; SPDX artifact generated.

Verified ARM64 candidate: **rc.9**, schema **67**, digest:

```text
bbb9706400e2ee7fdd6e22ae0321f860e7bd160e7574de7aca93f8dca0ef1ddc
```

The first full CI run caught two test-harness follow-through issues. The intentional native-delete pause hit the unchanged 5-second production statement limit, and its lock observer could compete with the blocker/deleter/creator for the small request pool. That fixture now uses a separate coordination connection for observation, explicitly tests a four-connection request pool, and has a test-only 30-second statement timeout with a 45-second outer limit. The real physical-row lock-wait and rollback assertions remain. The cascade query-count tracer also now classifies the single auth metadata gate and requires exactly one such query for both 8-record and 64-record closures; its bounded-count and affected-row assertions remain. Focused HTTP race tests pass (18.564s), and the query-budget test passes three race-enabled repetitions (6.794s). No production source changed for these harness corrections. Original CI failures remain in local evidence.

Merge also requires the final commit's required CI, full manual Backend matrix (including native Windows/macOS) and AMD64 stock-image backup/TLS/upgrade workflows. Local logs, disposable data and candidate archives are excluded from Git.

## Bounds

Reviewers read auth CRUD, registration and policy transitions; relation identity and deletion flows; record transaction/artifact boundaries; realtime delivery; backup activation, coordination and integrity; current image packaging; CLI gates; SDK session transitions; and connected administrator backup UI contracts. They did not reread every historical document, every UI screen or every SDK method, or enumerate every expression/permission combination. No broad throughput benchmark was run.

External authorized OAuth login remains unavailable without a test app/account, as previously confirmed. Process-termination checks do not substitute for physically cutting power to storage hardware. No production deployment was performed.
