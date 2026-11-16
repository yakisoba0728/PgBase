# Final integrated independent QA

Reviewer: Agent C (`/root/independent_qa`), separate from CI author Agent A
and listener author Agent B. **Final verdict: ACCEPT_FINAL_INTEGRATED_QUALIFICATION.**
The single frozen-source final execution and independent raw-evidence review
are complete. No whole-repository rescan or new load campaign was started by
the reviewer.

## Source identity

| Identity | Value |
| --- | --- |
| Final CI run | [43861850923](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923) |
| Frozen published PR head | `4091c3be20ec8db177886a2a85550924fce37ff5` |
| Actual CI synthetic checkout | `ba838872d72993abf1953dcafc64ba043f978c06` |
| Exact common Git tree | `653a46758f9e6af9662b77ecfe2f069a2ac94cb4` |
| Independently race-tested local runtime | `ff5cd9ac3188923400d26297280faa87839b590d` |
| Its published GitHub equivalent | `277c18cbb5333ca2a925ccb84628702c29b7fcd6` |
| Exact common independently tested tree | `1d30798382078731b9442e4a42aadb0a9d328a34` |

The reviewer compared Git objects locally. All changes after the focused
test source are workstream documentation/evidence only. The unchanged
load-gate Git blob, synthetic parents and changed-path list are retained
in `qualification-source.json`. Fresh scanner summaries and completed job
logs independently identify the actual synthetic checkout.

## Changes and independent execution

The full qualification previously selected the Chromium-only browser
workflow. Agent A changes one callable-workflow target to the established
strict superset with Firefox and WebKit. Inputs, permissions, Chromium,
hardened runtime, recovery count/shuffle and the job timeout are preserved.
No browser test, console assertion or performance threshold was weakened.

Agent B transfers the schema LISTEN session from an acquired pool slot to
explicit listener ownership with pgxpool `Hijack`, then explicitly closes
it with bounded cleanup. The initialized session retains secure transport,
search path, timeout and backup writer lock. Request/background pool 4 is
unchanged. One extra long-lived schema session is disclosed; the existing
application backup-gate session and diagnostic observer are separate.
The additional read-only `full_page_writes` snapshot changes no DB setting.

The reviewer restored the old listener, independently reproduced the
three-of-four request-capacity failure, then restored the complete combined
candidate. PostgreSQL 17.9 integration/race execution passed 25 top-level
tests plus 17 subtests with no failures/skips, covering capacity, reconnect,
missed-catalog readiness, fencing, session policy, backup exclusion/recovery,
backend disappearance and shutdown ordering. Details and raw RED/GREEN
results are in `gate-contract-review.md`, `focused-result.json` and their
linked logs. This focused execution is distinct from the full final CI.

## Fresh security and supply-chain reconciliation

The reviewer downloaded the five final security artifacts once and checked
every ZIP's size and SHA-256 against GitHub metadata. Raw ZIPs are retained
under `security/`; `fresh-security-recheck.json` records their URLs/digests
and the reproducible comparison with historical run 43836410887.

The Go and JavaScript/TypeScript reports contain 24 and 1 findings. Every
rule/message/fingerprint identity and resolved primary location matches
the previous classification, and every source file in the reported
dataflows has the same Git blob. This is a finding-by-finding comparison,
not an inference from equal counts. The current reports successfully
analyzed the configured full source, have no diff-range restriction, and
record no warning/error extraction notifications. All four changed Go
runtime paths and the TypeScript SDK path appear among 485 Go and 331 JS/TS
extracted locations.

The all-severity image report still contains 32 exact matching records:
21 MEDIUM, 9 LOW and 2 UNKNOWN. Its separate HIGH/CRITICAL gate has zero
results. SBOM package versions and runtime-asset manifests agree; the
148 unique copied trust-root fingerprints match the manifest. Successful
jobs do not erase source warnings or establish that native helpers are
unreachable. The previous libc/OpenSSL uncertainty, UNKNOWN records,
distribution/upstream disagreements and verified-amd64-only limits remain.
No suppression or dependency allowance was added. Scope remains the
configured Linux Go and JS/TS scan plus executed package/image checks;
this is not coverage of every Dart, SQL, OS or external-provider path.

## Completed functional CI, independently reconciled

The reviewer separately fetched terminal jobs through GitHub's API:
**15 successful jobs, zero failures, zero pending**. The fourteen functional
jobs match Agent A's ledger exactly. Every required step in that ledger was
checked against the independently fetched job metadata. All fourteen retained
gzip logs were decompressed, independently hashed, compared with retrieved
raw bytes, and checked for the actual `ba838872` checkout marker. No named
test failure/skip occurs in those logs. Existing conditional matrix/release
steps remain distinguished from required tests; no waiver was introduced.

The executed gates include full Go unit/static/race policy; PostgreSQL 16.13,
17.9 core and application race, and 18.3; schema/Storage/backup/SDK contracts;
TypeScript 119 and Dart 138 tests in the applicable matrix executions;
frontend/typecheck/build; Windows/macOS; immutable-artifact/observability
checks; and source/supply-chain/image analysis. These are fresh final-source
results, not promoted historical passes.

The reviewer separately checked the browser/recovery raw log: Chromium
**26 passed**, and **12 passed** for the enabled Firefox/WebKit projects.
The unchanged symmetric configuration selects five authenticated scenarios
and one managed-backup scenario for each engine, supporting **6 Firefox and
6 WebKit**. Hardened-runtime steps succeeded. Black-box logs record
**30 top-level passes**, comprising 15 distinct scenarios repeated twice,
with no named failure/skip. The direct API snapshot is `final-jobs.json`;
`functional-independent-check.json` binds every job, required step and raw
log hash to this conclusion.

## Performance evidence

**Performance = Pass.** The independent postprocessor was written before
the result and its SHA-256 was reported to the
coordinator: `243bcf24642f0cf24619e5a7b44150f8547b29e04dda93c9a3c12eae37789e3d`.
It was checked offline against the preserved historical failing artifact:
119,453 completed, 547 drops, no unexpected request errors, 547 incomplete,
p99 209.489023 ms, correctly **Fail** despite acceptable p99. The historical
missing `full_page_writes` measurement is marked as an evidence gap, not
as a disabled setting. No extra load run was performed for this check.

Agent B supplied the final raw files before supplying its computed numbers.
The reviewer independently parsed all 120,000 rows and obtained:

| Gate | Independent result |
| --- | --- |
| Scheduled / attempted / completed | 120,000 / 120,000 / 120,000 |
| Dropped / unscheduled / unexpected errors / incomplete | 0 / 0 / 0 / 0 |
| Scheduled-to-validated p50 / p95 / p99 | 7.400337 / 12.632966 / 22.221162 ms |
| Maximum end-to-end latency | 126.553471 ms |
| Input/workload | 200 RPS, 600 seconds, exact 5 ms offsets and unchanged owner/operation mix |
| Request/background pool | Maximum 4 in all 6,000 observations |
| Before/after durability | fsync, synchronous_commit and full_page_writes all on; wal_sync_method unchanged fdatasync |
| Final durable SQL/owner/cursor assertions | Completed successfully in the unchanged Go test |

Latency decomposition, operation statuses, log counters and nearest-rank
quantiles agree. Dropped/unscheduled arrivals remain separate errors without
invented latency samples. Each owner has 60,000 scheduled operations; physical
row assertions require 24,256 total rows and 12,128 per owner after draining.
These successful expected counts are source-enforced assertions; neither
reviewer claims to have queried the already-disposed database afterward.

All eight extracted files were compared byte-for-byte with the original
[artifact 14224442513](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/artifacts/14224442513).
The independently recomputed ZIP length is 3,528,654 bytes and SHA-256 is
`82f0397e6b0ab5e1d71c96ebc2c4af35137fc8638d60c3c262b8208d5935d84e`, matching
GitHub's metadata. Its environment file identifies the expected synthetic
checkout. `performance-artifact-integrity.json` records member hashes;
`final-performance-recheck.json` records every gate calculation. The original
ZIP is retained by Agent B at `../performance/final-gate/pgbase-final-mixed-http.zip`.

Six persistent application PIDs appear in every observation. With the
excluded diagnostic observer, there are **seven client sessions**, including
four pooled request/background slots, one schema listener and the existing
backup gate. The 150 extra activity rows have `autovacuum:` query prefixes.
They account for a sampled peak of eight PostgreSQL activity backends,
including maintenance and the observer, not eight client connections.
The sampler omits `backend_type`; that maintenance classification explicitly
relies on query-prefix evidence. Counts and independently aligned host
statistics are recorded in `performance-connection-and-host-check.json`.

The reviewer also checked Agent B's final report against raw timing maxima,
all component quantiles, bucket p99, pool occupancy, WAL deltas, wait samples,
logged CPU/GC/duration and 600 aligned vmstat samples. No substantive factual
discrepancy was found. B's report correctly distinguishes correlations,
aggregate counters and source-enforced assertions from proved causation.

## Final decision and retained limits

| Axis | Verdict |
| --- | --- |
| Functional / Non-performance | **Ready** |
| Performance | **Pass** |
| Overall | **Ready for Manual Merge** |

No P0/P1 blocker was established in the reviewed changes or final required
gates. Exactly one final 600-second run was used; there is no same-code retry
or second performance attempt to select a favorable result. The independent
reviewer did not author the CI/listener implementation and did not rerun the
load while analyzing its artifacts.

The historical Firefox font-abort cause remains unproven. Current required
browser passes are not a claim of its causal repair. The historical 547 drops
remain a failed earlier gate; this successful candidate does not establish
that separating the listener alone explains the difference. One disposable
host does not prove unlimited production capacity. Existing native-helper,
external-provider and operational Storage/Jobs limits remain as documented.

This is a manual-merge readiness decision. PR #148 remains Draft/unmerged;
no main merge, deployment, release or SDK publication was performed by this
reviewer. Subsequent report/evidence commits must remain runtime-equivalent
and must not be mislabelled as another tested SHA.
