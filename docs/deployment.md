# Deployment

pgBase is pre-v0.1. Validate every release in a staging copy, keep a current backup, and expect configuration, API, and migration procedures to change during v0.x.

## Database and instance topology

One pgBase project owns one PostgreSQL database. That database contains both the `pgbase_meta` system schema and the `app` collection schema. Do not share one database between unrelated projects or manage pgBase-owned objects outside pgBase.

Multiple pgBase instances for the same project may share the database. They must use the same `PGBASE_TOKEN_SECRET`, `PGBASE_PUBLIC_URL`, trusted proxy policy, and administrator-facing origin policy. Schema changes are serialized in PostgreSQL and other instances synchronize through PostgreSQL `LISTEN`; readiness is false while an instance cannot synchronize.

## Required production sequence

Production migration application is explicit. `PGBASE_AUTO_MIGRATE` defaults to `false`; keep it false or unset outside the bundled development Compose stack.

For each deployment:

1. Back up both `pgbase_meta` and `app` as described in [Backup and restore](backup-restore.md).
2. Run the new release's `pgbase migrate status` against the target database.
3. Run the new release's `pgbase migrate up` exactly once. The migration runner serializes concurrent attempts, but one nominated migration job is easier to operate.
4. Run `pgbase migrate status` again and require `current` to equal `latest` with `dirty=false`.
5. Start or roll out `pgbase serve` instances.
6. Require `/health/ready` to return HTTP 200 before adding each instance to load balancing.

The bundled Compose stack is for development and has `PGBASE_AUTO_MIGRATE=true`. To exercise an explicit migration with that image, start PostgreSQL first and override the setting for the one-off command:

```console
docker compose --env-file deployments/.env -f deployments/docker-compose.yml up -d postgres --wait
docker compose --env-file deployments/.env -f deployments/docker-compose.yml run --rm -e PGBASE_AUTO_MIGRATE=false pgbase migrate status
docker compose --env-file deployments/.env -f deployments/docker-compose.yml run --rm -e PGBASE_AUTO_MIGRATE=false pgbase migrate up
```

Do not use `docker compose ... up pgbase` as a production migration job unless the Compose service is overridden to `PGBASE_AUTO_MIGRATE=false`.

## Configuration

The production binary reads configuration from environment variables:

| Variable | Requirement and behavior |
| --- | --- |
| `PGBASE_DATABASE_URL` | Required `postgres://` or `postgresql://` URL. Use TLS verification such as `sslmode=verify-full` for a remote database. |
| `PGBASE_TOKEN_SECRET` | Required base64 text decoding to at least 32 bytes. All instances must share it. |
| `PGBASE_SETUP_TOKEN` | Set to an unguessable value of at least 32 characters before first-administrator setup. |
| `PGBASE_HTTP_ADDRESS` | Listener address; default `:8090`. |
| `PGBASE_PUBLIC_URL` | Exact external `http` or `https` origin, with no path, query, fragment, credentials, or wildcard. Set it in production. |
| `PGBASE_AUTO_MIGRATE` | Boolean; default `false`. Use `true` only for the bundled development Compose path. |
| `PGBASE_TRUSTED_PROXIES` | Comma-separated, masked CIDR prefixes for proxies allowed to supply forwarded scheme/address data. Empty by default. |
| `PGBASE_CORS_ALLOWED_ORIGINS` | Comma-separated exact `http`/`https` origins. Wildcards are rejected. Leave empty for same-origin administration. |
| `PGBASE_ADMIN_SESSION_TTL` | Go duration; positive and no greater than `12h`; default `12h`. |
| `PGBASE_STATEMENT_TIMEOUT` | Go duration; default `5s`. |
| `PGBASE_SHUTDOWN_TIMEOUT` | Go duration; default `15s`. |

Never put secret values in command arguments, source control, images, support bundles, or logs. Supply them through a secret manager or protected environment injection. The JSON logger redacts known credentials, but redaction is not a substitute for avoiding secret-bearing log messages.

## TLS and reverse proxies

pgBase serves HTTP and does not expose a TLS certificate flag. Terminate TLS at a reverse proxy or platform load balancer, keep the backend listener private, and set `PGBASE_PUBLIC_URL=https://your-exact-host.example`.

Configure `PGBASE_TRUSTED_PROXIES` with only the actual proxy network ranges. Forwarded scheme and client addresses are ignored from an untrusted peer. pgBase walks `X-Forwarded-For` from the trusted side and selects the first untrusted address; malformed chains fall back to the direct peer. Do not trust `0.0.0.0/0` or `::/0` merely to make proxying work.

Administrator mutations require an exact matching `Origin`, a valid administrator cookie, and `X-PgBase-CSRF`. Cross-origin browser deployments additionally require an exact entry in `PGBASE_CORS_ALLOWED_ORIGINS`; `*` is rejected. With an HTTPS public URL, administrator cookies are issued with `Secure`.

## External PostgreSQL

- Use a dedicated database and role for one pgBase project.
- Grant the role permission to create and modify objects in `pgbase_meta` and `app`; migrations and collection DDL require it.
- Require encrypted, verified transport for non-local PostgreSQL.
- Size connection limits for all pgBase instances plus migration, backup, and operator connections.
- Back up both pgBase schemas together. See [Backup and restore](backup-restore.md).

## Health, rollout, and shutdown

`GET /health/live` returns HTTP 200 while the process HTTP path is alive; a PostgreSQL outage alone does not make liveness fail. `GET /health/ready` returns HTTP 200 only while HTTP is accepting, PostgreSQL is reachable, system migrations are current, and the schema listener is synchronized. A failed readiness response is HTTP 503 with safe component names, not raw dependency errors.

Use liveness only to detect a stuck process. Use readiness for load-balancer admission and rollout gates. The container health check invokes `/pgbase healthcheck --url http://127.0.0.1:8090/health/ready` and requires no shell or curl.

On SIGTERM, pgBase marks readiness false, stops accepting new requests, cancels background work, and drains HTTP requests for up to `PGBASE_SHUTDOWN_TIMEOUT`. Set the orchestrator termination grace period longer than that timeout. Requests exceeding the grace period can be interrupted.

## Secret rotation

- Rotating `PGBASE_TOKEN_SECRET` invalidates existing administrator sessions, CSRF tokens, signed cursors, and the current rate-limit hash namespace. Update all instances together and expect administrators to sign in again.
- Rotating `PGBASE_SETUP_TOKEN` changes only first-administrator bootstrap credentials. After an administrator exists, setup stays unavailable regardless of the token.
- `pgbase admin reset-password --email EMAIL --password-stdin` replaces an administrator password and revokes all sessions for that administrator.
- Rotate a PostgreSQL password in a coordinated database/application change so old connections can drain and new instances receive the new `PGBASE_DATABASE_URL` without exposing it.

## Zero-downtime limits

Multi-instance serving is supported against one shared database, but zero-downtime upgrades are not guaranteed. The Production Kernel has no online-DDL mechanism, down-migration command, replica routing, or compatibility promise between arbitrary v0.x binaries and schemas. Collection DDL can acquire PostgreSQL locks. A system migration may require a maintenance window; follow release-specific guidance and prefer a tested stop-migrate-start sequence whenever compatibility is uncertain.
