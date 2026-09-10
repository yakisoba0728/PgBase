# pgBase

pgBase is a PostgreSQL-backed application backend with an embedded administrator UI. It provides schema planning, typed records and relations, authentication and record rules, local/S3 files, realtime events, TypeScript and Dart SDKs, trusted JavaScript extensions, an opt-in SQL console, self-update tooling, and administrator tools for data, logs, jobs, and backups. Current development remains early v0.x with evolving contracts; the initial v0.1.0 release and the current source have different feature sets.

As of 2026-09-11, N1–N5 is merged through [PR #34](https://github.com/yakisoba0728/PgBase/pull/34) and installed in the existing local project (PostgreSQL 17, schema v40). N6–N9 JSVM, Dart SDK, SQL console, and self-update work (schema v41) is merged through [PR #35](https://github.com/yakisoba0728/PgBase/pull/35) at `53a4741`, but has not been installed in that project or publicly released. The separate PocketBase Round 3 integration is locally verified at `bd76b1d1` and is not recorded as installed or released. Start with [implementation history and current status](docs/project-status.md), [next work and acceptance criteria](docs/next-work.md), and the [current PocketBase comparison](docs/pocketbase-feature-comparison-current-2026-09-10.md).

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
- [Current PocketBase comparison and remaining gaps](docs/pocketbase-current-reassessment.md)
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

M1–M6 and P1–P8 have been merged; N1–N5 are merged and installed. The local workflows include editable authentication settings and mail templates, record bulk operations, schema transfer, file transformations, multipart batches, persistent request logs, calendar jobs, managed backup/restore, and Go custom routes. The source additionally contains locally accepted N6–N9 extension, SDK, console, and updater work. See [live implementation status](TODO.md) for completed acceptance and remaining rollout work. Source implementation does not imply a new published release.

Start PostgreSQL, PgBase and the local mail inbox with `scripts/dev-local.sh`. See [local development](docs/local-development.md), [local verification](docs/local-verification.md), [realtime](docs/realtime.md), and [extension/client scope](docs/extensions-and-client-scope.md).

현재 상태는 [구현 이력](docs/project-status.md), [후속 작업](docs/next-work.md), [PocketBase 재평가](docs/pocketbase-current-reassessment.md)를 참고하세요. 과거 실행별 수치는 [로컬 검증 기록](docs/local-verification.md)에 보존합니다.
