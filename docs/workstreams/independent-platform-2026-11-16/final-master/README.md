# PgBase final master consolidation

Status: **BLOCKED / NOT_READY — current sustained-load p99 fails the mandatory gate.**

[Full qualification 43982303611](https://github.com/yakisoba0728/PgBase/actions/runs/43982303611) completed: **14 successful jobs / one failed mixed-http job**. All 120,000 scheduled requests completed with zero drops/errors/incomplete requests; end-to-end p99 is **588.031143 ms**, above the unchanged 250 ms limit. Independent raw-row recomputation confirms the failure.

PR #148 remains Open / Draft / Unmerged. Main remains `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`: **NO_MAIN_ROLLBACK_NEEDED**.

## Review entry points

| Document | Purpose |
| --- | --- |
| [Final qualification](final-qualification.md) | Exact candidate/checkout/tree, all fifteen CI jobs, failed sustained-load gate and resumption boundary |
| [Change map](change-map.md) | Main-to-PR modules, commit relationships and compatibility |
| [Correction and optimization ledger](refactor-optimization-ledger.md) | Four accepted P2 corrections, RED/GREEN evidence and unadopted optimization claims |
| [Independent correction QA](independent-qa.md) | Separate reviewer original/corrected-source executions |
| [Final independent CI](final-ci-independent.md) | Source/step/hash reconciliation and explicit blocked verdict |
| [Final independent performance](final-performance-independent.md) | Raw-row quantiles, resources and bounded failure diagnosis |
| [Fresh security review](final-security-review.md) | All 25 source findings and 32 image warnings, concrete controls and unresolved native dependencies |
| [Evidence index](evidence-index.md) | Canonical reports, original archives/logs, hashes and preservation limits |
| [Risk register](risk-register.md) | Failed gate, security findings, operating limits and revalidation conditions |
| [Migration and rollback](migration-and-rollback.md) | Schema69, writer drain, backup/restore and database connection boundaries |
| [Continuation state](../continuation-state.md) | Exact checkpoint and permitted bounded resumption |

## Completed safe changes

Storage lease reuse now requires controller ownership. Queued TS/Dart refreshes and Jobs/storage-migration administrator actions retain their issuing session. The existing repeated-HTTP evidence reader enforces stage order, complete latency populations and pool4. The database guide distinguishes seven persistent diagnostic sessions from extra storage coordination sessions.

All four code correction areas passed separate old-source RED and corrected-source GREEN checks. Physical PostgreSQL17.9 ownership/adjacent migration tests, real HTTP SDK contracts and final whole-source functional jobs pass. The final frontend run passes 78 files / 458 tests, including the previously failing untouched ApiView file; the earlier failure stays recorded.

**New runtime performance optimizations adopted: zero.** Shared-host timing observations do not establish a new causal speed, CPU or RSS gain. Schema69, administrator layout, durability and acceptance thresholds remain unchanged.

The frozen candidate is `89e883fc700ef0250024427a1f09bb88affe0383`; all jobs actually checked out `b5c17b6f989bb11575e83544d37cc37fc6385d19`, with the identical tree `c9a4c21a136fe6f9de0d854d0f7453aedb6a5b55`. Later commits contain documentation/evidence only and do not claim a new runtime test.

## Preserved evidence and remaining decision

All original **617 evidence files** remain byte-for-byte intact, including eighteen duplicate-blob groups. The historical 547-drop failure's original archive was recovered. Fifteen current job logs, nine current original artifact ZIPs and exact source/security/performance records are retained. The large release binary archive has verified durable manifests/hashes and a documented hosted expiry.

Fresh source warnings remain 1 CRITICAL / 17 HIGH / 7 MEDIUM by scanner score, with path-specific dispositions. The image retains 21 MEDIUM / 9 LOW / 2 UNKNOWN records; thirty remain `needs_review`. Successful scanner execution is not a zero-vulnerability claim.

Raw load observations associate long tails with WAL/host-I/O and pool queueing, but do not establish a unique cause or a safe source fix. No blind retry was made. Another final load run requires a reproduced cause, actual source correction and separate QA. The task stops at this documented blocker; no background continuation is assumed.

Previous [integrated](../final-integrated-qualification.md) and [performance](../final-performance-qualification.md) qualifications remain historical and do not supersede the current failed gate.
