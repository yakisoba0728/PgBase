# Final master refactor and optimization ledger

This round starts from `242f80aea730ee483cc22798fb6450bdb3064081` (tree `b758d273142d75377c18011d240cd64929cb4c97`). Local implementation SHAs below identify the authors' actual checkouts; publication mappings and the exact final CI source are recorded in the final qualification. A local commit identity is not presented as a separately published GitHub revision.

## Accepted corrections

| ID / priority | Behavior and bounded correction | Regression evidence | Original local implementation |
| --- | --- | --- | --- |
| STORAGE-LEASE-01 / P2 | Reusing a context lease from controller A previously routed controller B's Put/Open/Delete to A's store and could authorize B's publication. Four controller-identity checks make ownership consistent with BeginStorageOperation/OpenRange. | Three canceled-context method cases fail on original runtime and pass on corrected source; physical PostgreSQL namespace/publication verification is recorded separately in independent QA. | `e59fc07668e0a282555f2f52dbddf890162254e0` |
| SDK-REFRESH-01 / P2 | A refresh queued for an old user previously ran with the replacement user's bearer. TS and Dart capture the issuing generation/origin and reject cross-session execution with `session_changed`. | Two deterministic old-source failures in each language; preservation tests cover serial rotations, logout and explicitly started replacement-session refresh. Full author suites: TS123 / Dart142 pass. | `8ccf8ca7b5a7a973dc209563e2b3256966da89be` |
| ADMIN-SESSION-01 / P2 | Jobs and storage migration callbacks could show an old request's toast or refresh replacement-session cache. Existing session-scope helpers now guard request start and success/error/settled handling. | Independent old source: seven failures / eight controls; corrected focused suite:15/15. Same-session actions, error display and queued clicks remain covered. | `b56b74022eced2c767429b435a2abc4031ec37ee` |
| PERF-EVIDENCE-01 / P2 | The offline repeated-HTTP parser accepted wrong worker stages, reordered recovery, truncated latency populations and enlarged request pools. It now enforces the already registered c1→c8→c32→c4 sequence, complete populations and pool4. | Original parser: four invalid-evidence failures plus one valid control. Corrected script suite:17/17; three historical logs retain identical parsed metrics. | `6add53b53b57e6fc3826820ac7b6580b493629cb` |
| CONNECTION-DOC-01 / documentation | Clarifies that the observed seven persistent diagnostic clients do not cap all database sessions: storage operations use additional dedicated coordination sessions, with admission up to64 per controller. | Checked against storage_settings.go and pool.go; no runtime configuration changed. | Coordinator documentation commit |

These are correctness and evidence-quality improvements. No public HTTP exploit was demonstrated for the trusted service composition storage defect. UI guards cannot undo an already authorized server operation; they stop cross-session request initiation and stale client-side settlement. SDK distribution output and administrator distribution output are regenerated and independently checked against source. Database migrations, public API routes and visual layout are unchanged by this round.

## Performance experiments and adoption

**New runtime performance optimizations adopted:0.** The existing sorted-field path still measures0 B/op and0 allocs/op in the bounded query benchmark; its historical copy/sort reference measures8,680 B/op and4 allocs/op. This rechecks a pre-existing improvement and is not new optimization work. The current twenty-field encoder records5,745 B/op and89 allocs/op in three observations.

Elapsed-time results overlapped other worktree builds. No same-environment causal speed, CPU or RSS improvement is established: **INCONCLUSIVE / NOT_ADOPTED**. No durability, retention, transaction, access-control, timeout or performance threshold was relaxed. The final fixed-arrival gate remains200 RPS×600 seconds,120,000 complete requests,zero drops/errors/incomplete, end-to-end p99≤250 ms. Its fresh source, raw measurements and limits belong in [final qualification](final-qualification.md).

The repeated-HTTP evidence validator's c1/c8/c32/c4 protocol is distinct from the final mixed-HTTP and sustained fixed-arrival gates. Tightening its parser does not alter the recorded workloads or retroactively rewrite prior failures.

## Preserved failures and limits

- The author UI baseline was449/449. The modified full local run was457/458, with an untouched ApiView retry-render test timing out. That file alone subsequently passed3/3 without assertion or timeout changes. Shared-host contention is an inference; the failure remains preserved and requires a clean final complete UI CI result.
- The historical sustained-load failure with547 dropped arrivals remains historical failure evidence. It is not explained away by this round.
- Independent PostgreSQL provisioning initially lacked pg_trgm; this prevented migration66 before either old/new behavior executed. Installing the source-matched extension is a fixture correction, not a runtime fix or a RED result.
- Existing external-provider, storage ambiguity/tombstone, old-worker, external Jobs-effect and native scanner limitations remain in [risk register](risk-register.md).

## Reversal and compatibility

All accepted runtime changes are narrow logical commits. Reverting a code correction on an isolated branch is possible but reintroduces its reproduced defect and requires fresh regression qualification. Keep generated SDK/UI output consistent with any source reversal. This round introduces no schema migration and authorizes no database downgrade. Restoring a pre-schema69 deployment requires the separately verified backup/data procedure in [migration and rollback](migration-and-rollback.md), not a Git reset. Main remains untouched and PR148 remains Draft/Open/Unmerged.

## Detailed author evidence

- [Backend report](backend-review.md) and [manifest](../evidence/final-master/backend/manifest.json)
- [SDK/UI report](sdk-ui-review.md) and [manifest](../evidence/final-master/sdk-ui/manifest.json)
- [Performance/infrastructure report](performance-infrastructure-review.md) and [manifest](../evidence/final-master/performance/manifest.json)
- [Independent QA](independent-qa.md) distinguishes separate executions from author results and final CI.
