# pgBase

pgBase is a PostgreSQL-backed application backend with an embedded administrator UI. It provides schema planning, typed records and relations, authentication and record rules, local/S3 files, realtime events, TypeScript and Dart SDKs, trusted JavaScript extensions, an opt-in SQL console, self-update tooling, and administrator tools for data, logs, jobs, and backups. Current development remains early v0.x with evolving contracts; the initial v0.1.0 release and the current source have different feature sets.

PgBase is an independent project inspired by PocketBase. Its PostgreSQL data model, authentication, APIs, and operational contracts guide development. See the [2026-09-22 implementation and next steps](docs/workstreams/independent-product-2026-09-22/README.md); the PocketBase comparison is reference material, not a requirement for identical behavior.

The current source includes schema 66: SQL view reference validation (including persisted views), authentication-safe migration replay, service-token request-log identities, indexed log/email search, and administrator record-page file metadata. Administrator security writes require `currentPassword`. See [current status](docs/project-status.md), [readiness changes and verification](docs/production-readiness-2026-11-13.md), and [remaining work](docs/next-work.md). Historical implementation and deployment records are retained in those documents.

## Quick start

1. Copy `deployments/.env.example` to `deployments/.env` and generate all three required secrets as described in [the quickstart](docs/quickstart.md#1-create-the-local-environment-file).
2. Start PostgreSQL 16.13 and pgBase:

   ```console
   docker compose --env-file deployments/.env -f deployments/docker-compose.yml up -d --build --wait
   ```

3. Open <http://127.0.0.1:8090/admin>, enter the setup token from `deployments/.env`, and create the first administrator.

The bundled Compose file is a development convenience and sets `PGBASE_AUTO_MIGRATE=true`. Production deployments must apply migrations explicitly before starting or rolling out servers; see [Deployment](docs/deployment.md).

## Documentation

- [한국어 지원과 PocketBase 스타일 화면 개선](docs/admin-korean-ui.md)
- [2026-11-13 새 관리자 UI 병합과 운영 배포](docs/deployment-2026-11-13.md)
- [2026-11-12 서버 배포 기록](docs/deployment-2026-11-12.md)
- [PocketBase-inspired administrator UI rebuild and verification](docs/admin-ui-rebuild.md)
- [Dokploy 배포: Compose, HTTPS, 영구 저장소와 백업](docs/dokploy.md)
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

Relation mutations use the record API/Record Operations kernel. Direct writes to pgBase-owned PostgreSQL objects can bypass ETag/revision, audit, outbox, logical delete actions, and relation locking and are outside that mutation contract. Cross-project relations, relation edge attributes, automatic single/multiple relation conversion, online DDL, read-replica routing, and sharding are not provided. JSVM, the Dart SDK, SQL console and self-update are implemented; SQL console activation remains opt-in. There is no throughput, production SLO, or zero-downtime upgrade guarantee.

## Local development features

The administrator UI, schema and record services, TypeScript/Dart clients, authentication settings, mail templates, file storage, jobs, backup/restore and extension APIs are implemented. Source, production deployment and tagged image releases are tracked separately in [project status](docs/project-status.md). Production backup configuration is intentionally deferred at the owner's request on 2026-11-13.

Start PostgreSQL, PgBase and the local mail inbox with `scripts/dev-local.sh`. See [local development](docs/local-development.md), [local verification](docs/local-verification.md), [realtime](docs/realtime.md), and [extension/client scope](docs/extensions-and-client-scope.md).

현재 상태는 [구현 이력](docs/project-status.md), [후속 작업](docs/next-work.md), [PocketBase 재평가](docs/pocketbase-current-reassessment.md)를 참고하세요. 과거 실행별 수치는 [로컬 검증 기록](docs/local-verification.md)에 보존합니다.
