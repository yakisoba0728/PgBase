# Independent PostgreSQL platform workstream

Baseline: `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`, schema 67. Work is isolated in PR [#148](https://github.com/yakisoba0728/PgBase/pull/148). Status: implemented hardening batches with final validation in progress; **not a completed exhaustive audit or unrestricted large-scale readiness certification**. No production access, deployment, release publication or schema migration has been performed.

## Owner-approved scope

Breaking API, SDK, schema and configuration changes are allowed when justified. Existing production data need not be preserved, but this does not authorize accessing or erasing production data during development. Features may be replaced when correctness, security, performance, reliability or developer experience improve. Preserve the administrator UI's visual identity. PgBase is an independent PostgreSQL application backend; historical research is not a compatibility promise. Required third-party notices remain intact. Production deployment remains the owner's responsibility.

## Architecture decisions

1. PostgreSQL remains the owner of durable state. The supported scaling boundary is multiple instances of one project sharing one database, consistent secrets and suitable shared file storage. Do not add sharding, replicas or a distributed cache merely to claim scalability.
2. Keep authentication, authorization, query planning, transaction execution and response projection separate. Preserve atomic record/revision/audit/outbox changes and the tested transaction ownership rules.
3. Compile immutable configuration at publication boundaries. Request handling should not repeatedly parse settings or retain unrelated configuration.
4. Bind frontend requests and recovery to a session lifetime. Neither a delayed successful result nor a late 401 may publish into a new login; a rejected old mutation must not be retried under a new session.
5. Put image format, encoded-byte and decoded-pixel limits in explicit shared boundaries. Borrow already-owned input bytes instead of duplicating them; retain image orientation, transparency and output contracts.
6. Preserve the existing UI layout and styling. Changes in this workstream concern transport behavior and product terminology, not a visual redesign.

The existing core was recently rewritten and contains substantial transaction, auth, migration and recovery coverage. Rewriting these kernels again without demonstrated benefit would discard useful evidence. This workstream therefore makes targeted structural changes rather than equating changed-line count with quality. In particular, replacing the schema `FOR SHARE` pin with advisory locking is deferred: snapshot timing and DDL/catalog consistency need a dedicated design and regression proof.

## Implemented batches

| Area | Change | Evidence and boundary |
| --- | --- | --- |
| HTTP | Explicit gzip preferences override wildcards across header lines; validate qvalue grammar; conflicting duplicates deny conservatively | Baseline reproduced 11/18 new failures; corrected 22 cases and completed bounded fuzzing. No blanket HTTP parser audit claim |
| Administrator transport | Session-bound success/error/body decoding and CSRF recovery; abort cleanup, preserved abort reason, upload timeout handling and bounded progress | 17 new lifecycle cases plus existing transport/session tests; no layout or CSS change |
| PostgreSQL driver | pgx 5.11.0 to 5.13.0 with verified module lockfile | Includes upstream security/correctness fixes; do not claim every upstream issue was exploitable in PgBase |
| API quotas | Immutable compiled exclusion policies; distinguish database failure from genuine quota exhaustion | Fault-injected baseline reproduction, five corrected race/integration runs; paired microbenchmarks |
| Images | Shared bounded decoder, zero extra encoded-input copy on byte entry points, WebP fallback reuses input | Existing GIF/WebP/orientation/transparency tests plus byte/reader parity, bounds and input immutability; paired benchmarks |
| SDKs | Native `pgbase_auth` cookie namespace, TypeScript/Dart attribute parity and validation, explicit empty/missing-state semantics | Breaking contract and deployment guidance in [SDK cookies](../../sdk-auth-cookies.md); SDK validation must pass on the final revision |
| Product identity | Independent README and English/Korean administrator wording | Legacy mode remains labelled legacy, not falsely native. Historical documents and license notices are preserved |
| Test reliability | Backup test waits for actual operation drain and owns cleanup immediately; crash E2E resolves recreated container identities | Fixes observed fixture failures without removing durability assertions; crash test now forces container replacement |
| Qualification | Bounded concurrent query-service profile with dataset, concurrency, tails and error reporting | Synthetic disposable database only; [performance evidence](performance.md) states limitations |

## Verification policy

Preserve the repository's formatting/static/unit/integration/SDK/browser/security gates. Full manual Backend covers PostgreSQL 16/17/18, 17 race shards, multiprocess durability, live SDKs, Windows self-update, macOS terminal and release-phase tests. Full Security additionally scans the produced image. E2E includes browser scenarios and process/database failure recovery. These are distinct from release publication, and no release workflow has been dispatched.

The earlier full candidate `5d378046` passed PostgreSQL 16/18, the 17 application shard, Windows/macOS and full security, but failed a backup test's three-second polling assumption and crash-test cleanup of stale container IDs. Those failures are retained as evidence and corrected in source; they are not silently rerun until green or presented as product data-loss fixes. Initial frontend tests also showed intermittent one-second view-assertion failures; five focused repetitions and later full runs passed, but the original timing cause was not conclusively established.

The current final run results will be recorded separately by exact revision. Unexecuted, cancelled, failed or older-revision checks are not a pass for the final candidate. Focused benchmark results are retained in [performance.md](performance.md), including the small-image case that did not improve.

## Scope still requiring qualification

- End-to-end HTTP saturation and sustained mixed read/write workloads, realistic relation graphs, realtime fan-out, slow clients, storage/network failures and large backup/restore timings.
- Independent security/code review. A GitHub Codex review was requested; a request is not a completed review. This chat environment has no separate coding-agent spawn facility, and parallel CI jobs are not independent AI agents.
- Browser coverage beyond the existing Chromium suite, actual assistive-technology use, external OAuth/SMTP vendors and a full manual visual audit.
- The repository About description remains an administrative setting outside this code PR. Proposed wording: “Independent PostgreSQL application backend with typed data, authentication, realtime, file storage and an embedded administrator UI.”
- Legacy wire adapters and dated research remain where removal would be a separate contract change. A positioning change does not justify deleting third-party notices or pretending historical work never existed.

## Execution record

The initial local environment could not resolve external hosts. Source and pinned toolchains were transported through short-retention, read-only Actions artifacts, but a subsequent local execution error prevented initialization of the full local toolchain. CI is the evidence for executed repository checks. The temporary toolchain/source transport and generated-output writer have been removed; remaining workstream-only validation helpers must be removed or converted to read-only manual qualification before merge.
