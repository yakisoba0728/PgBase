# pgBase

pgBase is a PostgreSQL-backed application backend with an embedded administrator UI. It provides schema planning, typed records and relations, authentication and record rules, local/S3 files, realtime events, TypeScript and Dart SDKs, trusted JavaScript extensions, an opt-in SQL console, self-update tooling, and administrator tools for data, logs, jobs, and backups. Current development remains early v0.x with evolving contracts; the initial v0.1.0 release and the current source have different feature sets.

PgBase is an independent project inspired by PocketBase. Its PostgreSQL data model, authentication, APIs, and operational contracts guide development. See the [2026-09-22 implementation and next steps](docs/workstreams/independent-product-2026-09-22/README.md); the PocketBase comparison is reference material, not a requirement for identical behavior.

As of 2026-10-22, `main` is at `5c495e1` ([PR #92](https://github.com/yakisoba0728/PgBase/pull/92)), still schema 65. [PR #87](https://github.com/yakisoba0728/PgBase/pull/87) moved Go to 1.26.10 and `golang.org/x/net` to v0.60.0 and bumped vite/vitest to clear the Go and npm vulnerability findings; [PR #88](https://github.com/yakisoba0728/PgBase/pull/88) consolidated ten Dependabot updates; [PR #89](https://github.com/yakisoba0728/PgBase/pull/89) fixed the release-mode restore drill and an OTP concurrency test flake. The local installation's binary was replaced with `ad7783c` after a backup. The repository was made public on 2026-10-22 so CI runs on GitHub's standard hosted runners, and [PR #90](https://github.com/yakisoba0728/PgBase/pull/90) (`8267984`) runs the release-gate race suite on 17.9 only, split into two parallel shards. [v0.2.3](https://github.com/yakisoba0728/PgBase/releases/tag/v0.2.3) was released from `5c495e1` ([#92](https://github.com/yakisoba0728/PgBase/pull/92)) with all release gates passing; tags `v0.2.0`–`v0.2.2` failed their gates and were not promoted. Details are in [project status](docs/project-status.md). The next paragraph is the 2026-10-01 record.

As of 2026-10-01, `main` is at `31a9694` ([PR #66](https://github.com/yakisoba0728/PgBase/pull/66)) and its system migrations end at `000065` (schema 65). After the 2026-09-11 record below, [PR #36](https://github.com/yakisoba0728/PgBase/pull/36) (`5cb721f`) merged the PocketBase Round 3 scope and the 20 confirmed audit-defect fixes, which moved the schema from v41 to 65; [PR #39](https://github.com/yakisoba0728/PgBase/pull/39) (`9a02663`) merged the 45 maintainability items and the audited parity remediation; [PR #41](https://github.com/yakisoba0728/PgBase/pull/41)/[PR #42](https://github.com/yakisoba0728/PgBase/pull/42) (`7ee5fd6`, `6993744`) fixed CI test flakes and routed main pushes to the self-hosted runners; [PR #54](https://github.com/yakisoba0728/PgBase/pull/54) (`8e78392`) merged native administration, auth policy UI, query/cursor fixes, file byte ranges, EXIF/GIF/WebP thumbnails, view import, TypeScript/Dart SDK helpers, native Windows self-update, and multi-process acceptance; [PR #58](https://github.com/yakisoba0728/PgBase/pull/58) (`5ccab67`) consolidated the October dependency updates; PR #59 aligned the CI Node toolchain on 24.21.0; [PR #60](https://github.com/yakisoba0728/PgBase/pull/60) updated these status documents; [PR #61](https://github.com/yakisoba0728/PgBase/pull/61) (`4f7fbfa`) isolated integration Docker resources on the shared self-hosted runner host; [PR #62](https://github.com/yakisoba0728/PgBase/pull/62) (`c1cd442`) fixed the Windows self-update permission check for elevated (Administrators-owned) installs; [PR #63](https://github.com/yakisoba0728/PgBase/pull/63) removed two timing races from tests; [PR #64](https://github.com/yakisoba0728/PgBase/pull/64) recorded those fixes; [PR #65](https://github.com/yakisoba0728/PgBase/pull/65) (`8568458`) fixed unknown non-API paths returning 500 instead of 404 when no public directory is configured; and PR #66 builds the MinIO test fixture from pinned upstream source because the upstream images are no longer anonymously pullable. The earlier N1–N5 / schema 40 installation was not present on the documenting Mac, so on 2026-10-01 a fresh local installation was created there instead of an upgrade: binary `8568458`, PostgreSQL 17.9, schema 65 at `127.0.0.1:8090`, with doctor, readiness, and the admin UI verified and no administrator created yet. There is no public release after v0.1.0. The scheduled GitHub-hosted workflows were blocked by the account's Actions billing/spending limit; that and other CI and operations items are listed in [project status](docs/project-status.md). The next paragraph is the 2026-09-11 record.

As of 2026-09-11, N1–N5 is merged through [PR #34](https://github.com/yakisoba0728/PgBase/pull/34) and installed in the existing local project (PostgreSQL 17, schema v40). N6–N9 JSVM, Dart SDK, SQL console, and self-update work (schema v41) is merged through [PR #35](https://github.com/yakisoba0728/PgBase/pull/35) at `53a4741`, but has not been installed in that project or publicly released. The separate PocketBase Round 3 integration is locally verified at `bd76b1d1` and is not recorded as installed or released. Start with [implementation history and current status](docs/project-status.md), [next work and acceptance criteria](docs/next-work.md), and the [current PocketBase comparison](docs/pocketbase-feature-comparison-current-2026-09-10.md).

The feature-acceptance comparison is historical evidence, not a current quality verdict. The independent fixed-head audit at `0ea07ff` found 20 product findings and 2 documentation findings; see the [2026-09-11 audit](docs/codebase-audit-2026-09-11.md).

The follow-up fix ledger keeps that audit unchanged while recording all 20 confirmed product-finding fixes and final validation, including the completed structural-budget policy reconciliation: [defect fixes](docs/audits/2026-09-11/defect-fixes.md).

## Quick start

1. Copy `deployments/.env.example` to `deployments/.env` and generate all three required secrets as described in [the quickstart](docs/quickstart.md#1-create-the-local-environment-file).
2. Start PostgreSQL 16.13 and pgBase:

   ```console
   docker compose --env-file deployments/.env -f deployments/docker-compose.yml up -d --build --wait
   ```

3. Open <http://127.0.0.1:8090/admin>, enter the setup token from `deployments/.env`, and create the first administrator.

The bundled Compose file is a development convenience and sets `PGBASE_AUTO_MIGRATE=true`. Production deployments must apply migrations explicitly before starting or rolling out servers; see [Deployment](docs/deployment.md).

## Documentation

- [Implementation history, current status, and verification evidence](docs/project-status.md)
- [Next work: priorities, dependencies, and completion criteria](docs/next-work.md)
- [PocketBase comparison reference (dated evidence)](docs/pocketbase-current-reassessment.md)
- [Public user management and password changes](docs/user-management.md)
- [Historical Production Kernel handoff](docs/production-kernel-handoff.md)
- [Production Kernel tradeoffs and decision triggers](docs/production-kernel-tradeoffs.md)
- [Codebase audit findings and remediation evidence](docs/codebase-audit-2026-08-28.md)
- [Round 4-5 codebase audit and remediation evidence](docs/codebase-audit-2026-09-01.md)
- [Implementation TODO and live work log](TODO.md)
- [Public user authentication and owner access](docs/user-access.md)
- [10-minute quickstart](docs/quickstart.md)
- [Deployment and configuration](docs/deployment.md)
- [Backup and restore](docs/backup-restore.md)
- [Upgrades](docs/upgrades.md)
- [Security policy](SECURITY.md)
- [Approved Production Kernel design](docs/superpowers/specs/2026-08-28-production-kernel-design.md)
- [Approved Production Kernel implementation plan](docs/superpowers/plans/2026-08-28-production-kernel.md)
- [PocketBase reference research and large-scale roadmap](docs/pocketbase-reference-research-2026-08-31.md)
- [Record Operations atomicity design](docs/superpowers/specs/2026-08-31-record-operations-design.md)
- [Record Operations implementation plan](docs/superpowers/plans/2026-08-31-record-operations.md)
- [Relation and schema lifecycle design](docs/superpowers/specs/2026-08-31-relation-schema-lifecycle-design.md)
- [Relation and schema lifecycle implementation plan](docs/superpowers/plans/2026-08-31-relation-schema-lifecycle.md)

## Scope and stability

One pgBase project owns one PostgreSQL database. Multiple pgBase instances for that same project may share that database and must share configuration secrets. Do not point unrelated projects at one pgBase database.

The v0.x HTTP, administrator, command-line, configuration, database, and backup contracts may change without backwards compatibility. The `/api/v1` path is not a promise of v1 stability during v0.x.

Public access uses collection opt-in and typed action rules, including authorization through supported relation paths. Named auth realms, OAuth/MFA/email OTP, files, realtime, TypeScript clients, an OpenAPI route manifest, data-preserving field rename, compound indexes, and read-only SQL views are implemented. PocketBase wire formats, filter syntax, SDKs, and extension APIs are not drop-in compatible.

Relation mutations use the record API/Record Operations kernel. Direct writes to pgBase-owned PostgreSQL objects can bypass ETag/revision, audit, outbox, logical delete actions, and relation locking and are outside that mutation contract. Cross-project relations, relation edge attributes, automatic single/multiple relation conversion, online DDL, read-replica routing, and sharding are not provided. The current source's JSVM, Dart SDK, SQL console, and self-update features are merged but remain separate from the installed/released N1–N5 product until rollout is completed. There is no throughput, production SLO, or zero-downtime upgrade guarantee.

## Local development features

M1–M6 and P1–P8 have been merged; N1–N5 are merged and installed. The local workflows include editable authentication settings and mail templates, record bulk operations, schema transfer, file transformations, multipart batches, persistent request logs, calendar jobs, managed backup/restore, and Go custom routes. The source additionally contains locally accepted N6–N9 extension, SDK, console, and updater work and the later work merged through PR #36–#59; none of it is recorded as installed. See [live implementation status](TODO.md) for completed acceptance and remaining rollout work. Source implementation does not imply a new published release.

Start PostgreSQL, PgBase and the local mail inbox with `scripts/dev-local.sh`. See [local development](docs/local-development.md), [local verification](docs/local-verification.md), [realtime](docs/realtime.md), and [extension/client scope](docs/extensions-and-client-scope.md).

현재 상태는 [구현 이력](docs/project-status.md), [후속 작업](docs/next-work.md), [PocketBase 재평가](docs/pocketbase-current-reassessment.md)를 참고하세요. 과거 실행별 수치는 [로컬 검증 기록](docs/local-verification.md)에 보존합니다.
