# Performance blocker recovery — 2026-11-17

## Decision: BLOCKED / NOT_READY

The bounded recovery is complete. Existing raw evidence localizes substantial delay inside durable PostgreSQL COMMIT and the associated connection/request queues, but it does **not** establish the cause of the slow COMMIT or a safely correctable software defect. No runtime patch was adopted. The one conditionally permitted new final qualification was **not launched**, because defect reproduction, a safe correction, and independent correction approval are absent.

The mandatory result remains [Full source qualification 43982303611](https://github.com/yakisoba0728/PgBase/actions/runs/43982303611), attempt 1: **14 successful jobs / one failed mixed-http job**, with **92 of 93 required substantive steps passing**. End-to-end p99 remains **588.031143 ms**, exceeding the unchanged **250 ms** limit. This report does not substitute focused control tests for the failed gate.

PR #148 remains the single Open / Draft / Unmerged integration PR, with auto-merge disabled. Main is untouched. No merge, deployment, release, tag, package publication, threshold relaxation, or unchanged-source performance retry was performed.

## Source identity and recovered state

| Identity | Exact value and scope |
| --- | --- |
| Starting GitHub PR HEAD | `77bc070047c5a19d68439b75cb7b65bcde5d1c9a` |
| Starting complete Git tree | `07fc4145c03d636c1416e2e838ad701cebc3b03b` |
| Untouched main / merge base | `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a` |
| Published recovery checkpoint | `8e3c77372003bc3d01a78cc1ef9120f4d06bdfa6` |
| Checkpoint tree | `309bdf4b16f880fca4ab4fd2115c3a935f5f0b60` |
| Published three-agent evidence checkpoint | `c16370534fcce6b9859d99b6e8a5bb966a16150d`, tree `6f5de55b19b22be953dc759299732ec20ad30044` |
| Source of the new focused control tests | `77bc070047c5a19d68439b75cb7b65bcde5d1c9a`, unchanged |
| Last full-CI runtime candidate | `89e883fc700ef0250024427a1f09bb88affe0383` |
| Actual checkout in all fifteen existing CI jobs | `b5c17b6f989bb11575e83544d37cc37fc6385d19` |
| Equal full-CI candidate / checkout tree | `c9a4c21a136fe6f9de0d854d0f7453aedb6a5b55` |
| Final runtime source | Unchanged; all 1,932 non-document Git entries retain the full-CI candidate's blobs and modes |
| Final documentation/evidence HEAD | Recorded as an exact immutable SHA and tree in the PR #148 publication handoff after this report is committed; it is not a new tested runtime candidate |

Both the GitHub connector and `git ls-remote` confirmed the starting refs. The [starting snapshot](../evidence/final-master/performance-blocker-recovery/start-github-state.json) and [published checkpoint snapshot](../evidence/final-master/performance-blocker-recovery/recovery-checkpoint-state.json) preserve the observations. The current report, three agent evidence directories, and refreshed evidence manifest are additive documentation/evidence; their publication does not claim a fresh full CI execution.

## Bounded hypotheses and findings

Only the three requested hypotheses were investigated. [Agent A's report and reproducer](../evidence/final-master/performance-blocker-recovery/agent-a/diagnosis.md) and [independent QA](../evidence/final-master/performance-blocker-recovery/agent-c/independent-qa.md) retain the detailed calculations and limitations.

| Hypothesis | What existing evidence establishes | What remains unproved |
| --- | --- | --- |
| A — WAL / COMMIT | Seven sampled active COMMIT entries already have query ages over 250 ms; the largest is 284.166 ms. Of 168 observed blocking edges, 117 end at a WAL-waiting COMMIT. | Query age is not exact WAL wait duration. Samples are not seven uniquely identified transactions. They do not establish why durable COMMIT is slow or that application code adds unnecessary WAL. |
| B — DB pool / request queue | Pool occupancy reaches four in 1,604 observations; 1,038 also contain an active PgBase backend waiting on WAL. HTTP-service p99 is 277.767335 ms and worker-queue p99 is 425.904449 ms. | No avoidable connection retention, incorrect release, unnecessary transaction lifetime, or removable safe lock was reproduced. Different percentile series must not be added together. |
| C — environment / observer | Historical pass and current failure used different hosts. Measured request paths, workload, observer, workflow, and dependencies match. Observer query time decreased from about 4.377 s to 3.272 s, and its sampling lag p99 did not grow. | Host differences are not a controlled explanation; low total observer time does not prove zero perturbation. Existing observations cannot separate storage/host effects from workload dynamics with causal certainty. |

The 1,038 count requires request-pool acquired count four, `application_name=pgbase`, `state=active`, and a WAL wait event in `{WalSync,WALWrite,WalWrite,WalInitSync,WalInitWrite}`. Omitting the active-state filter gives 1,045; the extra seven are idle-in-transaction transition observations (four WalWrite, three WalSync). Neither count is a number of unique blocked requests.

Compared with the historical pass, WAL bytes, commit counts, and row-work totals are similar. Source comparison from historical tested source `4091c3be20ec8db177886a2a85550924fce37ff5` to the final tested candidate found no changes in the measured database/query/record/membership/auth/schema paths, open-loop/mixed harness, observer, relevant workflows, or Go dependencies. The only changed production Go file adds storage-controller lease ownership guards; the measured workload does not perform file operations. This narrows the investigation but does not prove the application is free of latent performance defects.

## Implementation decision and focused runtime controls

**Runtime changes: zero.** No defect was reproduced, so there is no correction RED/GREEN pair and no candidate patch for independent approval. Removing the schema `FOR SHARE` protection would weaken DDL/snapshot fencing and was not adopted. Authentication scans release their pooled connections; owned read/write transactions complete before HTTP serialization; the observer uses its own connection. These inspected paths do not demonstrate an avoidable pooled hold.

Agent B executed one bounded group of existing tests on a fresh, loopback-only PostgreSQL **17.9** cluster using Go **1.26.11**, `GOMAXPROCS=4`, and the race detector. The six parent tests cover transaction cleanup on callback error/panic/expired deadline, schema pin/DDL behavior, listener capacity/reconnection, and the bounded read kernel. **Six parent tests plus three read-query subtests passed across three packages; zero failed or skipped.**

The listener control explicitly asserts a request pool of four and proves that all four can be acquired while its dedicated listener reconnects. Other controls retain their own pre-existing pool configurations; they are not described as a new mixed-load qualification. `fsync`, `synchronous_commit`, and `full_page_writes` remained ON before and after. Test databases were cleaned up and the disposable cluster stopped.

Two fixture issues are retained in Agent B's evidence: unsupported Unix-domain socket startup, then connection-refused results when a server process did not persist across executor calls. Those failed attempts did not reach the application test paths and are not defect RED evidence. Running startup, the existing controls, and teardown in one command corrected the fixture without changing product code or durability. The final commands, raw results, and source identity are in the [implementation-control report](../evidence/final-master/performance-blocker-recovery/agent-b/runtime-review.md).

## Independent QA and unchanged performance contract

Agent C worked from its own checkout at the exact starting SHA and authored no runtime implementation. It independently read both original ZIPs, recalculated the failing request population, inspected configuration, reconciled source identity, and reexecuted the retained-CI evidence verifier offline.

| Gate or evidence property | Independently verified result |
| --- | --- |
| Offered load / duration | 200 RPS × 600 seconds |
| Scheduled / completed | 120,000 / 120,000 |
| Dropped / unexpected errors / incomplete | 0 / 0 / 0 |
| End-to-end p99 / limit | **588.031143 ms / 250 ms — FAIL** |
| Requests over 250 ms | 7,726 |
| Request pool | Four throughout all 6,000 retained observations |
| PostgreSQL durability | fsync / synchronous_commit / full_page_writes ON before and after |
| Current original ZIP SHA-256 | `560f35fa19fb6a18c96c7bc517759beeedb6604dc2fbdd2de044c38113e7e393` |
| Historical failed original ZIP SHA-256 | `d2411a4837ffd38a5fd3c2b67423240f07de4eed9ed0f5d4038af59ca628cf6e` |
| Historical failed accounting | 119,453 completed / 547 dropped; remains a failure |
| Retained-CI verifier | Evidence integrity PASS; qualification NOT_READY_BLOCKED, expected exit 1 |
| Correction approval | Not available: no reproduced defect / no safe patch |
| New final performance / full-CI executions | **0 / 0** |

Independent evidence approval is not correction approval. The unchanged controls cannot provide missing before/after defect evidence. Historical PostgreSQL 16.13/17.9/18.3, Go, SDK, administrator UI, browser, recovery, platform, and security results remain bound to their original source; no new whole-project execution is claimed.

Exactly three subagents were created for this recovery, with a maximum of three concurrently active (four agents including the Coordinator). Their detailed evidence is committed under `performance-blocker-recovery/agent-a`, `agent-b`, and `agent-c`.

## Security, integrity, evidence retention, and operating limits

Runtime, schema, dependencies, authorization, storage ownership, Jobs fencing, SDK/session behavior, durability, pool size, load, queue/worker settings, latency accounting, and acceptance thresholds are unchanged. No new security disposition or warning suppression was introduced. Existing scoped CodeQL dispositions, image findings requiring review, and operational limits remain in the [risk register](risk-register.md) and [security review](final-security-review.md).

The 588 ms failure, historical 547-drop failure, their original ZIPs, and previous functional/security reports remain intact. The prior 792-entry manifest is preserved verbatim as [evidence-manifest-before.json](../evidence/final-master/performance-blocker-recovery/evidence-manifest-before.json), SHA-256 `ad6087d85b152ff17ce96521e1e6471f392d05af4b56b365427fc945fcfa29cd`. The existing manifest builder refreshes current document/evidence hashes while continuing to enforce all 617 original evidence files' byte identities. No new measurement protocol or verifier formula is adopted.

Schema 69 upgrades still require coherent backups, drained old workers, explicit migration/readiness checks, and the existing recovery contract. Uncertain storage intents require remote-upload quiescence evidence; Jobs fencing does not guarantee exactly-once external effects. Nothing in this recovery authorizes production DB access or deployment.

## Conditions for a further recovery

1. Collect a short, discriminating trace that identifies transaction/query start identity and maps request connection acquisition, transaction/COMMIT, and release times. The existing observer lacks the identity/timestamps needed to prove one long retained transaction from repeated hashes.
2. Use that trace with aligned PostgreSQL/host I/O observations to distinguish unavoidable durable COMMIT time from unnecessary application holding/work. Compare the same relevant source and workload on a controlled host when attributing an environment effect; do not seek a favorable runner by repeated final-gate attempts.
3. Proceed only if the evidence reproduces a concrete avoidable software problem. Demonstrate the failure, make the smallest safe correction, and independently verify the same case plus relevant race/PostgreSQL integrity controls. Preserve request pool four, PostgreSQL durability, original workload/accounting, and the 250 ms limit.
4. Freeze that independently approved source and execute the existing complete qualification and 200 RPS × 600-second gate once. Require 120,000 completed, zero dropped/unexpected errors/incomplete, and end-to-end p99 ≤250 ms. Bind the result to the exact source SHA/tree and retain any failure.

If these prerequisites cannot be met, the decision remains **BLOCKED / NOT_READY**. No continuing background benchmark or automatic retry is scheduled.
