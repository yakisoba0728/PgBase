# pgBase

pgBase is a PostgreSQL-backed application backend with an embedded administrator UI. The initial v0.1.0 release is published; current development remains early v0.x with evolving contracts. The current source adds opt-in public user registration, Bearer sessions, and owner-only scalar record access; see [public user access](docs/user-access.md) for the version and scope boundary. It also provides administrator bootstrap and sessions, collection planning/application (create, update, and delete), typed CRUD records including ordered single/multiple relation fields, bounded relation expand/filter/sort, signed cursor pagination, create idempotency, strong ETag concurrency control, committed audit and a transactional outbox writer, health/readiness, protected opt-in Prometheus metrics, explicit system migrations, diagnostics, and a container image.

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

Public user access is limited to enabled collections without incoming or outgoing relations, and users can access only records they create. This kernel does not yet provide configurable Auth collections, OAuth2/MFA, an arbitrary record-rule language, cross-project relations, arbitrary SQL joins, relation edge attributes, files, outbox delivery/realtime, an SDK, generated OpenAPI, online DDL, read-replica routing, or sharding. Relation mutations are supported only through the record API/Record Operations kernel; direct writes to pgBase-owned PostgreSQL objects are outside the contract because they bypass ETag/revision, audit, outbox, logical delete actions, and relation locking. Collection updates cover add, drop, flag, and whitelisted widening type changes (integer→bigint, integer→numeric, bigint→numeric); there is no rename operation (a renamed field is planned as a drop plus add, shown in the SQL preview), automatic single/multiple relation conversion, or composite indexes. It makes no throughput, large-scale-readiness, or zero-downtime upgrade guarantee.
