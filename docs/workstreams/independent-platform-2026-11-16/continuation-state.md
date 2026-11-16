# PgBase — subagent-first continuation

## Active checkpoint — 2026-11-16

This section supersedes the historical checkpoint below. Actual remote refs, source trees and CI evidence take precedence; a running task is not a passed gate.

- Observed published HEAD: `d77fd45de6975f55b047a3de9c4099e1805162ce`, branch `refactor/independent-platform-20261116`.
- Source tree: `81f741069604ef08c3d1339c800139d896fc60a9`. Fetched Actions merge commit `f763e5016bcdecf939cb307bd4c1b292a0306529` has this same tree.
- Main remains `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`; schema **68**; PR #148 open, Draft, unmerged.
- Latest observed full qualification [43804114855](https://github.com/yakisoba0728/PgBase/actions/runs/43804114855): **14 successful jobs; mixed-http job 133090810046 failed**.
- Browser/recovery job 133090810491 **passed**, including the 26-test flow. Its corrected managed-backup locator is not a new repair target.
- Release decision: **Not Ready**. Current-source load failure, integrated qualification of recovered fixes and incomplete audit boundaries remain open. Historical all-green runtime below does not qualify this source.

### Failed load and evidence

The unchanged gate requires **200 RPS for 600 seconds, pool 4, zero errors/drops/incomplete requests and end-to-end p99 <=250ms**. Observed: 120,000 scheduled, 119,822 completed, 178 dropped; end-to-end p99 1.209s, service p99 370ms, generator/queue p99 1.036s. Percentiles are not additive and are not a causal diagnosis. An earlier passing run with identical source/harness/workflow bytes on another host does not explain or erase this failure. Instrumented observer runs remain a separate diagnostic stratum.

- Failed-load artifact `14198176815`, source merge SHA `f763e5016bcdecf939cb307bd4c1b292a0306529`.
- Go source artifact `14197358999`; JS/TS `14196994897`; image SARIF `14196759277`; runtime assets `14196998628`; SBOM `14196973700`.
- Preserve raw bytes and digest/source identity before artifact expiry. Workflow success alone is not finding classification.

### Actual agents and ownership

The tool limit is four concurrent agents including the coordinator. **Three child agents were actually created and started in parallel**. Later roles are queued, not yet created.

| Agent | State | Scope |
| --- | --- | --- |
| A `performance` | Running | Load diagnostics, performance evidence; separate server/DB/pool/producer/queue/host/GC |
| B `backend` | Running | Realtime delivery cursor/context/retries; extension transaction boundaries |
| C `security` | Running | Auth request context, avatar redirects, file policy/ranges/cleanup, scan evidence |
| D Frontend/SDK | Queued | Delayed listener and backup callback ownership; contracts and unchanged visual design |
| E Reliability/QA | Queued | Independent review, load/data-integrity accounting, exact-source CI and coverage gaps |

Main checkout: `/workspace/scratch/79c920ff2ec3/PgBase`, branch `coordinator/subagent-first-20261116`. Child worktrees are sibling `PgBase-performance`, `PgBase-backend`, `PgBase-security`, each on its own `agent/*-20261116` branch. Coordinator owns shared-branch publication. Agents commit changes/evidence and report briefly; CI jobs are not AI reviewers.

### Unpublished recovery inventory

Old worktrees remain intact under `/workspace/scratch/a440b0968ef6/`. Old coordinator HEAD `93272446eb7b9b37868f83300f37dbb231724ff2` contains **24 unpublished commits after d77fd45d**, plus dirty workflow/root evidence. Do not reset them or count them as accepted.

- Realtime: `564d1919`, `1e005485`, `b17f83e2`, formatting and historical review evidence.
- SDK listener ownership `01dae8a4`; administrator backup callbacks `bad579f9`.
- Files/request policy/ranges `28a0ae8b`; avatar redirects `a52d57c2`; upload/variant cleanup `64b1940f`.
- Aligned DB load diagnostics `93272446`; preregistered performance evidence, QA outcome tooling, browser configuration and operations documentation.

Recover selectively, freshly test and independently review. Existing raw evidence remains historical with its original source identity.

### Publication, persistence and next actions

First integration batch recovers the aligned PostgreSQL observer and separate generator/worker/pool/host diagnostics from A (`c298a9f4`, `17203060`). B independently reviewed tree `e711982abeeb767bfcb88e1b321984e3e24142db`: all arrival, pool, zero-loss/error, latency and durable-count criteria remain unchanged. Its 100-arrival smoke **failed** p99 under concurrent compilation; it is preserved and is not a qualification. The 600-second instrumented local baseline is running while team builds pause. Hosted diagnosis will run against the original runtime plus instrumentation before the recovered runtime fixes are integrated.

HTTPS Git reads work; nonmutating push/CLI probes found no shell write credential. Publish through the connected GitHub Git-data API, preserve logical commits, compare resulting trees with local trees, record local/published SHA mappings, and update only the work branch with exact expected HEAD and `force=false`. Do not request or borrow real credentials.

No chat-independent autonomous coding runner was verified. GitHub Actions supports hosted validation after publication, independently of this chat; this does not imply that child implementation continues after the conversation ends.

1. Collect A/B/C commits and evidence, cross-review, then integrate; create D/E when slots free.
2. Recover remaining verified evidence/operations docs without rerunning completed work gratuitously.
3. Publish and qualify the exact integrated source; retain run/job IDs, tested merge SHA/tree and artifacts. Diagnose new failures before rerunning.
4. Update this file at every significant integration/review/CI boundary. Keep **Not Ready** while important failures or mandatory unverified gates remain.

No main push, automatic merge, force push, production access/deployment, release/tag or SDK package publication is authorized.

---

## Historical Phase 2 recovery (superseded by active checkpoint above)

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
