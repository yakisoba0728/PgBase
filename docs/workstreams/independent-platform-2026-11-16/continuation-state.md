# Phase 2 recovery and continuation

This is the restart entry point for PR [#148](https://github.com/yakisoba0728/PgBase/pull/148). Read this document together with the actual branch HEAD and Actions results. Historical green runs do not qualify later source changes.

## Recovery checkpoint — 2026-11-16

- Published HEAD observed at recovery: `5285e9963251a73656dd22eb2f8a9254ba21678e`, tree `5fa2e8a1df2342ffd06f08efa87709283d2549b6`.
- Branch: `refactor/independent-platform-20261116`; main remains `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`. PR is open, Draft and unmerged; system schema is **68**.
- Last all-green historical runtime: `5e4b71d1f602c9c136809bf65f2375bf75fc4a9f`, run [43780150632](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632). It does not qualify schema 68 or the later runtime.
- Latest qualification at recovery: [43798704195](https://github.com/yakisoba0728/PgBase/actions/runs/43798704195), **14 successful jobs, one failed browser/recovery job**. Routine Backend, Frontend and Security workflows passed on the published HEAD.
- Release decision: **Not Ready** until the required integrated gates and unresolved performance/coverage questions are reconciled.

## What was actually recovered

The previous local worktrees survived. They were inspected read-only; no previous uncommitted changes were reset, overwritten or automatically counted as published work. The previous coordinator had local HEAD `d50d984a7f9f71eb9b0ffd82e8ae4fade0900704`. A publication ledger identifies local `10be96595f0710220810a33c385c9e32a0786835` as tree-identical to published `5285e996`.

Local commits after that publication include a corrected backup locator, independent QA/security/performance evidence, a field-order isolation experiment and Go outcome-summary tooling. Uncommitted performance reports, raw evidence and an architecture/operations draft also survived. They are being verified and selectively recovered; their existence is not a test pass or an exhaustive audit.

Fresh isolated worktrees and **three actual new child agents** were created for Frontend/SDK, Backend/Architecture and Performance. Earlier agent sessions were not restored. The coordinator owns publication, source reconciliation, security/QA coverage and the release decision. Independent security and QA work will use subsequent available slots.

## Immediate correction

`ui/admin/e2e/managed-backups/backup.spec.ts` selected both the completed-job notice and the archive detail panel using the accessible name `Backup`. The recovered correction restricts the region to the one containing the actual `Download` link and asserts exactly one match. No test is skipped and the download, SHA-256, upload, restore, row, ETag and attachment checks remain.

Source review, strict TypeScript checking and discovery of all 26 browser tests passed for the recovered locator change. Actual browser completion is still pending: the previous CI stopped at the ambiguous locator; that failure neither proves data corruption nor proves successful restoration. The local environment has no installed Playwright Chromium or Docker, so the shipped-image browser/recovery flow must run in the isolated hosted fixture.

## Coverage and remaining work

| Area | Recovered state | Next required evidence |
| --- | --- | --- |
| Realtime, jobs, transactions | Schema 68 atomic admission, indexed pending predecessors, retention prefix, generation/lease fencing, observer and savepoint fixes exist in published source | Reconcile reviewed boundaries and investigate remaining broker/delivery and failure paths |
| Security | Archive/OIDC fixes and fresh-source scan artifacts exist; finding counts must remain distinct from exploitable vulnerabilities | Verify latest scan/source identity and authorization/security gaps independently |
| UI/SDK | Auth ownership, disposer/SSE and async admin fixes exist; managed-backup browser scenario unfinished | Execute full corrected backup flow, complete current E2E and check remaining lifetime contracts |
| Performance | Preserved evidence includes 2M-row index diagnostics, native and hosted HTTP comparisons, field-order isolation and open-loop repeats | Independently verify raw identities and limits; do not rerun already completed work without a concrete reason |
| Architecture | Targeted prior reviews exist | Record actual path/boundary coverage; whole-module audit is not complete |
| Reliability | Prior hosted fault, multi-process and recovery scenarios exist | Verify latest-source qualification and gaps in pool/network/storage/slow-client behavior |

The historical HTTP/c4 adverse result is retained. A later run on a different hosted machine does not by itself explain or repair that result. PostgreSQL `pg_stat_io` is cluster-wide; observer work and co-resident load must not be attributed to an individual request.

## Resume procedure

1. Read current PR metadata, `git log` and this file. Check for concurrent branch changes before publishing; use an expected-head check and never force push.
2. Inspect the newest `Full source qualification` run on the actual branch HEAD. Preserve job IDs, tested source/tree and raw artifacts. Finish the managed-backup scenario before declaring the browser gate green.
3. Read `final-backend.md`, `final-security.md`, `final-frontend-sdk.md`, `final-qa.md`, `final-performance-independent.md`, `final-performance-protocol.md` and their evidence. Older overview documents still contain schema-67/historical statements and must be interpreted with their revision scope.
4. Recover verified local commits/evidence without duplicating published trees; record a local/published SHA and tree mapping when connector publication changes commit identities.
5. Continue actual unfinished audit, fixes and experiments. Update this document at substantive integration/qualification boundaries.

No main changes, automatic merge, production access, deployment, release, tag or SDK package publication is authorized by this continuation.
