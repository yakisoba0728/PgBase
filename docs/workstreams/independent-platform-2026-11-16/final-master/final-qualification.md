# Final master qualification

## 2026-11-17 bounded blocker recovery — still NOT_READY

[Performance blocker recovery](performance-blocker-recovery.md) supersedes the prior next-action text below without replacing its source-bound results. Three separate agents reanalyzed the original failed load, inspected the measured runtime paths, and independently verified raw evidence. Delay was localized to sampled durable COMMIT and associated pool/queue waiting, but no correctable software cause was reproduced. Six existing parent controls plus three subtests passed with race on isolated PostgreSQL 17.9; they do not qualify the 200 RPS gate. No runtime patch, new final candidate, full-CI execution, or final performance retry was adopted.

Starting PR HEAD was `77bc070047c5a19d68439b75cb7b65bcde5d1c9a`. Published recovery evidence checkpoint is `c16370534fcce6b9859d99b6e8a5bb966a16150d` (tree `6f5de55b19b22be953dc759299732ec20ad30044`). Final closeout publication is documentation/evidence only; its exact immutable HEAD/tree is in PR #148's handoff. All 1,932 non-document entries remain equivalent to the last tested candidate. Independent evidence integrity passes; correction approval and permission under the agreed prerequisites to run a new final gate are absent. The decision remains **BLOCKED / NOT_READY**, with main untouched and PR Open / Draft / Unmerged.

## Decision: BLOCKED / NOT_READY

The frozen candidate's mandatory sustained-load latency gate failed. [Full source qualification 43982303611](https://github.com/yakisoba0728/PgBase/actions/runs/43982303611), attempt 1, completed with **14 successful jobs and one failed mixed-http job**. All 120,000 arrivals completed without drops or unexpected errors, but end-to-end **p99 588.031143 ms exceeds the unchanged 250 ms limit**. Independent reviewers recalculated the original request rows and reproduced that result.

The four bounded P2 correction areas are independently verified and retained. No new P0/P1 application defect was established by this review. Neither the passing functional jobs nor the historical performance pass overrides the current failed mandatory gate. PR #148 remains Open / Draft / Unmerged; main is unchanged. This round ends with an explicit blocker, preserved evidence, and a bounded resumption procedure. No background investigation or new test phase is scheduled.

## Source and publication identity

| Field | Exact value |
| --- | --- |
| Initial PR source | `242f80aea730ee483cc22798fb6450bdb3064081` |
| Untouched main / merge base | `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a` |
| Frozen published candidate | `89e883fc700ef0250024427a1f09bb88affe0383` |
| Equivalent local candidate | `51ea9dfae53d295c3bc55fcab6db2db78bfeccca` |
| Actual CI synthetic checkout | `b5c17b6f989bb11575e83544d37cc37fc6385d19` |
| Equal candidate/local/checkout Git tree | `c9a4c21a136fe6f9de0d854d0f7453aedb6a5b55` |
| Full CI | `43982303611`, attempt 1, completed / failure |
| Post-CI changes | Documentation and evidence only, with `[skip ci]`; exact published documentation HEAD is in PR #148's final handoff |
| Main disposition | **NO_MAIN_ROLLBACK_NEEDED** |

All fifteen job logs record the actual checkout. Source identity, API snapshots and raw-log hashes are retained in [final-ci](../evidence/final-master/final-ci/). The final documentation tree is intentionally distinct from the tested tree; its non-document paths and file modes are checked against the frozen candidate. No fresh runtime result is claimed for that later documentation commit.

## Corrections accepted in this round

| Correction / priority | Resulting behavior | Separate regression evidence | Published code commit |
| --- | --- | --- | --- |
| STORAGE-LEASE-01 / P2 | Put/Open/Delete/publication reuse a context lease only when the receiving controller owns it. | Original source fails three method cases and physical PostgreSQL foreign-controller publication/read/write/delete checks; corrected source and adjacent race/migration checks pass. | [6d09071e](https://github.com/yakisoba0728/PgBase/commit/6d09071e0c8229eeb6fca345067ee4e957d20000) |
| SDK-REFRESH-01 / P2 | Queued TypeScript/Dart refreshes retain the issuing session; obsolete work rejects with `session_changed` before transport. | Two original-source failures per language; corrected lifecycle suites and real HTTP contracts pass. Same-session serial rotations/logout and explicit replacement-session refresh remain covered. | [a8fe0bf1](https://github.com/yakisoba0728/PgBase/commit/a8fe0bf17c5a837921d09a969a489970a1819042) |
| ADMIN-SESSION-01 / P2 | Jobs and storage-migration actions/callbacks retain invocation session ownership. | Original source: seven failures and eight controls; corrected focused suite 15/15. Final complete frontend CI: 78 files / 458 tests pass. | [06287fa8](https://github.com/yakisoba0728/PgBase/commit/06287fa8a4eb10f392976e2fac2c70363822957a) |
| PERF-EVIDENCE-01 / P2 | The existing repeated-HTTP evidence reader enforces registered stage order, complete latency populations and pool4. | Four invalid-evidence cases fail against original reader; corrected 17-script-test suite passes; three historical logs retain their metrics. | [10ab48ca](https://github.com/yakisoba0728/PgBase/commit/10ab48ca6fd6117bb8f285251c88edd1ce3de818) |

[Independent QA](independent-qa.md) records separate author/reviewer executions and their actual source mappings. [The ledger](refactor-optimization-ledger.md) records compatibility and reversal boundaries. The storage defect was reproduced in trusted service composition; no public HTTP cross-tenant exploit was demonstrated. UI fencing cannot undo a server operation already authorized before a session changes.

**New runtime optimizations adopted: zero.** Existing zero-allocation sorted-field behavior was rechecked, while timing observations overlapped shared-host work. A new causal speed, CPU or RSS improvement is **INCONCLUSIVE / NOT_ADOPTED**. No performance threshold, timeout, assertion, durability setting, retention rule, pool budget or access-control policy was weakened. Schema remains 69 and the administrator layout is unchanged.

## Review team and independence

Five actual subagents completed assigned work: `git_evidence_recon` (ancestry/evidence), `backend_quality` (backend correction and fresh supplied-finding review), `sdk_ui_quality` (TS/Dart/admin corrections), `performance_infrastructure` (evidence-reader correction and independent raw-load analysis), and `independent_qa` (separate defect reproduction/source/CI acceptance). At most three subagents ran concurrently, four including the Coordinator. Independent QA authored none of the runtime changes. The performance and security follow-ups inspected the actual final artifacts; they did not run another workload or scan. [Final independent CI](final-ci-independent.md) and [independent performance](final-performance-independent.md) both conclude BLOCKED.

## Current whole-source CI: 15 actual jobs

| Job | Job ID | Result |
| --- | ---: | --- |
| backend / Go unit | 133701569373 | PASS |
| PostgreSQL integration / 16.13 | 133701569741 | PASS |
| PostgreSQL integration / 17.9 | 133701569828 | PASS |
| PostgreSQL integration / 18.3 | 133701569796 | PASS |
| PostgreSQL integration / 17.9 / pgbase | 133701570002 | PASS |
| frontend / test | 133701569311 | PASS |
| browser-and-recovery / e2e | 133701569432 | PASS |
| backend / macOS terminal | 133701569351 | PASS |
| backend / Windows self-update | 133701569629 | PASS |
| backend / Observability artifacts | 133701569656 | PASS |
| backend / Release phase artifacts | 133701569883 | PASS |
| security / audit | 133701569385 | PASS with retained findings |
| source analysis / Go | 133701569733 | PASS with retained findings |
| source analysis / JavaScript-TypeScript | 133701569579 | PASS with retained finding |
| mixed-http | 133701568842 | **FAIL: sustained-load p99** |

The current successful jobs cover configured Go/static/race/formatting/skip-policy checks; fresh PostgreSQL 16.13/17.9/18.3 schema and application contracts; isolated storage/MinIO/backup/restore; actual TS/Dart HTTP contracts; complete administrator tests/typecheck/clean distribution; Windows/macOS; observability; and release-artifact verification. Browser results are **Chromium 26, Firefox 6, WebKit 6**; hardened runtime and **30 black-box parent executions plus 28 subtests** pass. TypeScript passes123/123 with zero failures/cancellations/skips, and Dart passes142, on each main PostgreSQL16.13/17.9/18.3 job. Each also passes five live SDK HTTP contract parent tests and two browser-example tests. Unit Go race reports49 passing packages; PG16/18 report51 each; PG17 core race reports50 plus one pgbase shard package. These are package outcomes, not individual case or skip counts. Independent reconciliation finds **92/93 required substantive steps successful**, with the sustained-load gate as the sole failure. The exact required steps, full decoded logs and source checkouts are independently reconciled; setup/teardown and conditional release-only steps are distinguished from mandatory assertions.

Frontend CI passes **458/458** including the untouched ApiView retry-render file's three tests. The earlier local whole-suite 457/458 failure and isolated 3/3 pass remain preserved. The final whole-suite pass resolves current functional acceptance; it does not prove the historical timeout's cause. Historical Firefox font cancellation also remains unexplained; no browser-error suppression was added.

## Sustained-load result and limits

The mixed c1→c8→c32→c4 (recovery) functional stages pass. The fixed-arrival protocol remains 200 RPS for 600 seconds with request pool4 and PostgreSQL durability enabled.

| Measurement | Current candidate / run 43982303611 | Required result |
| --- | ---: | --- |
| Scheduled / enqueued / attempted / completed | 120,000 / 120,000 / 120,000 / 120,000 | 120,000 each |
| Dropped / unscheduled / unexpected errors / incomplete | 0 / 0 / 0 / 0 | Zero each |
| Offered load / measured arrival interval | 200 RPS / 600.000612834 s | 200 RPS × 600 s |
| End-to-end p50 / p95 / p99 | 6.264600 / 319.624000 / **588.031143 ms** | p99 ≤ 250 ms |
| HTTP service p50 / p95 / p99 | 5.854931 / 152.715202 / 277.767335 ms | Supporting measurement |
| Worker-queue p99 | 425.904449 ms | Supporting measurement |
| Generator p99 | 1.065102 ms | Supporting measurement |
| Requests exceeding 250 ms | 7,726 of 120,000 | Quantile gate above |
| Request pool / canceled acquisitions | 4 / 0 | Pool4 unchanged |
| Pool waits / cumulative wait | 108,914 / 2,653.122243438 s | Supporting measurement |
| Allocated bytes / allocations per request | 615,814.5 B / 9,045.1 | No new optimization claim |
| GC cycles / stop-the-world pause | 1,271 / 126.178154 ms | Supporting measurement |
| Runtime busy CPU / GC CPU | 404.278245 s / 46.073618 s | Go runtime counters, not host CPU percentage |
| Heap before / after | 96,764,608 / 100,894,720 B | Heap snapshots, not RSS |

The preserved archive is [final-mixed-http.zip](../evidence/final-master/final-ci/artifacts/final-mixed-http.zip), 3,717,310 bytes, SHA-256 `560f35fa19fb6a18c96c7bc517759beeedb6604dc2fbdd2de044c38113e7e393`. Both reviewers independently recalculate all five quantile series from the ordered 120,000 rows. The five-ms arrival schedule, workload counts and zero-error population are consistent. This is a real recorded gate failure, not a row-count or parser mismatch.

The current runner uses AMD EPYC 9V74; the historical 22.221162 ms pass used Intel Xeon Platinum 8573C. Both report four vCPUs, Go 1.26.11 and PostgreSQL 17.9, but they are **not a controlled identical-host baseline/final pair**. The current-versus-historical observations show mean WAL sync 0.178575→0.803968 ms, WALWrite wait samples 57→2,843, pool-full samples 93→1,604 of 6,000, and host I/O wait 1.087%→5.235%. The worst load windows align with high pool occupancy and I/O wait. These are associations consistent with commit-I/O and queueing pressure; they do not prove a unique cause, a hardware-only explanation, or a regression caused by this round's code.

The load/query/dependency/harness paths are unchanged; the sole changed Go runtime file contains the four storage-controller identity checks. This observation narrows investigation but is not an exonerating experiment. Whole test wall time includes approximately 161.117 s setup, 600.000613 s offered load and 3.413 s post-load work. It does not mean the arrival interval was silently lengthened. Application RSS was not recorded. PostgreSQL Docker MemUsage peaks around364.8MiB over the observer's whole setup/load/drain lifetime; the samples lack timestamps needed to align a peak to offered load and are not process RSS. Host CPU uses600 aligned load-window vmstat samples. Go CPU/heap/allocation counters include the in-process server, client, generator and diagnostics; they do not isolate server resource consumption.

No concrete safe runtime correction has been reproduced from these data. Therefore **no second final gate was run**. The historical failure with 547 drops also remains a failure, with its original archive recovered and verified this round.

## Security and operational acceptance boundaries

[Fresh security review](final-security-review.md) retains exact per-location controls, source flows, primary-advisory references and all raw severities:

- CodeQL: **25 raw findings — 1 CRITICAL, 17 HIGH, 7 MEDIUM**. All 25 concrete reported paths receive scoped `not_actionable` reasoning; resolved rule/flow identities and 69 referenced source blobs match the historical baseline. This is not a library-wide security proof.
- Image: **32 raw findings — 21 MEDIUM, 9 LOW, 2 UNKNOWN**; **30 remain `needs_review`**, and two are excluded only for the captured amd64 architecture. The HIGH/CRITICAL scanner gate has zero results. Native PostgreSQL helpers, upstream/distribution advisory disagreement, and UNKNOWN severities remain explicit limitations.
- Configured Go and JS/TS scans evaluate 37 and 105 queries. Their script-test total is 17 each: twelve SARIF parser tests plus five load-evidence tests. Excluded platform/test/generated paths, Dart/SQL coverage limits, package/CA/tzdata scope and release-only conditional steps remain documented. No suppressions or allowlists were added.

Schema69 upgrade/restore requires the reviewed [migration and rollback procedure](migration-and-rollback.md). Retained uncertain storage intents cannot be aged out merely because Delete succeeded or a read is absent. Old workers must stop or upgrade before cutover; writers and remote uploads must be drained or independently proven quiescent. Jobs fencing does not guarantee exactly-once external effects.

The persistent diagnostic budget is **4 request-pool + 1 schema listener + 1 backup gate + optional 1 observer = 7**. Active storage operations use additional dedicated coordination sessions, with admission up to 64 per controller. This is not a total `max_connections` recommendation. Real external OAuth/OIDC/SMTP/S3-provider and broader device/accessibility acceptance remain outside the bounded fixture.

## Evidence retention and resumption

All **617 original evidence files** (615 in the workstream plus two older audit files; 15,197,375 bytes) remain identical. All eighteen duplicate-blob groups retain their original paths. Three duplicated API snapshots have preferred navigation only; no raw evidence was deleted, moved or rewritten.

This round adds all fifteen final job logs, exact run/job/step/source snapshots, nine original final artifact ZIPs, release manifests/hashes, independent reports, and the original historical 547-drop failure ZIP. All ten final hosted artifacts were downloaded and matched to API SHA-256/size. The 51,295,460-byte release binary archive is **not** duplicated in Git: both original manifests and all eight binary hashes are retained, and its hosted copy expires 2027-02-15 08:47:34 KST. Future rebuilds are not claimed byte-identical. See [artifact retention](../evidence/final-master/final-ci/artifact-retention.json) and [evidence index](evidence-index.md).

The next permitted step is a **bounded diagnosis leading to a reproduced cause**. A second final 200 RPS × 600-second run requires an actual safe source correction and separate QA approval first. Do not rerun unchanged source to search for a favorable host, weaken thresholds, enlarge the request pool, or lower durability. Preserve the current failure as the baseline for that work.

[Continuation state](../continuation-state.md) contains the exact source and offline verification commands. The PR is not ready for manual merge. No merge, main rollback, deployment, tag, release or package publication was performed.
