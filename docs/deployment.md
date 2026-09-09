# Deployment

Validate every pgBase release in a staging copy, keep a current backup, and expect configuration, API, and migration procedures to change during v0.x.

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

## Release image verification

Deploy immutable `image@sha256:...` references rather than relying on a mutable tag.

A release tag runs a tag/version preflight first. Only after it accepts the tag does the workflow build and push exactly one candidate image, and that candidate's digest is the sole input to the five release gates: backend, frontend, Docker E2E, security scanning, and the backup/restore drill. The E2E and restore gates refuse to start without a digest-pinned candidate and have no source-build or local-tag fallback, and security scans the same digest instead of building its own audit image. Promotion adds tags to that verified digest and never rebuilds. The promoted release digest is therefore the artifact that passed the gates.

Publication fails closed unless the triggering tag is protected. Restrict creation of `refs/tags/v*` to the release owner and prevent updates and deletion of existing release tags. Protect the default branch with pull requests and the five routine CI checks. The `release-publish` environment permits only release tags and the protected default branch used by historical phase dispatch. The workflow limits registry write permission to the candidate and promotion jobs; signing secrets exist only in the candidate job, while promotion receives registry credentials but no signing key.

For this private, personal GitHub Pro repository, creating an owner-only protected release tag is the owner's approval to run the complete release pipeline. This is a single-owner release policy, not independent two-person approval. GitHub does not offer required environment reviewers for private Pro repositories. If independent approval becomes a requirement, use a supported environment approval service or plan before publishing; an ordinary environment without required reviewers does not provide that property.

The candidate is built for `linux/amd64` only. There is no multi-architecture release image; running pgBase on another architecture means building from source and validating it yourself.

The private release uses key-backed Cosign attestations for provenance and SPDX SBOMs. The encrypted private key and its password are `COSIGN_PRIVATE_KEY` and `COSIGN_PASSWORD` secrets in `release-publish`; only the public key, `.github/cosign.pub`, is committed. Neither private repository metadata nor signatures are submitted to public Fulcio/Rekor services. The GHCR package and its attached attestations must remain private.

Before any gate consumes the digest, the workflow retrieves and verifies both signed attestations from that exact registry digest and checks their source and predicate bindings. Consumers must obtain the public key through a trusted channel, rather than accepting a replacement key supplied alongside an untrusted image. The initial public-key file SHA-256 is `11d76bbe37cb662c446d9a9b2eff9cd3826bd5afe64dfdf61e4adff798052f7a`.

These signatures establish that the holder of the release key made the provenance claims. They are not GitHub-issued keyless identities and do not provide a public transparency timestamp. Protect the signing environment and workflow changes accordingly. Keep an encrypted key backup and its password outside source control with access restricted to the release owner; rotate the trusted key and document the affected releases if it is compromised.

Use Cosign v3.1.2 and the trusted public key to verify a release from the personal repository namespace:

```console
cosign verify-attestation --key .github/cosign.pub --insecure-ignore-tlog \
  --type slsaprovenance1 ghcr.io/yakisoba0728/pgbase@sha256:...
cosign verify-attestation --key .github/cosign.pub --insecure-ignore-tlog \
  --type spdxjson ghcr.io/yakisoba0728/pgbase@sha256:...
```

`--insecure-ignore-tlog` disables only the transparency-log requirement; public-key signature and image-subject verification remain required. Inspect the verified provenance's repository, source commit and version against the release you selected. The workflow's `verify-release-attestation.sh` helper additionally requires exact equality with the predicate produced by that build. Retain the decoded statements and digest with the deployment record. GitHub artifact-attestation APIs are not used by this private Pro release path.

### Historical rollout-phase images

Migration rollouts use the immutable source commits in
`deployments/release-phases.json`, not a current binary with a version constant
overridden. Dispatch the **Release phase image gate** workflow with the exact
`rollout` and `phase`. It resolves exactly one manifest entry, builds that
commit's real `cmd/pgbase` binary with Go 1.26.8, and copies only that binary and
the pinned CA bundle into the current hardened distroless runtime. The candidate
is built and pushed once as `linux/amd64`; its digest is then passed unchanged to
the backend, frontend, E2E, security, and backup/restore gates. This workflow
does not promote a tag.

In addition to the ordinary provenance and SPDX 2.3 SBOM, the phase workflow
publishes and verifies a custom predicate that binds the candidate to the
rollout, phase, source repository and commit, packaging-workflow commit,
canonical version, and raw binary SHA-256. Retain that verification evidence
with the deployment record. The manifest's `operator` and audit-only
`predecessor` entries are built by the backend process gate but cannot be
selected as deployable phases by this workflow.

### Release tag policy

| Release tag | Immutable exact tag | Moving channel tag | Untouched |
| --- | --- | --- | --- |
| `vMAJOR.MINOR.PATCH` | created when absent, no-op when it already points at the candidate digest | `latest` | `alpha`, `beta`, `rc` |
| `vMAJOR.MINOR.PATCH-alpha.N` | same | `alpha` | `latest`, `beta`, `rc` |
| `vMAJOR.MINOR.PATCH-beta.N` | same | `beta` | `latest`, `alpha`, `rc` |
| `vMAJOR.MINOR.PATCH-rc.N` | same | `rc` | `latest`, `alpha`, `beta` |
| anything else, including build metadata and unsupported channels | rejected in preflight | none | all |

Only a stable SemVer release moves `latest`. Each channel has its own promotion concurrency group, and the promotion reads the current channel image's `org.opencontainers.image.version` label before it mutates either tag. The candidate must be strictly newer within that same channel; an older version, an equal version at another digest, a malformed/cross-channel label, or an unreadable label fails before every registry mutation. Numeric SemVer components and prerelease ordinals are compared as canonical decimal strings rather than machine integers, so their length is not bounded by shell arithmetic.

The final runtime image carries the canonical release version in both the binary and its OCI version label. The exact version tag's registry state is read back before any channel is touched; if it already points at a different digest the whole promotion fails without mutating a channel, and the exact tag is never force-retagged. After promotion, both the exact tag and channel are read back for the candidate digest and version label. Promotion deletes no tags, and the before/after digest and version evidence is retained as a build artifact.

An older channel created before OCI version labels were added fails closed. For a one-time migration only, set the repository variable `PGBASE_RELEASE_CHANNEL_BOOTSTRAP_VERSION` to the channel's current canonical exact version (for example `v1.2.2`) before releasing a strictly newer version. Promotion proceeds only when that immutable exact tag exists and has exactly the channel's current digest. Remove the variable immediately after the successful promotion; never use it to guess a missing version or bypass a missing exact tag.

## Configuration

The production binary reads configuration from environment variables:

| Variable | Requirement and behavior |
| --- | --- |
| `PGBASE_DATABASE_URL` | Required `postgres://` or `postgresql://` URL. Every remote primary and fallback target must use encrypted certificate and hostname verification (`sslmode=verify-full`). Local loopback and Unix-socket targets may use plaintext. |
| `PGBASE_ALLOW_INSECURE_DATABASE` | Boolean; default `false`. Explicit development-only escape hatch for a non-loopback database without verified TLS. The bundled Compose profile sets it only for PostgreSQL on its private development network; never enable it for a production or otherwise untrusted network. |
| `PGBASE_TOKEN_SECRET` | Required base64 text decoding to at least 32 bytes. All instances must share it. |
| `PGBASE_SETUP_TOKEN` | Set to an unguessable valid UTF-8 value of at least 32 characters and no more than 1,024 bytes before first-administrator setup. The upper bound guarantees the configured credential fits the setup API's request budget. |
| `PGBASE_METRICS_TOKEN` | Optional bearer-token-safe secret of 32–256 bytes. When set, enables `GET /metrics`; when empty, the route does not exist. Use a distinct secret per monitoring trust boundary. |
| `PGBASE_HTTP_ADDRESS` | Listener address; default `127.0.0.1:8090`. A reverse-proxy deployment may bind a private or wildcard backend address explicitly. |
| `PGBASE_PUBLIC_DIR` | Optional absolute directory of a user static site. PgBase resolves it at startup and refuses a missing, non-directory, or unresolved path. |
| `PGBASE_PUBLIC_SPA_FALLBACK` | Boolean; default `false`. When enabled with `PGBASE_PUBLIC_DIR`, navigation-shaped missing paths serve `index.html`; missing asset-shaped paths remain 404. `/api`, `/admin`, health, metrics, and registered custom routes keep precedence. |
| `PGBASE_PUBLIC_URL` | Exact external `http` or `https` origin, with no path, query, fragment, credentials, or wildcard. Plain HTTP is accepted only for loopback hosts; use HTTPS for every non-loopback public URL. Set it in production. |
| `PGBASE_AUTO_MIGRATE` | Boolean; default `false`. Use `true` only for the bundled development Compose path. |
| `PGBASE_TRUSTED_PROXIES` | Comma-separated, masked CIDR prefixes for proxies allowed to supply forwarded scheme/address data. Empty by default. |
| `PGBASE_CORS_ALLOWED_ORIGINS` | Comma-separated exact `http`/`https` origins. Wildcards are rejected. Leave empty for same-origin administration. |
| `PGBASE_ADMIN_SESSION_TTL` | Go duration; positive and no greater than `12h`; default `12h`. |
| `PGBASE_STATEMENT_TIMEOUT` | Go duration; default `5s`. Applies to ordinary request and diagnostic SQL. System migrations and approved collection DDL disable this statement limit locally so large schema work can finish; they retain a 5-second lock-acquisition timeout and remain cancelable through the command/request context. |
| `PGBASE_SCHEMA_APPLY_TIMEOUT` | Go duration; default `10m`. Bounds the administrator schema-apply HTTP route without widening ordinary request deadlines. |
| `PGBASE_SHUTDOWN_TIMEOUT` | Go duration; default `15s`. |
| `PGBASE_STOP_GRACE_PERIOD` | Compose stop grace Go duration; default `20s`. When set, it must be at least `PGBASE_SHUTDOWN_TIMEOUT + 5s`; pgBase refuses startup when the margin is smaller. |

Never put secret values in command arguments, source control, images, support bundles, or logs. Supply them through a secret manager or protected environment injection. The JSON logger redacts known credentials, but redaction is not a substitute for avoiding secret-bearing log messages.

### Bundled development Compose resource ceilings

The bundled development profile applies ordinary Compose service-level `cpus`, `mem_limit`, and `pids_limit` bounds rather than the optional Swarm `deploy` section. It defaults both pgBase and PostgreSQL to 2 CPUs, 1 GiB of memory, and 256 PIDs. Override them with `PGBASE_APP_CPUS`, `PGBASE_APP_MEMORY_LIMIT`, `PGBASE_APP_PIDS_LIMIT`, `PGBASE_POSTGRES_CPUS`, `PGBASE_POSTGRES_MEMORY_LIMIT`, and `PGBASE_POSTGRES_PIDS_LIMIT` after measuring the local workload. These defaults are containment ceilings for the development stack, not production sizing guidance; set and validate production limits in the target orchestrator.

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

The bundled cookie-authenticated administrator UI must share the API origin. Administrator reads allow non-browser clients that omit `Origin`, but a browser that sends `Origin` must send the exact public origin; mutations additionally require `X-PgBase-CSRF`. `PGBASE_CORS_ALLOWED_ORIGINS` controls non-credentialed browser response visibility and preflight only. It never grants cookie-authenticated read, setup, login, or mutation authority to another origin, and global CORS responses do not opt into browser credentials. A future cross-origin administrator client requires credentials designed separately from the `SameSite=Lax` cookie. Wildcards are rejected. With an HTTPS public URL, administrator cookies are issued with `Secure`.

## External PostgreSQL

- Use a dedicated database and role for one pgBase project.
- Grant the role permission to create and modify objects in `pgbase_meta`, `app`, and `pgbase_relation`; migrations and collection/relation DDL require it.
- Require encrypted, verified transport for non-local PostgreSQL.
- Pooled sessions pin `search_path` to `pg_catalog`; application relations are schema-qualified so caller- or role-controlled schemas cannot capture unqualified object resolution.
- Configure at least four connections per pgBase pool (`pool_max_conns=4` or higher): one listener, one schema/apply path, and background/request headroom. Size the database limit for all instances plus migration, backup, and operator connections.
- Back up all three pgBase schemas together. See [Backup and restore](backup-restore.md).

## Health, rollout, and shutdown

`GET /health/live` returns HTTP 200 while the process HTTP path is alive; a PostgreSQL outage alone does not make liveness fail. `GET /health/ready` returns HTTP 200 only while HTTP is accepting, PostgreSQL is reachable, system migrations are current, and the schema listener is synchronized. A failed readiness response is HTTP 503 with safe component names, not raw dependency errors. Concurrent readiness probes share one dependency evaluation and reuse it for at most 250 ms; the local shutdown gate still overrides a cached success immediately. Public administrator setup-status checks use the same bounded reuse window and successful setup invalidates it immediately.

Use liveness only to detect a stuck process. Use readiness for load-balancer admission and rollout gates. The container health check invokes `/pgbase healthcheck --url http://127.0.0.1:8090/health/ready` and requires no shell or curl.

On SIGTERM, pgBase marks readiness false, stops accepting new requests, cancels background work, and drains HTTP requests for up to `PGBASE_SHUTDOWN_TIMEOUT`. The bundled Compose profile applies `PGBASE_STOP_GRACE_PERIOD` to both the container `stop_grace_period` and the application, so one variable moves both, and requires at least a five-second margin over the application timeout. Keep the equivalent margin in other orchestrators. Requests exceeding the grace period can be interrupted.

A correct shutdown ends with exit code 0. Exit code 137 means the orchestrator's grace period expired and the container was killed mid-drain; treat it as a configuration fault rather than a normal stop. Verify a rendered profile before deploying it:

```console
docker compose --env-file deployments/.env -f deployments/docker-compose.yml config
```

and confirm that the rendered `stop_grace_period` is at least `PGBASE_SHUTDOWN_TIMEOUT + 5s`. pgBase refuses to start when the margin is smaller, so a profile that renders is not automatically a profile that runs.

## Secret rotation

- Rotating `PGBASE_TOKEN_SECRET` invalidates existing administrator sessions, CSRF tokens, signed cursors, and the current rate-limit hash namespace. Update all instances together and expect administrators to sign in again.
- Rotating `PGBASE_SETUP_TOKEN` changes only first-administrator bootstrap credentials. After an administrator exists, setup stays unavailable regardless of the token.
- Rotating `PGBASE_METRICS_TOKEN` requires a coordinated application rollout and Prometheus credentials-file reload. One pgBase instance accepts one metrics token with no old/new overlap, so keep the scrape-down alert under maintenance or use separate old/new scrape jobs for the two target batches during a zero-blind-spot rolling rotation; remove the old job only after every new-token target reports `up == 1`.
- `pgbase admin reset-password --email EMAIL --password-stdin` replaces an administrator password, revokes all sessions for that administrator, and clears that account's login lockout. Use it as the host-authorized recovery path after repeated failed logins.
- `pgbase admin upsert --email EMAIL --password-stdin` creates an absent administrator or replaces an existing administrator password and revokes its sessions. `admin update` is an alias for this password-changing operation; it has no other update fields. `admin delete --email EMAIL` refuses to delete the final active administrator.
- `pgbase admin otp enable --email EMAIL --secret-stdin` stores a base32 TOTP secret encrypted under `PGBASE_TOKEN_SECRET` and invalidates that administrator's sessions. A protected administrator must submit `otpCode` with the normal admin login request; the embedded UI prompts after an `otp_required` response. `admin otp disable --email EMAIL` removes the factor and invalidates sessions.
- `pgbase admin ip allow|deny --email EMAIL --cidr CIDR` manages canonical CIDR entries; `admin ip list --email EMAIL` prints them. Once entries exist, login and every administrator session request must originate within one allowed prefix. Proxy forwarding is honored only for `PGBASE_TRUSTED_PROXIES`.
- Rotate a PostgreSQL password in a coordinated database/application change so old connections can drain and new instances receive the new `PGBASE_DATABASE_URL` without exposing it.

## Zero-downtime limits

Multi-instance serving is supported against one shared database, but zero-downtime upgrades are not guaranteed. The Production Kernel has no online-DDL mechanism, down-migration command, read-replica routing, or compatibility promise between arbitrary v0.x binaries and schemas. Collection and relation DDL can acquire PostgreSQL locks. A system migration may require a maintenance window; follow release-specific guidance and prefer a tested stop-migrate-start sequence whenever compatibility is uncertain.

## Public user registration and collection access

`PGBASE_USER_REGISTRATION` defaults to `false`. Set it to `true` to expose self-registration in builds containing the post-v0.1.0 public-user capability; login and existing Bearer sessions are independent of the registration switch. The bundled Compose file forwards this setting from its environment file. Sessions last 24 hours and can be revoked with logout. Public and administrator sessions use separate tables and token domains.

Public record access is separately default-denied per collection. An administrator enables owner-only access through `/api/admin/collections/{collection}/user-access`. Incoming or outgoing relations make the public record capability unavailable until relation-aware authorization is implemented. See [user access](user-access.md) for exact routes, limits, and examples, and [upgrades](upgrades.md#public-identity-and-owner-access-system-migrations-1011) before using an existing database.
