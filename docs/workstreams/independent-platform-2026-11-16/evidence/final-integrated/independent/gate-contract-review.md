# Independent gate-contract review

Reviewer: Agent C (`/root/independent_qa`), separate from Agent A (CI change)
and Agent B (listener implementation). This is a bounded correctness and
qualification review, not a new repository-wide vulnerability scan.

## Starting source

PR #148 was independently read through the GitHub connector: open, Draft,
unmerged; head `23b6c4090e453587325d9dfad8fec6d854f3bee2`, base
`8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`. The reviewer uses the separate
`PgBase-qa` worktree. Prior passing results are not attributed to new code.

## Existing performance contract

Read `pgbase/open_loop_load_integration_test.go` and the optional diagnostics
and process supervisor. The final workflow sets 200 RPS and 600 seconds;
the test therefore schedules 120,000 independent arrivals. Request pool
configuration is explicitly `pool_max_conns=4`. The bounded queue (256) and
worker count (32) are unchanged. Scheduled time, not worker start, begins the
latency measurement; it ends after response validation. A request timeout is
included among attempted latencies. A dropped or unscheduled arrival has an
explicit error and does not receive an invented latency sample. Passing
requires every intended arrival to complete, no drops/errors, and end-to-end
p99 at most 250 ms. A good completed-request p99 cannot override losses.

The workload checks authenticated owner selection, expected CRUD status,
ETag transitions, related-record identity, filtered ordering and cursors.
After draining, physical SQL counts must equal 24,256 total records, 12,000
creates and 12,000 updated records for the full run. Each owner is traversed
through every page, with 12,128 distinct records, 6,000 creates and 6,000
updates. Cross-owner point reads must return 404. The public total is capped
at 10,000, but that hint is not the physical-integrity oracle.

The diagnostic observer uses its own PostgreSQL connection outside pool4,
samples at 100 ms and changes measurement overhead. Its data is explicitly
labelled as an instrumented stratum. Cluster IO/WAL statistics and sampled
waits do not prove single-request causation; percentile components must not
be added. Final artifacts/settings and row counts still require independent
verification after execution.

## CI correction independently accepted

Agent A author commit `87cf6a19e25a999de8690a212e7d10c72f2baefd` changes the
full qualification's browser call from `e2e.yml` to
`nonperformance-e2e.yml`. The reviewer compared both callable workflows.
Inputs/defaults and permissions are compatible. Existing Chromium command,
35-minute job bound, fixture isolation, nonroot/no-shell assertions and
black-box `-count=2 -shuffle=on` remain identical. The target adds the
already-established Firefox/WebKit smoke projects and failure-trace uploads.
This closes an orchestration coverage omission without lowering any gate.

The six-pair repeated-performance workflow is outside this final bounded
round. The full suite's fixed-arrival gate must run once on the frozen
candidate; any second final run requires new verified code and separate QA.

## Listener design review before implementation

Current `internal/schema/listener.go` reserves a pool connection throughout
LISTEN. With pool4, only three pooled connections remain for other work.
This is previously documented behavior, not evidence of a connection leak
or proof of the historical 547-drop cause. The user's final instructions
permit one dedicated listener session while keeping request pool4.

Acquiring then calling pgxpool v5.13.0 `Hijack()` transfers an already
initialized session to the listener. Local dependency source documents that
the caller must close it and must not reuse/release the pool wrapper.
This preserves the pool's verified-transport policy, safe search path, UTC,
statement timeout and shared backup writer lock. Reusing the existing
`OpenCoordinationSession` helper would not preserve all session setup.

App shutdown cancels and awaits background components before closing the
pool. Acceptance still requires executed capacity, initialized-session,
reconnect, cancellation, backend disappearance and backup-lock tests. An
extra persistent listener session must be disclosed separately from the
diagnostic observer and other coordination connections.

## Security baseline, preserved limits

The reviewer opened the retained raw ZIPs for historical run 43836410887,
recomputed their SHA-256 hashes and matched all three recorded API digests.
Actual SARIF counts remain 24 Go, one JavaScript and 32 all-severity image
records; the HIGH/CRITICAL-only image report contains zero. The derived
byte check is in `historical-security-byte-check.json`. These are verified
historical bytes, not a fresh scan of this candidate.

The historical classifications retain native helper/libc/OpenSSL uncertainty,
two UNKNOWN records, distribution/upstream conflicts and architecture limits.
No finding suppression or claim of vulnerability-free code is warranted.
The historical Firefox abort remains an unresolved recorded risk; it does
not itself reverse a later required-browser pass without new reproduction.

## Executed independent review of the integrated candidate

The reviewer independently ran the new capacity test with only the listener
restored to its original implementation. Both initial and reconnected
listeners left pool4 with one acquired listener slot, three simultaneously
held request slots, and a fourth request acquisition timing out. The retained
RED log records four physical backends and unchanged durability settings.

The reviewer then restored the candidate and switched to the combined source
`ff5cd9ac3188923400d26297280faa87839b590d`, tree
`1d30798382078731b9442e4a42aadb0a9d328a34`. Its tracked working tree was clean.
Independent PostgreSQL17.9 integration/race execution passed **25 top-level
tests plus17 subtests**, zero failures/skips, across schema, database and
application packages. This includes pool capacity before/after reconnect,
readiness and missed-catalog recovery, writer-generation fencing, backup
writer exclusion/recovery, initialized session settings/transport policy,
listener backend disappearance and application shutdown ordering. Raw results,
exit statuses and reproduction commands are stored alongside this review.

The GREEN capacity fixture has four simultaneously held request connections
plus one dedicated listener, with its shared backup lock retained. Both old
and replacement listener PIDs disappear after termination/cancellation; the
exclusive backup gate can be acquired after listener and pool shutdown.
The complete application additionally has its pre-existing dedicated backup
gate (`pgbase/app_construct.go`), and the instrumented load adds an observer:
the persistent budget therefore changes from six to seven connections when
all four pool slots exist. Other temporary coordination connections, if any,
remain separately observable. The new listener's one-connection increase is
not concealed inside a larger request pool.

The measured workload file is byte-identical to starting23b6c409. The only
diagnostic SQL change adds `full_page_writes` to a read-only settings snapshot.
Request count/rate, queue/workers, latency accounting, error/drop gates,
durable checks and PostgreSQL settings are unchanged. Deployment documentation
now states the additional listener budget and transient pool acquire on
reconnect. The reviewer found no blocking regression in these scoped changes.

## Pre-gate disposition

**ACCEPT_SCOPED_CANDIDATE_FOR_FINAL_GATE.** The author changes and integrated
focused checks are accepted for one final full-CI/performance execution.
This is not yet a performance PASS or a whole-product readiness decision.
Final CI checkout/tree, raw CSV, durability snapshots and fresh scanner
artifacts still require independent reconciliation after the fixed-source run.
