# Security policy

## Reporting a vulnerability

Report suspected vulnerabilities through [GitHub private vulnerability reporting for this repository](https://github.com/yakisoba0728/PgBase/security/advisories/new). Do not open a public issue, discussion, or pull request before coordinated disclosure.

Include the affected commit or version, deployment assumptions, impact, reproduction steps, and a minimal proof of concept. Remove credentials, tokens, personal data, and production database contents. If private vulnerability reporting is unavailable to you, wait for a repository maintainer to publish an alternate private channel rather than posting exploit details publicly; this policy does not invent a contact address.

We will use the private advisory to clarify the report and coordinate remediation/disclosure. This pre-v0.1 project does not promise a response or remediation SLA.

## Supported versions

pgBase has no stable release yet. Security fixes are supported only on the latest published repository state or explicitly identified latest pre-release; older commits and pre-releases are unsupported. Upgrade guidance may require a database backup and explicit migration.

| Version | Supported |
| --- | --- |
| Latest repository state / latest identified pre-release | Yes |
| Older commits, images, or pre-releases | No |

## Deployment responsibility

Operators must protect PostgreSQL and pgBase secrets, terminate TLS at a trusted proxy, configure exact origins and trusted proxy CIDRs, keep production auto-migration disabled, back up all three pgBase schemas (`pgbase_meta`, `app`, and `pgbase_relation`) together, and run current dependencies/images. See [Deployment](docs/deployment.md) and [Backup and restore](docs/backup-restore.md).

The Production Kernel does not include public user authentication or record rules, cross-project relations, arbitrary SQL joins, relation edge attributes, files, realtime, an SDK, generated OpenAPI, online DDL, replicas, or sharding. Do not infer security guarantees for those excluded capabilities.
