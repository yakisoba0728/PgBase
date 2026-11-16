# Performance continuation: fixed-arrival investigation

Agent `/root/performance`, recovered runtime `d77fd45de6975f55b047a3de9c4099e1805162ce`.

The required gate remains 200 arrivals/s for 600 seconds, pool four, p99 at most
250 ms, and **zero** errors, dropped or incomplete requests. No runtime change,
threshold relaxation or skipped assertion is part of the diagnostic commits.

## Recovered evidence

The runtime, load harness and workflow Git objects are identical between the
passing historical run 43797156393 and failed run 43804114855. Their runner
CPU/hypervisor differs (Intel/KVM versus AMD/Azure). The source comparison JSON
was recovered and its Git object identities were independently rechecked in this
session. A source regression cannot explain this pair; a particular host or
database cause has not yet been measured. The earlier pass does not qualify a
future integrated runtime.

The failed run's 178 CSV errors are all `arrival queue full`: 15 creates, 17
updates, 18 deletes, 18 point reads and 110 pages. Subtracting only the unsent
mutations predicts the observed durable state exactly for both owners. This
explains the planned-count mismatch; it does not turn the incomplete workload
into a passing result or establish the cause of saturation. See
`failed-load-mutation-reconciliation.json`.

## Diagnostic changes

Recovered commit `93272446` supplies an optional independent 100 ms PostgreSQL
observer. This continuation adds per-sample application pool statistics and
separates scheduled-to-enqueue generator delay from enqueue-to-worker delay.
Their sum remains the original generator/queue duration. The original end-to-end
latency origin, bounded admission queue and correctness checks are unchanged.

Hosted qualification enables that explicitly instrumented stratum, records
PostgreSQL I/O/WAL timings without changing durability settings, and captures
one-second host/container resource samples. Observers add overhead and their
results must remain distinct from uninstrumented historical measurements.
Database activity samples are not exact per-query durations; cluster-wide I/O
and WAL include observer and other database work.

The first local 20 arrivals/s, five-second smoke validates 100 raw rows, zero
request errors, 49 DB/pool observations and the timing identity for every row.
It **fails** the unchanged p99 gate at 251.522 ms during concurrent compilation;
the failure and raw artifacts are retained. This is instrumentation validation,
not release qualification or a throughput claim. Repository workflow policy
tests pass. A 600-second controlled team-quiet diagnostic run is pending.

Raw evidence: `observer-smoke.zip`; assertions and result:
`observer-smoke-validation.json`. No production service or credentials are used.
