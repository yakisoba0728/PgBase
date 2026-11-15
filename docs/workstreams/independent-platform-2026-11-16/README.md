# Independent PostgreSQL platform workstream

Baseline: `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`, schema 67. Changes are isolated in draft PR [#148](https://github.com/yakisoba0728/PgBase/pull/148). An implemented hardening batch and its automated qualification are delivered; **the broader whole-platform redesign and independent large-scale production qualification are not complete**. No main merge, production access/deployment, schema migration, tag or package/release publication was performed.

Start with the [verification and handoff record](validation.md), [measured performance](performance.md) and [breaking SDK cookie contract](../../sdk-auth-cookies.md). These distinguish completed work from the original nine-phase program rather than claiming all phases are finished.

## Owner-approved scope

Breaking API, SDK, schema and configuration changes are allowed when justified. Existing production data need not be preserved, but this does not authorize accessing or erasing production data during development. Features may be replaced when correctness, security, performance, reliability or developer experience improve. Preserve the administrator UI's visual identity. PgBase is an independent PostgreSQL application backend; historical research is not a compatibility promise. Required third-party notices remain intact. Production deployment remains the owner's responsibility.

## Architecture decisions

1. PostgreSQL remains the owner of durable state. The supported scaling boundary is multiple instances of one project sharing one database, consistent secrets and suitable shared file storage. Do not add sharding, replicas or a distributed cache merely to claim scalability.
2. Keep authentication, authorization, query planning, transaction execution and response projection separate. Preserve atomic record/revision/audit/outbox changes and tested transaction ownership rules.
3. Compile immutable configuration at publication boundaries. Requests should not repeatedly parse settings or retain unrelated configuration.
4. Bind frontend requests and recovery to a session lifetime. Neither a delayed successful result nor a late 401 may publish into a new login; an old rejected mutation must not be retried under a new session.
5. Put image format, encoded-byte and decoded-pixel limits in shared explicit boundaries. Borrow already-owned input bytes instead of duplicating them; retain orientation, transparency and output contracts.
6. Preserve existing UI layout and styling. This batch changes transport behavior and product terminology, not the visual design.

The core was recently rewritten and contains substantial transaction, auth, migration and recovery coverage. This work preserves those boundaries and makes targeted structural changes instead of treating changed-line count as quality. Replacing the schema `FOR SHARE` pin with advisory locks remains a separate design: snapshot timing and DDL/catalog consistency need dedicated regression proof.

## Implemented batches

| Area | Change |
| --- | --- |
| HTTP | Explicit gzip preferences override wildcards across header lines; validate qvalue grammar; conservatively handle conflicting duplicates |
| Administrator transport | Session-bound success/error/body decoding and CSRF recovery; abort cleanup/reason preservation, upload timeout handling and bounded progress |
| PostgreSQL driver | pgx 5.11.0 to 5.13.0 with verified module lockfile and real database/SDK validation |
| API quotas | Immutable compiled IP/CIDR policies; distinguish database failure from genuine quota exhaustion |
| Images | Shared bounded decoder; no additional encoded-input copy on byte paths; reuse WebP fallback input |
| SDKs | Native `pgbase_auth` namespace, TS/Dart cookie-attribute parity/validation, explicit missing/empty/malformed-state semantics |
| Browser example | Explicitly serve the SDK's new transitive module without widening its closed file allowlist |
| Product identity | Independent README and English/Korean administrator terminology; legacy mode remains honestly labelled legacy |
| Test reliability | Deterministic backup completion/cleanup, recreated-container recovery checks, exact classification of intentionally injected history drift |
| Qualification | Paired benchmarks, 500000-row concurrent query profile, extended Go/JS/TS CodeQL analysis and read-only manual qualification workflows |

The full runtime candidate passed PostgreSQL 16/17/18, race, live SDK, bounded multiprocess, native Windows/macOS, security/image and browser/crash E2E validation. Frontend verification passed 422 tests and exact clean-build reproduction. Extended CodeQL reported zero Go and zero JavaScript/TypeScript findings. See [validation.md](validation.md) for precise commits, run IDs, initial failures, corrective changes and scope limits; passing these checks is not a vulnerability-free or unlimited-scale guarantee.

## Remaining qualification

- Attribute page-hydration allocations and validate realistic relation-heavy, sustained mixed HTTP workloads, saturation, realtime fan-out and slow clients. Current query results are not HTTP capacity numbers.
- Complete independent security/code review and a module-by-module audit. The review request has not yielded a completed response; parallel CI is not independent AI review.
- Complete broad UX/accessibility/browser assessment, external OAuth/SMTP verification, storage/network fault and large restore timing qualification. Existing Chromium and isolated fixtures cover only their stated scenarios.
- Resolve existing intermittent UI test timing and non-fatal `act(...)`/jsdom warnings rather than interpreting later passes as proven root-cause fixes.
- Update repository About metadata using an authorized settings capability. Proposed description: “Independent PostgreSQL application backend with typed data, authentication, realtime, file storage and an embedded administrator UI.” This code PR does not change that metadata.
- Decide separately whether to retire legacy wire adapters. Historical research and required attribution remain; removing product positioning does not justify erasing legal notices or pretending history never existed.

## Execution and delivery boundaries

The local full toolchain could not be initialized after environment failures; executed repository checks are backed by GitHub Actions. No independent coding-agent spawn facility was available. Temporary source transport, automatic source-writing, dispatch and branch-only diagnostic workflows are removed. The reusable `query-load.yml` and `source-security.yml` are manual and read-only. Original quality gates remain intact. Draft status reflects the open broader scope, not a claim that an unspecified future background process will finish it.
