# Agent C — SDK iterator session ownership

**One reproduced defect, fixed. Validated source: `3ec5f640a02541c4f9499703c54da22a9341b7f4`.**
Base: `55f539895f85ca3b7d8731ab8f4c3b8999ab92bb` plus new regression tests.

TypeScript `PgBaseClient.iterate` and Dart `Records.iterate` guarded each HTTP response but not the iterator lifetime. After a consumer paused and authentication changed, buffered records from the old user could still be emitted; pagination could submit the previous user's cursor under the new credentials. This is client-side session isolation, not a demonstrated server authorization bypass.

Both iterators now capture the starting auth generation, check it before each page request and each delivered record, and reject with the existing `403/session_changed` contract. No UI design, layout or behavior changed. TS distribution was rebuilt from source.

## Evidence

- RED on unchanged implementation: TS **4 failures**, Dart **4 failures** (buffered/page boundary × replace/logout). Bootstrap errors are separate compressed evidence, not defect proof.
- GREEN: full TS `npm test` **119 passed / 0 skipped**; Dart lifecycle **22 passed**, full Dart **138 passed / 0 skipped**; `dart analyze` clean.
- Real PgBase HTTP with disposable PostgreSQL **17.9**: `TestSDKIteratorSessionHTTPConformance` **1 parent / 2 SDK subtests passed / 0 skipped**. Covers blocked buffered delivery/cursor continuation, stable two-page iteration, and a fresh replacement-user iterator returning no old-user rows.
- Exact commands, tool paths, outcomes and SHA-256 digests: `verification.json`. Raw logs are retained alongside it; `dart-red-direct.log.gz` losslessly preserves the initial compiler failure.

## Limits and next step

The reviewed boundary was SDK paginated reads. Existing user-body lifecycle tests informed the choice; relevant administrator record-form code was read only. Other SDK/admin workflows were not independently requalified. No Firefox, benchmark, load test or production access occurred. PostgreSQL 16/18 and whole-product qualification are not claimed here. Independent QA remains the coordinator's next step; product verdict remains **Not Ready**.
