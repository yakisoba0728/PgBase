# Remaining development goal — 2026-09-22

This work continues the user's selected local `origin/main` snapshot `6993744a8f36db48ddfeaa8dae017656752d24c7`. Commit `abc94ef` preserves the preceding native account, query, UI, and SDK work. The records in this directory describe the subsequent implementation and acceptance work, not a new PocketBase compatibility requirement. Historical comparison reports remain historical evidence.

## Product changes

| Area | Result and contract |
| --- | --- |
| Query operators | [Literal-left any-match normalization](query.md) preserves comparison direction and contains direction in PostgreSQL and scalar metadata predicates. Corrected query fingerprints require restarting affected old cursors. |
| Managed files | [Single HTTP byte ranges](file-range.md) support original and generated files through local storage and S3, retain authorization, handle HEAD, and preserve compatible custom-store full-response fallback. |
| Images | [EXIF orientation and transparent thumbnails](images.md) handle JPEG orientations 1–8 and PNG output for GIF/WebP. Animated sources produce one still image; bounded animated WebP parsing validates frame dimensions before allocation. |
| Schema import | [Existing declarative views](view-import.md) use the native planner for definition changes and preserve identity, validation, transaction rollback, and dependent-view protections. |
| TypeScript SDK | [Native administrator and authentication helpers](sdk-typescript.md) add TOTP removal, administrator accounts, import receipts, and managed backups, including binary upload/download and interrupted job receipts. |
| Dart SDK | [Equivalent native helper coverage](sdk-dart.md) preserves exact JSON and cancellation, streams backup uploads, and deprecates an already-existing ignored TOTP projection parameter without breaking callers. |
| Recovery | [Historical source upgrade and rollback](upgrade-restore.md) uses populated old-source fixtures and a matched database/files recovery set. The regular native restore drill also runs on PostgreSQL 16, 17, and 18. |
| Windows update | [Native self-update acceptance](windows-update.md) verifies signed update handoff, canonical-path health, explicit and automatic rollback, real owner/ACL validation, and process coordination across executable replacement. |
| Process failure | [Bounded multi-process acceptance](multi-process.md) exercises concurrent writers, shared idempotency, realtime resume, process kill/restart, and session revocation across two real server processes. |

Cross-review found additional defects after initial focused tests: scalar predicate normalization, presentation-only view imports with dependents, malformed decimal range syntax, S3 range metadata/not-found handling, custom object-store capability detection, animated WebP decoding, and Windows updater permission/locking behavior. Each resolved item is recorded with its applicable regression evidence rather than inferred from another feature's test result.

## Verification scope

Feature records contain focused test commands and observed results. [The real HTTP SDK harness](../../../tests/sdk-acceptance/README.md) exercises external consumers against an isolated server. CI now includes native Windows updater tests and bounded multi-process acceptance on main/manual source runs; the source-only historical upgrade drill remains separate from digest-pinned release-candidate restore validation.

[Final local verification](validation.md) records the combined test results, exact source accounting, review corrections, and the remote CI/environment limits.

The previous UI slice's 624 unit tests and eight Chromium flows are recorded in [the checkpoint validation](../independent-product-2026-09-22/continuation-validation.md). This goal adds backend, SDK, and operational work; it does not replace those historical results with claims of a fresh UI run.

## Environment limits

The user confirmed that no real previous-installation copy, backup, or SMTP/OAuth/S3 provider configuration is available on this Windows host. No production credentials were requested in chat. Local mail/OAuth fixtures, MinIO, and historical-source recovery fixtures establish reproducible local behavior, not external delivery, provider-account acceptance, or recovery of a real installation.

Public npm/pub packages, release images, production deployment, external-provider acceptance, and production load/RPO/RTO certification are separate release or deployment activities. They are not claimed as completed development tests. The remaining real-environment acceptance requires an actual deployment and its provider configuration; it is not an unimplemented replacement for the delivered local feature paths.
