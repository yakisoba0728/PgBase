# PgBase final master consolidation

Status: **IN_PROGRESS — scoped fixes and independent QA complete; final whole-source qualification pending.**

PR148 remains Open / Draft / Unmerged. Main remains `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`: **NO_MAIN_ROLLBACK_NEEDED**.

## Review entry points

| Document | Purpose |
| --- | --- |
| [Final qualification](final-qualification.md) | Exact candidate/checkout/tree, mandatory CI and sustained-load verdict |
| [Change map](change-map.md) | Main-to-PR modules, commit relationships and compatibility |
| [Correction and optimization ledger](refactor-optimization-ledger.md) | Four accepted correction areas, RED/GREEN evidence, rejected performance claims |
| [Independent QA](independent-qa.md) | Separate reviewer executions against original and corrected sources |
| [Evidence index](evidence-index.md) | Canonical reports, immutable raw evidence and verified preservation |
| [Risk register](risk-register.md) | Security findings, operating limits and revalidation conditions |
| [Migration and rollback](migration-and-rollback.md) | Schema69, writer drain, backup/restore and compatibility boundaries |

## Current round

- Storage I/O/publication leases now require the receiving controller's ownership.
- TS/Dart queued refreshes retain their original user-session lineage.
- Jobs and storage-migration UI actions and callbacks retain their issuing session scope.
- The repeated-HTTP evidence parser enforces its existing workload sequence, sample populations and pool4 budget.
- The database guide distinguishes the seven persistent diagnostic sessions from additional storage coordination sessions.

All four code correction areas passed independent old-source RED and corrected-source GREEN checks. Physical PostgreSQL17.9 ownership/adjacent migration checks and real HTTP TS/Dart contracts passed. The local whole UI run's untouched ApiView timeout remains recorded; final complete UI CI must pass.

**New runtime performance optimizations adopted:0.** Existing allocation results were rechecked; elapsed-time improvement remains inconclusive under shared-host load. Schema69 and the visible administrator layout are unchanged.

The original617 evidence files (615 workstream plus2 older audit files) remain byte-for-byte intact. Eighteen equal-blob groups retain their original contexts; no historical evidence was deleted or relabeled as a new pass.

[Initial inventory](../evidence/final-master/initial-inventory.json), [live initial GitHub provenance](../evidence/final-master/github-provenance.json), and [local-to-published source mapping](../evidence/final-master/publication-map.json) distinguish old qualification, this round's author/checkpoint identities, and final CI.

## Qualification procedure

The next frozen commit receives one complete Full source qualification and one fixed200 RPS×600-second gate:120,000 complete arrivals,zero drops/errors/incomplete, end-to-end p99≤250ms. Every actual job/step and raw artifact must be verified, including source/checkout tree equality and fresh scanner findings. A successful invocation alone is insufficient.

[Previous integrated qualification](../final-integrated-qualification.md) and [previous performance qualification](../final-performance-qualification.md) remain historical. No previous READY recommendation is reused for the new candidate before its mandatory gates complete.
