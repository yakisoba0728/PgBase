# Performance blocker recovery — independent QA

## Decision

**Evidence audit: PASS. Runtime correction approval: NOT AVAILABLE. Final gate authorization: NOT APPROVED. Overall: BLOCKED / NOT_READY.**

This reviewer used a separate worktree at the exact starting remote revision. No runtime, benchmark, workflow, security rule, durability setting or acceptance threshold was changed. No 600-second workload was rerun. Existing full CI remains the source-bound qualification; this report does not claim a new Go/SDK/browser/PostgreSQL full-suite execution.

## Source identities and preservation

| Identity | Value |
| --- | --- |
| Recovery start | `77bc070047c5a19d68439b75cb7b65bcde5d1c9a` |
| Recovery start tree | `07fc4145c03d636c1416e2e838ad701cebc3b03b` |
| Existing tested candidate | `89e883fc700ef0250024427a1f09bb88affe0383` |
| Existing actual checkout | `b5c17b6f989bb11575e83544d37cc37fc6385d19` |
| Equal candidate/checkout tree | `c9a4c21a136fe6f9de0d854d0f7453aedb6a5b55` |
| Existing full CI | [43982303611](https://github.com/yakisoba0728/PgBase/actions/runs/43982303611), attempt 1 |

Git comparison confirms **zero non-document changes** between the tested candidate and recovery start, across 1,932 non-document tree entries. Modes/blobs of these unchanged entries preserve existing Storage, Realtime, Jobs, Auth, SDK and UI source; no historical result is extended to a different runtime tree. The reviewed workload, observer, workflow, record-operation transaction and collection mutation-lock files also have identical blobs between the earlier passing source `4091c3be` and current failed source `89e883fc`.

The existing evidence manifest verifies **792 indexed files, 617 original evidence files and 18 duplicate groups** at this checkpoint. Both failure archives retain their original byte streams:

- Current failed ZIP: 3,717,310 bytes; SHA-256 `560f35fa19fb6a18c96c7bc517759beeedb6604dc2fbdd2de044c38113e7e393`.
- Historical 547-drop failed ZIP: 3,451,697 bytes; SHA-256 `d2411a4837ffd38a5fd3c2b67423240f07de4eed9ed0f5d4038af59ca628cf6e`.

## Independent raw-row checks

New read-only script [verify_retained_load.py](verify_retained_load.py) directly reads the two original ZIPs. It does not import the implementation author's analysis or previous performance verifier. Its complete output is [retained-load-verification.json](retained-load-verification.json).

It checks each CSV index, exact 5 ms scheduled offset, balanced owner assignment, registered operation mix, expected successful HTTP status, queue/generator decomposition and latency population. Quantiles use the existing nearest-rank rule over the recorded attempted population; no latency, request or error is discarded to change gate eligibility.

| Current failing workload | Independent result |
| --- | ---: |
| Scheduled rows / unique contiguous indices | 120,000 / 120,000 |
| Completed / attempted | 120,000 / 120,000 |
| Dropped / errors / incomplete | 0 / 0 / 0 |
| End-to-end p50 / p95 / p99 | 6.264600 / 319.624000 / **588.031143 ms** |
| HTTP service p99 | 277.767335 ms |
| Worker queue p99 | 425.904449 ms |
| Generator p99 | 1.065102 ms |
| Arrivals over 250 ms | 7,726 |
| Pool maximum in all observer samples | 4 |
| Observer samples / configured cadence | 6,000 / 100 ms |
| `fsync`, `synchronous_commit`, `full_page_writes` | ON before and after |

The historical failure still has **119,453 completed and 547 dropped/error rows out of 120,000**, so no successful latency quantile can make that execution pass. Its older `full_page_writes` value was not captured and stays **NOT_RECORDED**, rather than being inferred from the newer artifact.

The raw status counts alone are not a data-integrity proof. Exact-source inspection confirms that the existing workload also checks physical row/create/update counts, owner isolation, relation identity, distinct IDs, ETag behavior, post-load cursor traversal and capped public totals. Existing current-run logs retain those checks; only the p99 gate emits a failure.

## Independent localization of the observed delay

[verify_causal_localization.py](verify_causal_localization.py) independently joins backend PID blocker references inside each existing observation. [causal-localization-verification.json](causal-localization-verification.json) contains the exact checks and source blob comparisons.

- 1,604 observations show all four request connections acquired. **1,038** of those observations contain an **active** `pgbase` backend waiting on `WalSync`, `WALWrite` or `WalWrite`.
- There are 168 recorded blocking edges; **117** identify a blocker whose query prefix is `commit`. Those 117 blockers show 80 `WALWrite`, 36 `WalSync` and one `WalWrite` wait.
- The maximum sampled active COMMIT age is **284.166 ms**, with `WalSync`, at approximately 159.750 seconds after load start.
- 164 of the 168 blocked-query prefixes are `select pg_advisory_xact_lock($1)`.
- The observer's own query-time p99 is 2.296910 ms. This is elapsed query duration, not observer CPU cost and not a controlled estimate of observer overhead.

These observations support a concrete local chain: some transactions retain their transaction locks while their COMMIT waits for WAL; downstream request-pool and worker queues then accumulate. **They do not establish why the WAL work was slow or identify a safely removable software defect.** Query age is not complete transaction duration. Repeated query hashes are not transaction IDs, and the recorded rows lack `query_start`/`xact_start` correlation identities. Activity and blocker fields can also reflect transitions during sampling.

The initial comparison counted 1,045 pool-full observations with WAL-related fields without requiring `state='active'`, so its expected-1,038 audit assertion failed. The seven extra observations contain idle-in-transaction/WAL transition combinations. The reviewer reconciled the definition with the raw rows and recorded both populations; the active-state check now passes. This correction concerns an offline diagnostic count and changes no benchmark formula or gate result.

## Transaction safety review

The focused source review covers `pgbase/open_loop_load_integration_test.go`, `pgbase/open_loop_diagnostics_integration_test.go`, `.github/workflows/full-qualification.yml`, `internal/recordops/transaction.go`, `internal/recordops/collection_mutation_lock.go` and the current-versus-historical four-line storage ownership correction.

`createTx`/`pinnedTx` intentionally hold the shared schema pin while executing the operation, retain operation savepoint behavior for borrowed transactions, and select the isolation level needed for idempotent replay and delete-graph visibility. The collection mutation lock is explicitly transaction-scoped and coordinates create/truncate ordering. A lock or connection cannot be released before the transaction outcome merely to reduce sampled wait time without redesigning those correctness contracts. No evidence reviewed here permits removing the schema pin, dropping synchronous COMMIT, reducing audit/outbox work, or moving an authorization or integrity check outside its protected transaction.

No candidate correction has been submitted, so there is no RED/GREEN regression pair to independently approve. Existing control tests executed by another agent, if successful, can validate their specific unchanged invariants; they do not reproduce or repair the 588 ms failure and do not justify a new final gate.

## Existing CI reconciliation

The supplied retained-CI verifier was executed independently against the current immutable logs/metadata. [retained-ci-verification.json](retained-ci-verification.json) reports evidence integrity **PASS**, no integrity errors, no pending jobs, and qualification **NOT_READY_BLOCKED**. Its exit 1 is expected because the original mixed-http job failed; it is not a new runtime execution failure.

The recorded field `required_successful_steps: 93` is the verifier's configured required-step count, **not** a count of 93 passing steps. The original actual outcome remains **92/93 required steps, 14 successful jobs and one failed mixed-http job**.

Existing scanner findings and operating restrictions remain unchanged, including the 30 native dependency entries needing review, uncertain Storage intent quiescence, old-worker retirement, external Jobs effect idempotency and real-provider limitations. No fresh security scan or blanket risk acceptance is claimed here.

## Reproduction and stopping condition

From the repository root, run the two new scripts with the repository path and a temporary output path. Both return 0 when the original failure evidence is reproduced accurately:

```bash
python3 docs/workstreams/independent-platform-2026-11-16/evidence/final-master/performance-blocker-recovery/agent-c/verify_retained_load.py . /tmp/pgbase-recovery-load.json
python3 docs/workstreams/independent-platform-2026-11-16/evidence/final-master/performance-blocker-recovery/agent-c/verify_causal_localization.py . /tmp/pgbase-recovery-causal.json
python3 docs/workstreams/independent-platform-2026-11-16/evidence/final-master/independent-final-ci/verify_retained_ci.py docs/workstreams/independent-platform-2026-11-16/evidence/final-master/final-ci /tmp/pgbase-recovery-ci.json
```

The last command deliberately returns 1 for the retained failed qualification. No final performance run is approved until a specific defect has an actual before-fix reproduction, a safe minimal correction and an independent same-condition verification. More runs on unchanged source, favorable-host selection or unvalidated removal of transaction safeguards are not acceptable substitutes.
