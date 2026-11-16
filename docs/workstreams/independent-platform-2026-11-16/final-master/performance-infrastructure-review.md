# Final master: performance and infrastructure review

## Result and scope

This bounded review found and corrected one **P2 evidence-validation defect**.
No runtime optimization was adopted, and this reviewer did not launch a full
qualification workflow or the 200 RPS / 600 second gate. Those gates remain the
Coordinator's responsibility on the final combined candidate. Historical PASS
results are not new execution evidence.

The starting source was `242f80aea730ee483cc22798fb6450bdb3064081`, tree
`b758d273142d75377c18011d240cd64929cb4c97`. The local correction commit is
`6add53b53b57e6fc3826820ac7b6580b493629cb`, tree
`a1abddc0078df9a34427225963bf0b6631930780`. Changes are confined to the offline
analysis script and its regression tests. They alter neither runtime behavior,
the database schema, the benchmark workload, nor any acceptance threshold.

## Adopted correction: PERF-EVIDENCE-01

`scripts/analyze_repeated_load.py` promised to analyze the registered experiment,
but its HTTP reader accepted any four concurrency stages. It also accepted
percentiles computed from fewer requests than the reported workload and an
enlarged database pool. Consequently, substituted or incomplete experiment
evidence could be presented as the intended comparison.

The reader now requires the registered order **c1, c8, c32, c4 recovery**, the
expected request count in the latency population, and a maximum request pool of
four. Every original error/request-count and duplicate-stage check remains.

| Regression | Original source | Corrected source |
| --- | --- | --- |
| Registered four stages | PASS | PASS |
| Substitute c2 for c1 | Accepted: regression test FAIL | Rejected: PASS |
| Move c4 recovery before c32 | Accepted: regression test FAIL | Rejected: PASS |
| 999 latency samples for 1,000 requests | Accepted: regression test FAIL | Rejected: PASS |
| Increase request pool to 8 | Accepted: regression test FAIL | Rejected: PASS |

The old-source RED run had four failures and one success. The corrected Python
suite passed **17 tests: 12 existing CodeQL parser tests and five new HTTP
evidence tests**. It was repeated after committing the correction. Three
preserved real HTTP logs were replayed through both old and new readers: every
parsed metric is exactly equal. Their raw bytes and SHA-256 values remain
unchanged. This replay does not rerun the historical database workload.

Rollback is the inverse of this single script/test commit; no data rollback is
involved. Independent implementation review and final integration qualification
are separate from this author's checks.

## Runtime measurement and optimization decision

The existing `fieldsByOrdinal` optimization and record encoder were checked with
the repository's focused correctness tests and bounded allocation benchmarks,
using Go **1.26.11**, Linux/amd64 and `GOMAXPROCS=4`:

```sh
go test ./internal/query \
  -run '^(TestRecordCodecKeepsColumnOrderWithoutMutatingCatalog|TestBenchmarkCollectionAndInput)$' \
  -bench '^(BenchmarkFieldsByOrdinal20|BenchmarkEncodeRecord20Fields)$' \
  -benchtime=100ms -count=3
```

All passed. The existing sorted-catalog path retained **0 B/op and 0 allocs/op**;
its in-benchmark copy/sort reference allocated **8,680 B/op and 4 allocs/op**.
Twenty-field record conversion measured **5,745 B/op and 89 allocs/op** in each
of the three observations. These are fixture-level process measurements, not a
new optimization, an HTTP measurement, or a server-only memory claim.

Other worktrees were compiling/testing concurrently. Timings varied materially
(record conversion 50,671–79,615 ns/op), so speed, CPU, RSS and end-to-end
improvement remain **INCONCLUSIVE / NOT_ADOPTED** for this review. No runtime
change was proposed on the strength of those timings. Database access, cursor
semantics, durable writes, resource budgets and the established load gate were
left intact.

## CI and final-gate protocol

The reviewed `.github/workflows/full-qualification.yml` runs the complete suite
on an open/synchronize/reopen/labeled PR event when the PR has the
`full-qualification` label. `workflow_dispatch` is also supported. A different
label event can therefore trigger the suite while that label is present.
Checkpoint commits should continue to use `[skip ci]`; after independent QA and
integration, one non-skipped commit can qualify the frozen candidate. Avoid
intervening label changes or pushes during that run because its concurrency
group cancels an in-progress predecessor.

The `performance-qualification` and `final-performance-qualification` labels
belong to a separate historical AB/BA comparison workflow; they are not the
final 600-second gate.

The final mixed job uses pinned PostgreSQL 17.9, a four-connection request pool,
`GOMAXPROCS=4`, **200 RPS × 600 seconds**, 120,000 scheduled arrivals, zero
drop/error/incomplete requirements, and end-to-end p99 at most 250 ms. The load
test performs post-drain physical row counts and per-owner traversal. Its
observer overhead is explicit. The persistent diagnostic connection budget is
**4 request + 1 Listener + 1 backup gate + 1 optional observer = 7**; production
without the observer uses six before operator/maintenance headroom.

All reusable qualification workflows use the event's source/synthetic checkout.
Final reconciliation must obtain each job's actual checkout SHA and compare its
tree to the frozen PR source; run success alone is insufficient. The workflows
used here do not publish or deploy. No gate or workflow was changed by this
review.

## Infrastructure, security and fixture checks

The existing resource-observer subprocess regressions passed **6/6**, including
early observer exit, preserved workload failure, bounded forced termination and
descendant cleanup. Go tests for `./internal/repositorypolicy ./deployments`
passed. The focused inspection covered workflow triggers, checkout binding,
source-analysis scope, image recording/gating and pinned fixture setup.

CodeQL disables diff-informed analysis and its parser rejects incomplete/full-
scope violations while preserving findings. Its successful execution is not a
zero-findings security decision. The image workflow records both HIGH/CRITICAL
and all-severity SARIF before the HIGH/CRITICAL gate, with an empty named
allowlist and no blanket unfixed-vulnerability exception. The runtime image
still includes native PostgreSQL helper libraries despite the static Go main
binary. Existing native-helper, distribution/upstream and architecture limits
must therefore remain in the final risk register. This reviewer did not rerun
scanners or establish new native-library exploitability conclusions.

The project-supported S3 fixture uses Docker Compose and pinned MinIO source.
This workstation initially lacked Docker and PostgreSQL. Its test infrastructure
accepts a disposable `PGBASE_TEST_ADMIN_URL`; no operational database was used.
The exact MinIO source versions are recorded in `tests/filestore/Dockerfile`,
which also provides a possible user-level build route if needed. Local fixture
provisioning and final integration execution are coordinated separately.

## Evidence

The [evidence manifest](../evidence/final-master/performance/manifest.json) binds
commands, source identities and artifact hashes. Key artifacts are:

- [Original-source RED](../evidence/final-master/performance/http-validator-red.log)
- [Committed-source GREEN](../evidence/final-master/performance/python-green-committed.log)
- [Unchanged historical parsed results](../evidence/final-master/performance/historical-http-replay.json)
- [Observer cleanup checks](../evidence/final-master/performance/observers-baseline.log)
- [Infrastructure policy tests](../evidence/final-master/performance/infrastructure-policy.log)
- [Bounded allocation baseline](../evidence/final-master/performance/query-microbench.log)

No historical raw evidence was moved, deleted or rewritten.
