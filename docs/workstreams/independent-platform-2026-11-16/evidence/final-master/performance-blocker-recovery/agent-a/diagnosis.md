# Agent A — bounded performance-blocker diagnosis

## Decision

**BLOCKED / NOT_READY.** Existing raw evidence localizes the observed long tail to PostgreSQL COMMIT queries waiting on WAL and the associated request-pool / worker queues. It does **not** establish why durable COMMIT became slow, a newly introduced hot-path regression, or unnecessary connection holding that can safely be corrected. No runtime patch, short workload experiment, or final-gate rerun was justified or performed by Agent A.

This work is limited to the three requested hypotheses. It is not a fresh platform audit. All preceding source, failure archives, security findings, and source-scoped functional qualifications remain intact.

## Source and evidence identity

| Role | Identity |
| --- | --- |
| Recovery starting HEAD | `77bc070047c5a19d68439b75cb7b65bcde5d1c9a` |
| Starting tree | `07fc4145c03d636c1416e2e838ad701cebc3b03b` |
| Failed candidate | `89e883fc700ef0250024427a1f09bb88affe0383` |
| Failed candidate / actual CI checkout tree | `c9a4c21a136fe6f9de0d854d0f7453aedb6a5b55` |
| Historical passing candidate | `4091c3be20ec8db177886a2a85550924fce37ff5` |
| Failed run | [43982303611](https://github.com/yakisoba0728/PgBase/actions/runs/43982303611) |
| Historical passing run | [43861850923](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923) |

The original failed ZIP is `../../final-ci/artifacts/final-mixed-http.zip`, SHA256 `560f35fa19fb6a18c96c7bc517759beeedb6604dc2fbdd2de044c38113e7e393`. The passing ZIP is `../../../final-integrated/performance/final-gate/pgbase-final-mixed-http.zip`, SHA256 `82f0397e6b0ab5e1d71c96ebc2c4af35137fc8638d60c3c262b8208d5935d84e`. These are relative to this report. No original archive was modified or extracted over tracked files.

[`analyze_existing.py`](analyze_existing.py) reads these two archives directly, verifies ZIP CRCs and raw accounting, and records member hashes, source comparisons, counters, latency distributions, minute windows, observer timings, sampled blockers, and exact locations of the over-250 ms COMMIT samples in [`raw-comparison.json`](raw-comparison.json).

The independent QA agent separately recalculated the critical numbers. This report makes no claim that either old CI or the offline analysis validates a newly modified runtime.

## Hypothesis A — PostgreSQL WAL / COMMIT wait

| Raw measurement | Historical pass | Failed final |
| --- | ---: | ---: |
| End-to-end p99 | 22.221162 ms | 588.031143 ms |
| Completed requests | 120,000 | 120,000 |
| Errors / drops / incomplete | 0 / 0 / 0 | 0 / 0 / 0 |
| Requests above 250 ms | 0 | 7,726 |
| Active COMMIT samples waiting on WALWrite | 54 | 2,806 |
| Active COMMIT samples waiting on WalSync | 1,303 | 1,444 |
| WAL bytes, cumulative delta | 432,024,542 | 432,181,885 |
| WAL sync count, cumulative delta | 183,820 | 178,309 |
| Mean WAL sync time, cumulative ratio | 0.178575 ms | 0.803968 ms |
| Database transaction commits, cumulative delta | 431,817 | 431,698 |
| Database updated tuples, cumulative delta | 120,732 | 120,715 |

Seven failed-run activity entries show an active COMMIT query already older than 250 ms: five while sampled on `LWLock/WALWrite` and two on `IO/WalSync`. The largest query age is **284.166 ms**, observed while waiting on WalSync. This is direct observation of an already-long COMMIT query. It is **not** a measurement that WalSync itself lasted 284.166 ms; the trace contains sampled query age and current wait, not an exact per-query wait history.

The volume of committed transactions, updated rows, and WAL is close between runs. There is no observed explosion in database work corresponding to the latency increase. This comparison does not prove every SQL plan, lock interleaving, dirty-page state, filesystem queue or physical write cost was equal.

**Finding:** the immediate long-tail location is supported. The origin of the slower durable COMMIT is unresolved. Disabling durability or claiming a hardware-only failure would go beyond the evidence.

## Hypothesis B — pool and worker queue

All 6,000 failed-run pool observations retain `MaxConns=4`; the final pool returns to four idle connections. Pool-full samples rise from **93 to 1,604**. Aggregate pool waits rise from **30.171 seconds to 2,653.122 seconds**. Failed worker-queue p99 is **425.904449 ms**, while HTTP-service p99 is **277.767335 ms**. These are different distributions and cannot be added to reconstruct end-to-end p99.

Of the 1,604 pool-full observations, **1,038** also contain at least one backend with all of:

- `application == "pgbase"`;
- `state == "active"`;
- `wait` in `{WalSync, WALWrite, WalWrite, WalInitSync, WalInitWrite}`.

The comparable historical count is 26 of 93. Omitting the `active` filter gives 1,045 failed observations: the extra seven show only `idle in transaction` with a WAL wait (four WalWrite, three WalSync). Activity fields and pool counters are not an atomic global snapshot, so those transitions must not be silently counted as active WAL work.

There are **168 sampled blocking edges** in the failed trace. In **117** of them the blocker query is COMMIT and its sampled wait is WALWrite (80), WalSync (36), or WalWrite (1). The frequent advisory-lock wait is therefore often downstream of a committing transaction. This does not justify releasing the advisory lock early: its transaction ownership is part of the mutation consistency protocol.

The trace cannot identify an unnecessarily retained connection from repeated query hashes. It omits `query_start`, `xact_start`, operation IDs and acquire/release spans; an equal hash in successive 100 ms snapshots can represent different transactions. The implementation reviewer inspected transaction completion and authenticated read/mutation paths separately. No source correction is established by the pool counters alone.

**Finding:** queue amplification is supported; connection leak, premature-release opportunity, or unnecessary hold is unproved.

## Hypothesis C — environment and observer

The passing host reports Intel Xeon Platinum 8573C; the failing host reports AMD EPYC 9V74. Both report four logical CPUs, Microsoft virtualization, Go 1.26.11, and PostgreSQL 17.9. Durability is ON in both before/after snapshots. These are different hosts, not a controlled hardware experiment.

Both archives contain 6,000 observer samples without a diagnostic error. Observed query elapsed totals are **4.377 seconds passing / 3.272 seconds failing**. Observer query p99 is **2.700880 / 2.296910 ms**; observer scheduling-lag p99 is **1.549822 / 1.157238 ms**. Increased observed sampler duration or missed ticks therefore does not explain the current increase. These totals are elapsed time, not isolated observer CPU; without an on/off causal experiment they do not prove zero observer interaction with locks or I/O.

Direct Git comparisons show no changes from the passing candidate to the failed candidate in:

- the three HTTP-load / diagnostics harnesses and `full-qualification.yml`;
- `go.mod`, `go.sum`, and the database, query, recordops, membership, schema, publicauth, and observability directories.

The only changed Go production path is the previously approved storage lease ownership guard. The measured text/integer/relation workload performs no file operations. Embedded UI assets changed, so complete executable identity is not asserted. The raw request kind/owner/status schedule, 5 ms arrival spacing, full latency population, and all latency decomposition checks match in both runs.

**Finding:** comparability limits remain. Neither an observer failure, sole hardware cause, nor measured-path code regression is established.

## Bounded conclusion and next admissible step

No new runtime experiment was launched because the review did not identify a specific needless operation or incorrect lifetime to test. Repeating the full workload would chiefly test another host/interleaving and would not resolve the missing causal distinction. There is no safe patch to submit for independent approval and therefore no basis for consuming the conditional final-gate run.

A future recovery must first name a concrete path and acquire evidence that distinguishes it from normal durable COMMIT. For example, a bounded diagnostic could correlate request acquire/begin/COMMIT/release spans with server `query_start`/`xact_start`, or reproduce a specific duplicate query / unnecessary hold under an isolated deterministic regression. It must preserve ownership and transaction fences, pool four, durability, workload accounting, and failed outcomes. Shortened fixtures are not automatically comparable to the original 120,000-arrival fixture. Merely obtaining a fast-host pass is insufficient.

Only a reproduced software defect, a minimal safe correction, and separate QA approval can justify the single new final qualification. Otherwise the decision stays **BLOCKED / NOT_READY**.

## Reproduction

From this checkout's repository root:

```bash
python3 docs/workstreams/independent-platform-2026-11-16/evidence/final-master/performance-blocker-recovery/agent-a/analyze_existing.py . /tmp/pgbase-recovery-comparison.json
```

Exit zero means the offline analysis and its accounting assertions completed; the JSON explicitly retains the failed performance verdict. It is not a runtime test pass. All new Agent A files are documentation/evidence only; no implementation, test gate, database state or workflow was changed.
