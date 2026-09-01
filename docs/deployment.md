# Deployment

pgBase is pre-v0.1. Validate every release in a staging copy, keep a current backup, and expect configuration, API, and migration procedures to change during v0.x.

## Database and instance topology

One pgBase project owns one PostgreSQL database. That database contains the `pgbase_meta` system/catalog schema, the `app` collection schema, and the `pgbase_relation` schema used by ordered multiple-relation edge tables and their constraint functions. Do not share one database between unrelated projects or manage pgBase-owned objects outside pgBase.

Multiple pgBase instances for the same project may share the database. They must use the same `PGBASE_TOKEN_SECRET`, `PGBASE_PUBLIC_URL`, trusted proxy policy, and administrator-facing origin policy. Schema changes are serialized in PostgreSQL and other instances synchronize through PostgreSQL `LISTEN`; readiness is false while an instance cannot synchronize.

## Required production sequence

Production migration application is explicit. `PGBASE_AUTO_MIGRATE` defaults to `false`; keep it false or unset outside the bundled development Compose stack.

For each deployment:

1. Back up `pgbase_meta`, `app`, and `pgbase_relation` together as described in [Backup and restore](backup-restore.md). Drain old writers before a release that introduces or changes record-operation or relation artifacts; mixed old/new writers can bypass revision, audit, outbox, relation-locking, or logical delete-action invariants.
2. Run the new release's `pgbase migrate status` against the target database.
3. Run the new release's `pgbase migrate up` exactly once. The migration runner serializes concurrent attempts, but one nominated migration job is easier to operate.
4. Run `pgbase migrate status` again and require `current` to equal `latest` with `dirty=false`.
5. Start or roll out `pgbase serve` instances.
6. Require `/health/ready` to return HTTP 200 before adding each instance to load balancing.

If the release declares a single optional compatibility migration, replace step 3 with the checksum-pinned `pgbase migrate up --through TARGET_VERSION` job and retain its JSON receipt. Follow the [P0 -> M -> F procedure and rollback matrix](upgrades.md#one-migration-compatibility-rollout); the ordinary `migrate up` sequence is not a substitute for that release gate.

The bundled Compose stack is for development and has `PGBASE_AUTO_MIGRATE=true`. To exercise an explicit migration with that image, start PostgreSQL first and override the setting for the one-off command:

```console
docker compose --env-file deployments/.env -f deployments/docker-compose.yml up -d postgres --wait
docker compose --env-file deployments/.env -f deployments/docker-compose.yml run --rm -e PGBASE_AUTO_MIGRATE=false pgbase migrate status
docker compose --env-file deployments/.env -f deployments/docker-compose.yml run --rm -e PGBASE_AUTO_MIGRATE=false pgbase migrate up
```

Do not use `docker compose ... up pgbase` as a production migration job unless the Compose service is overridden to `PGBASE_AUTO_MIGRATE=false`.

## Release image verification status

Deploy immutable `image@sha256:...` references rather than relying on a mutable tag. The current tag workflow runs backend, frontend, E2E, security, and backup/restore jobs before it publishes an image, but the image-dependent jobs do not consume one shared candidate: E2E builds a Compose image from source, security builds a separate audit image, backup/restore builds a Go binary from source, and the release job then performs another Buildx build for publication.

This ordering proves that the same source revision passed those jobs; it does not prove that the final registry digest itself passed E2E, image scanning, or restore validation. The release workflow does generate SBOM and provenance attestations for the published digest. Until a single-candidate, digest-pinned gate and rebuild-free promotion path is implemented, verify the exact chosen digest in staging and retain that digest with the deployment record instead of describing the published image as same-artifact tested.

## Configuration

The production binary reads configuration from environment variables:

| Variable | Requirement and behavior |
| --- | --- |
| `PGBASE_DATABASE_URL` | Required `postgres://` or `postgresql://` URL. Use TLS verification such as `sslmode=verify-full` for a remote database. |
| `PGBASE_TOKEN_SECRET` | Required base64 text decoding to at least 32 bytes. All instances must share it. |
| `PGBASE_SETUP_TOKEN` | Set to an unguessable value of at least 32 characters before first-administrator setup. |
| `PGBASE_METRICS_TOKEN` | Optional bearer-token-safe secret of 32–256 bytes. When set, enables `GET /metrics`; when empty, the route does not exist. Use a distinct secret per monitoring trust boundary. |
| `PGBASE_HTTP_ADDRESS` | Listener address; default `:8090`. |
| `PGBASE_PUBLIC_URL` | Exact external `http` or `https` origin, with no path, query, fragment, credentials, or wildcard. Set it in production. |
| `PGBASE_AUTO_MIGRATE` | Boolean; default `false`. Use `true` only for the bundled development Compose path. |
| `PGBASE_TRUSTED_PROXIES` | Comma-separated, masked CIDR prefixes for proxies allowed to supply forwarded scheme/address data. Empty by default. |
| `PGBASE_CORS_ALLOWED_ORIGINS` | Comma-separated exact `http`/`https` origins. Wildcards are rejected. Leave empty for same-origin administration. |
| `PGBASE_ADMIN_SESSION_TTL` | Go duration; positive and no greater than `12h`; default `12h`. |
| `PGBASE_STATEMENT_TIMEOUT` | Go duration; default `5s`. Applies to ordinary request and diagnostic SQL. System migrations and approved collection DDL disable this statement limit locally so large schema work can finish; they retain a 5-second lock-acquisition timeout and remain cancelable through the command/request context. |
| `PGBASE_SHUTDOWN_TIMEOUT` | Go duration; default `15s`. |
| `PGBASE_STOP_GRACE_PERIOD` | Compose stop grace Go duration; default `20s`. When set, it must be at least `PGBASE_SHUTDOWN_TIMEOUT + 5s`; pgBase refuses startup when the margin is smaller. |

Never put secret values in command arguments, source control, images, support bundles, or logs. Supply them through a secret manager or protected environment injection. The JSON logger redacts known credentials, but redaction is not a substitute for avoiding secret-bearing log messages.

## Record mutation safety

Use a new 16–128 character `Idempotency-Key` for each logical record create and keep the same key and JSON body when retrying an unknown response. A successful first execution and replay both return 201 with the original `ETag` and `Location`; `Idempotency-Replayed` distinguishes them. Reusing the key for another body returns 409. Raw keys are not stored, and completed replay state expires after 24 hours; every instance drains expired rows in 256-row `SKIP LOCKED` transactions with a short yield between full batches.

Read one record before editing and send its strong `ETag` in `If-Match` for PATCH or DELETE. A 412 means another committed mutation changed the record; fetch the current representation and require a human/application merge instead of blindly retrying. The bundled administrator UI follows this contract.

Pre-v0.1 `/api/v1` requests without these headers remain a deliberate compatibility path and receive `PgBase-Compatibility`. They do not receive duplicate-create or stale-write protection. Do not use that path for production automation. The standard `Deprecation` response header is reserved for deprecating an API resource itself with an RFC 9745 date and policy link. The outbox created by these mutations is durable source data only; this release has no delivery worker, lease, retention, webhook, or realtime guarantee.

Single relation values are canonical UUID strings or `null`; their bounds are `minSelect` 0 or 1 and `maxSelect` 1. Multiple relation values are ordered, duplicate-free UUID arrays with `maxSelect` from 2 to 1,000 and `minSelect <= maxSelect`. `required` must agree with `minSelect > 0`, `set_null` requires `minSelect=0`, relation defaults are unsupported, and only single relations may be unique. Self relations are supported; automatic single/multiple conversion is not.

`restrict`, `cascade`, and `set_null` are logical delete actions executed inside the same Record Operations transaction. Cascade is resolved to a fixed-point closure; a restrict edge wholly inside that closure does not block deletion, while a restrict source outside it rolls back the request. Each surviving `set_null` record receives one new revision/ETag plus one audit and outbox event; each root/cascade deletion receives an independent tombstone event and has its revision sidecar removed. A delete graph is bounded to depth 32, 10,000 affected records, and 100,000 examined edges; exceeding a bound rolls back the whole mutation. Delete planning requires a Read Committed transaction and acquires the schema version share lock, participating collection advisory locks in UUID order, and then affected record locks in deterministic order to close phantom-write gaps.

Use only the record API or the Record Operations Go seam for record and relation mutation. Direct SQL against `app`, `pgbase_relation`, or record sidecar tables is unsupported: it can bypass target validation and lock ordering, leave ordered edges or logical delete actions inconsistent, and omit revisions, ETags, audit, and outbox artifacts. Physical target foreign keys are a deferred commit-time backstop, not a replacement for the mutation kernel.

## Bounded relation reads

Raw record responses retain relation UUIDs in their stored order. `?expand=` adds selected forward dot paths or `collection_via_field` reverse paths under a sibling `expand` object; expanded targets do not change the root ETag or cursor material. The current hard limits are a 4 KiB expand expression, depth 6, 20 unique nodes, 10,000 fetched rows across the request, 1,000 rows per reverse node, 64 query steps, and a 4 MiB encoded response/page. Exceeding a bound fails with a stable validation response instead of returning an unbounded partial traversal.

The ordinary filter parser is limited to 4 KiB and 100 syntax nodes. Direct single relation ID equality and multiple relation membership are supported; relation-path filters use bounded `EXISTS` semantics and are additionally limited to depth 3 and eight join nodes. Sort accepts at most eight requested keys; relation-path sort is limited to depth 2 and to single-valued paths ending at an indexed or unique scalar field. Because exact policy-aware `NULLS LAST` relation ordering can still require a root-wide join and sort, it is rejected with `relation_sort_budget_exceeded` once the root collection contains more than 100,000 visible rows in the request snapshot; the guard probes at most 100,001 primary-key entries before executing target joins. Multiple-relation sort and arbitrary joins are rejected. List page size is capped at 200 records and signed cursors expire after 15 minutes. These are safety ceilings, not capacity or latency guarantees; validate representative shapes against production-like data before rollout and use an application-maintained indexed scalar projection when a larger collection needs that ordering.

## Metrics and initial SLO evidence

Metrics are disabled by default. Set `PGBASE_METRICS_TOKEN` to expose a Prometheus-compatible `GET /metrics` endpoint on the application listener. Scrapers must send exactly one `Authorization: Bearer <token>` header. Keep the listener private behind a monitoring network or authenticated reverse proxy even though pgBase also verifies the bearer token. The response is never cached.

The supplied alerts assume the Prometheus job is named `pgbase`. A minimal static scrape uses Prometheus's file-backed HTTP authorization so the token is not embedded in the configuration:

```yaml
scrape_configs:
  - job_name: pgbase
    scheme: https
    metrics_path: /metrics
    authorization:
      type: Bearer
      credentials_file: /run/secrets/pgbase_metrics_token
    static_configs:
      - targets: ["pgbase.example.com:443"]
```

The credentials file must be readable only by the scraper and contain the same secret injected as `PGBASE_METRICS_TOKEN`. Replace the static target with the deployment platform's service discovery when appropriate. If a different job name is required, update the `PgBaseMetricsScrapeUnavailable` selector in the supplied rules at the same time. See the official [Prometheus HTTP configuration reference](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#http_config).

Keep an orchestrator or load-balancer probe polling `/health/ready`; the database and migration gauges represent the latest bounded readiness evaluation rather than running a second telemetry-only database poller. The bundled container health check already provides this evaluation. HTTP acceptance and schema-listener gauges have lifecycle-owned writers and do not depend on scrape traffic.

The HTTP series use the registered Go route template, method, and bounded status class. They never label raw paths, collection or record identifiers, query strings, email addresses, client IPs, credentials, SQL, or error text. Pool, transaction-retry, schema/listener, readiness, migration, administrator-auth, and relation schema/write/delete/expand series follow the same bounded-label rule. Each pgBase instance owns a private registry; aggregate replicas in Prometheus rather than sharing in-process state. The numeric series ceiling and histogram bucket budget are recorded in the [operational telemetry design](superpowers/specs/2026-08-31-operational-telemetry-design.md#초기-series-예산).

Start capacity work by recording at least three comparable staging runs, then define latency and error objectives from those measurements. The supplied dashboard and alerts are initial operational guardrails, not a throughput or SLO guarantee. Do not weaken route limits, query bounds, pool headroom, or readiness policy solely to make a dashboard green.

Import [`deployments/observability/grafana-dashboard.json`](../deployments/observability/grafana-dashboard.json) into Grafana and load [`deployments/observability/prometheus-alerts.yml`](../deployments/observability/prometheus-alerts.yml) as Prometheus-compatible rules. The 5xx and pool thresholds are deliberately labeled initial guardrails; replace them only after retaining the staging evidence used to choose the new objective.

## TLS and reverse proxies

pgBase serves HTTP and does not expose a TLS certificate flag. Terminate TLS at a reverse proxy or platform load balancer, keep the backend listener private, and set `PGBASE_PUBLIC_URL=https://your-exact-host.example`.

Configure `PGBASE_TRUSTED_PROXIES` with only the actual proxy network ranges. Forwarded scheme and client addresses are ignored from an untrusted peer. pgBase walks `X-Forwarded-For` from the trusted side and selects the first untrusted address; malformed chains fall back to the direct peer. Do not trust `0.0.0.0/0` or `::/0` merely to make proxying work.

The bundled cookie-authenticated administrator UI must share the API origin. Administrator mutations require that exact `Origin`, a valid administrator cookie, and `X-PgBase-CSRF`. `PGBASE_CORS_ALLOWED_ORIGINS` controls browser response visibility and preflight only; it does not grant setup, login, or mutation authority to another origin. A future cross-origin administrator client requires credentials designed separately from the `SameSite=Lax` cookie. Wildcards are rejected. With an HTTPS public URL, administrator cookies are issued with `Secure`.

## External PostgreSQL

- Use a dedicated database and role for one pgBase project.
- Grant the role permission to create and modify objects in `pgbase_meta`, `app`, and `pgbase_relation`; migrations and collection/relation DDL require it.
- Require encrypted, verified transport for non-local PostgreSQL.
- Size connection limits for all pgBase instances plus migration, backup, and operator connections.
- Back up all three pgBase schemas together. See [Backup and restore](backup-restore.md).

## Health, rollout, and shutdown

`GET /health/live` returns HTTP 200 while the process HTTP path is alive; a PostgreSQL outage alone does not make liveness fail. `GET /health/ready` returns HTTP 200 only while HTTP is accepting, PostgreSQL is reachable, system migrations are current, and the schema listener is synchronized. A failed readiness response is HTTP 503 with safe component names, not raw dependency errors.

Use liveness only to detect a stuck process. Use readiness for load-balancer admission and rollout gates. The container health check invokes `/pgbase healthcheck --url http://127.0.0.1:8090/health/ready` and requires no shell or curl.

On SIGTERM, pgBase marks readiness false, stops accepting new requests, cancels background work, and drains HTTP requests for up to `PGBASE_SHUTDOWN_TIMEOUT`. The bundled Compose profile applies `PGBASE_STOP_GRACE_PERIOD` to the container and requires at least a five-second margin over the application timeout. Keep the equivalent margin in other orchestrators. Requests exceeding the grace period can be interrupted.

## Secret rotation

- Rotating `PGBASE_TOKEN_SECRET` invalidates existing administrator sessions, CSRF tokens, signed cursors, and the current rate-limit hash namespace. Update all instances together and expect administrators to sign in again.
- Rotating `PGBASE_SETUP_TOKEN` changes only first-administrator bootstrap credentials. After an administrator exists, setup stays unavailable regardless of the token.
- Rotating `PGBASE_METRICS_TOKEN` requires a coordinated application rollout and Prometheus credentials-file reload. One pgBase instance accepts one metrics token with no old/new overlap, so keep the scrape-down alert under maintenance or use separate old/new scrape jobs for the two target batches during a zero-blind-spot rolling rotation; remove the old job only after every new-token target reports `up == 1`.
- `pgbase admin reset-password --email EMAIL --password-stdin` replaces an administrator password, revokes all sessions for that administrator, and clears that account's login lockout. Use it as the host-authorized recovery path after repeated failed logins.
- Rotate a PostgreSQL password in a coordinated database/application change so old connections can drain and new instances receive the new `PGBASE_DATABASE_URL` without exposing it.

## Zero-downtime limits

Multi-instance serving is supported against one shared database, but zero-downtime upgrades are not guaranteed. The Production Kernel has no online-DDL mechanism, down-migration command, read-replica routing, or compatibility promise between arbitrary v0.x binaries and schemas. Collection and relation DDL can acquire PostgreSQL locks. A system migration may require a maintenance window; follow release-specific guidance and prefer a tested stop-migrate-start sequence whenever compatibility is uncertain.
