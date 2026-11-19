# PgBase

PgBase is an independent PostgreSQL application backend with an embedded administrator UI. Build applications with typed collections and relations, authentication and action-level authorization, file storage, realtime events, background jobs and programmable extensions—without assembling a separate service for every feature.

Start with one application instance and one PostgreSQL database. Run additional PgBase instances for the same project when the workload calls for it. PostgreSQL owns durable application state; transaction, authorization and failure-recovery contracts take priority over compatibility with other backends.

**Status:** early v0.x. Interfaces and database contracts may change. The latest published release is [v0.4.0](https://github.com/yakisoba0728/PgBase/releases/tag/v0.4.0), based on schema 69. Current source requires schema 71. Source changes, release artifacts and production deployments are separate states. The [independent-platform workstream](docs/workstreams/independent-platform-2026-11-16/README.md) records the current redesign scope and verification gaps. It is not a completed production-readiness certification.

## Capabilities

- **Data:** schema planning, typed records, relations, compound indexes, read-only SQL views, cursor pagination and transactional record operations.
- **Identity and authorization:** administrator sessions, named user realms, OAuth, MFA, email OTP, service tokens and typed action rules, including supported relation paths.
- **Files and events:** local or S3 storage, file authorization, image variants, realtime subscriptions and durable background work.
- **Development:** TypeScript and Dart SDK source, an OpenAPI route manifest, trusted JavaScript extensions, custom routes, CLI tools and an opt-in SQL console.
- **Administration:** an embedded Korean/English UI for schema, records, users, settings, logs, jobs and configured backup/restore facilities; self-update tooling and observability integrations.

Availability depends on configuration. In particular, SQL console activation is opt-in and managed backups require configuration. SDK source availability does not imply publication to npm or pub.dev.

## Quick start

1. Copy `deployments/.env.example` to `deployments/.env` and generate all three required secrets as described in [the quickstart](docs/quickstart.md#1-create-the-local-environment-file).
2. Start PostgreSQL and PgBase:

   ```console
   docker compose --env-file deployments/.env -f deployments/docker-compose.yml up -d --build --wait
   ```

3. Open <http://127.0.0.1:8090/admin>, enter the setup token from `deployments/.env`, and create the first administrator.

The bundled Compose stack is for development and sets `PGBASE_AUTO_MIGRATE=true`. Production deployments must apply migrations explicitly before starting or rolling out servers; see [Deployment](docs/deployment.md). Never reuse development secrets in production.

For a local mail inbox and development workflow, use `scripts/dev-local.sh` and [local development](docs/local-development.md).

## Architecture and operating boundaries

One PgBase project owns one PostgreSQL database. Instances for that project must share compatible secrets and configuration, and use an appropriate shared file-storage arrangement. Do not point unrelated projects at one PgBase database.

Use the record APIs or Record Operations kernel for mutations. Direct writes to PgBase-owned PostgreSQL objects bypass revision/ETag, audit, outbox, logical delete actions and relation-locking contracts and are outside the supported mutation boundary.

The v0.x HTTP, administrator, CLI, configuration, database and backup contracts can change without backwards compatibility. The `/api/v1` prefix does not promise v1 stability. Cross-project relations, relation edge attributes, automatic single/multiple relation conversion, online DDL, read-replica routing and sharding are not provided.

Large-scale use must be qualified against a concrete workload, deployment topology and failure model. There is no blanket throughput, production SLO or zero-downtime upgrade guarantee. Functional tests, performance measurements, recovery drills and operational acceptance are separate evidence.

## Documentation

| Area | References |
| --- | --- |
| Getting started | [Quickstart](docs/quickstart.md), [local development](docs/local-development.md), [user access](docs/user-access.md) |
| Data and architecture | [Backend design](docs/backend-redesign.md), [rewrite verification](docs/backend-rewrite-validation.md), [record operations](docs/superpowers/specs/2026-08-31-record-operations-design.md) |
| Application development | [User management](docs/user-management.md), [realtime](docs/realtime.md), [extensions and clients](docs/extensions-and-client-scope.md) |
| Operations | [Deployment](docs/deployment.md), [Dokploy](docs/dokploy.md), [backup/restore](docs/backup-restore.md), [upgrades](docs/upgrades.md), [security policy](SECURITY.md) |
| Evidence and work | [Current workstream](docs/workstreams/independent-platform-2026-11-16/README.md), [implementation history](docs/project-status.md), [runtime verification](docs/runtime-verification.md), [remaining work](docs/next-work.md) |

Historical research and deployment records are retained as dated evidence, not as the current product definition or a guarantee of compatibility. Required third-party copyright and license notices remain applicable.
