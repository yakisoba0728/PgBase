# Independent source review: fixed-arrival diagnostics

Reviewer: `/root/backend`, separate from the Performance agent.
Reviewed changes: `c298a9f4` and `17203060`, exact latter tree
`e711982abeeb767bfcb88e1b321984e3e24142db`.

Read the complete changed diagnostics sampler, generator admission/timing diff,
surrounding result/data assertions, workflow commands and artifact handling, and
the smoke/reconciliation reports. No blocking defect was found in these changes.

- The 200-arrivals/s × 600-second hosted gate, pool four, 256-place queue,
  32 workers, zero-loss/error checks and 250ms end-to-end p99 bound remain.
  SQL and per-owner post-load integrity assertions are unchanged.
- Generator delay is measured before enqueue; worker queue delay is the
  original scheduled-to-worker duration minus that measured generator delay.
  The existing end-to-end clock origin is unchanged. Dropped arrivals remain
  failures and do not become completed samples.
- PostgreSQL observations use a separate connection. Each activity query has
  a one-second independent context; snapshot/connect/close operations have a
  five-second budget. The observer is joined before its slices are serialized
  or its connection is closed. Sampled waits are explicitly not exact query
  durations, and cluster-wide statistics are labeled accordingly.
- Diagnostic errors use `t.Errorf`, allowing raw load CSV preservation. The
  workflow's artifact upload uses `always()`, and its shell exit trap cleans up
  host/container observers. Instrumented results are a separate stratum.
- The 100-request smoke's 251.522ms p99 is retained as a failure. It is not
  relabeled as passing release qualification despite zero request errors.

This is independent **source review**, not independent execution. The reviewer
did not rerun the load experiment, verify Docker observer behavior, or recompute
historical source/data accounting. QA must independently validate those results
and final integrated CI remains required.
