# Independent PostgreSQL platform workstream

Status: implementation in progress, not release-ready. Baseline: `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a` (schema 67). This record supersedes earlier owner decisions only for this workstream; historical deployment records remain historical evidence.

## Owner-approved scope

- PgBase is an independent PostgreSQL application backend. Product positioning must describe its own contracts and capabilities, not another product's compatibility or inspiration.
- Breaking API, SDK, schema and configuration changes are permitted when justified. Existing production data does not need preserving. This is not permission to access or erase production data during development.
- Features may be added, removed or replaced when they improve correctness, security, performance, operational reliability or developer experience.
- Preserve the current administrator UI's visual identity. Improve behavior, accessibility, latency and internal implementation rather than replacing its design.
- Work on the isolated refactor branch. Production deployment is excluded and remains the owner's responsibility. Do not publish a release or claim production readiness without the corresponding evidence.
- Preserve required third-party copyright/license notices. Historical research is not a current product contract.

## Architecture decisions

Retain a PostgreSQL-owned durable state model and explicit transaction ownership. The initial supported scaling boundary is multiple application instances for one project sharing one database and consistent secrets/storage configuration. Do not introduce sharding, read replicas, distributed caches or compatibility layers merely to claim scale. Each such change needs a workload, a failure model, and measured benefit.

Keep request admission, authentication, authorization, query planning, transaction execution, projection and delivery as separate responsibilities. Mutations must preserve the atomic relation between data, revisions, audit and outbox entries. Realtime, jobs and file work must remain bounded and cancellation-aware. Network/CDN latency is separate from server/database latency.

The frontend transport must not replay or publish work across a session boundary. Cache, request and upload lifetimes must be explicit. Existing styling remains the baseline.

## Execution and gates

1. Inventory runtime modules, public surfaces and current evidence; classify confirmed defects separately from hypotheses.
2. Record architecture decisions and behavioral contracts before changing shared boundaries.
3. Reproduce confirmed defects with tests; capture comparative benchmarks before performance changes.
4. Implement backend, frontend, SDK and documentation changes in coherent batches.
5. Run formatting, static checks, unit/race, PostgreSQL integration, SDK conformance and browser tests against the same revision.
6. Exercise concurrency, bounded resource use, failure/recovery and multi-instance behavior using disposable fixtures only.
7. Re-review changed boundaries, record remaining risks and produce a candidate only if the gates pass.

Passing existing unit tests does not prove absence of vulnerabilities or readiness for arbitrary large-scale services. Report tested workloads, environment, latency percentiles, throughput, errors and limitations. Unexecuted checks remain explicitly unverified.

## Evidence ledger

### HTTP compression negotiation

The baseline chose the first `gzip` or wildcard member and accepted non-HTTP floating-point weights. Eighteen new table cases reproduced eleven failures against the unchanged implementation. The correction gives explicit gzip preferences precedence across all header lines, validates the RFC 9110 qvalue grammar and treats conflicting duplicates conservatively.

The corrected standard-library-only source passed 22 table cases and six fuzz seeds with the Go 1.23.2 race detector in an isolated local harness. This is not the repository's Go 1.26.11 integration result. A short fuzz execution was interrupted by the execution limit and is not counted as a completed fuzz run. Full repository validation is pending CI.

### Execution environment

The connected GitHub tools can read and write the isolated branch and retrieve Actions evidence. The initial local environment could not resolve external hosts. Source and pinned tools were obtained through short-retention, read-only Actions artifacts. A subsequent local execution environment error prevents treating the transported tools as a successfully initialized test environment. Use CI results, not downloaded binaries, as evidence of executed checks.

No separate coding-agent spawn facility is available in this session; parallel CI jobs are not independent AI reviews. No production access or deployment has been performed.
