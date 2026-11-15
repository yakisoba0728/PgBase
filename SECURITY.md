# Security policy

## Reporting a vulnerability

Report suspected vulnerabilities through [GitHub private vulnerability reporting for this repository](https://github.com/yakisoba0728/PgBase/security/advisories/new). Do not open a public issue, discussion, or pull request before coordinated disclosure.

Include the affected commit or version, deployment assumptions, impact, reproduction steps, and a minimal proof of concept. Remove credentials, tokens, personal data, and production database contents. If private vulnerability reporting is unavailable to you, wait for a repository maintainer to publish an alternate private channel rather than posting exploit details publicly; this policy does not invent a contact address.

We will use the private advisory to clarify the report and coordinate remediation/disclosure. This early v0.x project does not promise a response or remediation SLA.

## Supported versions

PgBase has no stable release yet. Security fixes are supported only on the latest published repository state or explicitly identified latest pre-release; older commits and pre-releases are unsupported. Upgrade guidance may require a database backup and explicit migration.

| Version | Supported |
| --- | --- |
| Latest repository state / latest identified pre-release | Yes |
| Older commits, images, or pre-releases | No |

## Deployment responsibility

Operators must protect PostgreSQL and PgBase secrets, terminate TLS at a trusted proxy, configure exact origins and trusted proxy CIDRs, keep production auto-migration disabled, back up all three PgBase schemas (`pgbase_meta`, `app`, and `pgbase_relation`) together, and run current dependencies/images. See [Deployment](docs/deployment.md) and [Backup and restore](docs/backup-restore.md).

## Scope and trust boundaries

The current source includes administrator and public-user authentication, action/record rules, relations, local/S3 files, image processing, realtime replay, background jobs, TypeScript/Dart SDKs, trusted extensions and an embedded administrator UI. Their authorization, isolation, cancellation and recovery paths are all in security-review scope. Historical descriptions of a smaller “Production Kernel” do not exclude implemented capabilities from review.

One project owns one PostgreSQL database. Additional instances of that project must share compatible configuration, secrets and storage. Separate projects are not tenants isolated within a shared PgBase database. Cross-project relations, online DDL, replicas and sharding are not supported; the presence of several user realms does not change that infrastructure boundary.

Administrator-authored mail templates and installed Go/JavaScript extensions are trusted operator code/configuration. Dynamic values entering those templates still require contextual escaping. Optional SQL console, SMTP, outbound HTTP and backup/restore capabilities must remain guarded at their configured admission boundaries. An administrator-only input validation defect still merits correction; it must not be described as an unauthenticated exploit without evidence of that path.

Record mutations must preserve their transaction, revision/ETag, audit and outbox contracts. Realtime record replay rechecks current policy; application broadcasts have a separate operator-controlled, bounded best-effort delivery contract. Cached SDK identity is untrusted until the server validates the token. Logs and error responses must not disclose credentials, tokens or private payloads.

## Evidence standards

A successful security workflow means its declared commands completed. CodeQL warnings require source-to-sink and trust-boundary classification, and no-finding reports do not establish absence of vulnerabilities. Preserve all SARIF results, exact source/run identifiers, artifact digests, reproduction results and remaining limitations. Track confirmed defects, defensive changes and false positives separately; do not suppress results to produce a green summary.
