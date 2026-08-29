# pgBase

pgBase is a PostgreSQL-backed application backend with an embedded administrator UI. This repository is a pre-v0.1 development preview, not a stable release. It currently provides administrator bootstrap and sessions, collection planning/application (create, update, and delete), typed CRUD records with a filter expression and sort language plus signed cursor pagination, health/readiness, explicit system migrations, diagnostics, and a container image.

## Quick start

1. Copy `deployments/.env.example` to `deployments/.env` and generate all three required secrets as described in [the quickstart](docs/quickstart.md#1-create-the-local-environment-file).
2. Start PostgreSQL 16.13 and pgBase:

   ```console
   docker compose --env-file deployments/.env -f deployments/docker-compose.yml up -d --build --wait
   ```

3. Open <http://127.0.0.1:8090/admin>, enter the setup token from `deployments/.env`, and create the first administrator.

The bundled Compose file is a development convenience and sets `PGBASE_AUTO_MIGRATE=true`. Production deployments must apply migrations explicitly before starting or rolling out servers; see [Deployment](docs/deployment.md).

## Documentation

- [Production Kernel final handoff and current status](docs/production-kernel-handoff.md)
- [Production Kernel tradeoffs and decision triggers](docs/production-kernel-tradeoffs.md)
- [Codebase audit findings and remediation evidence](docs/codebase-audit-2026-08-28.md)
- [10-minute quickstart](docs/quickstart.md)
- [Deployment and configuration](docs/deployment.md)
- [Backup and restore](docs/backup-restore.md)
- [Upgrades](docs/upgrades.md)
- [Security policy](SECURITY.md)
- [Approved Production Kernel design](docs/superpowers/specs/2026-08-28-production-kernel-design.md)
- [Approved Production Kernel implementation plan](docs/superpowers/plans/2026-08-28-production-kernel.md)

## Scope and stability

One pgBase project owns one PostgreSQL database. Multiple pgBase instances for that same project may share that database and must share configuration secrets. Do not point unrelated projects at one pgBase database.

The v0.x HTTP, administrator, command-line, configuration, database, and backup contracts may change without backwards compatibility. The `/api/v1` path is not a promise of v1 stability while the project is pre-v0.1.

This kernel does not provide public user authentication, relationships, files, realtime, an SDK, generated OpenAPI, online DDL, replicas, or sharding. Collection updates cover add, drop, flag, and whitelisted widening type changes (integer→bigint, integer→numeric, bigint→numeric); renames and composite indexes are not supported. It makes no throughput or zero-downtime upgrade guarantee.
