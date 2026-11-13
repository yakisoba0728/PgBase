# Runtime verification — 2026-11-14 KST

This follow-up extends [the backend closure validation](backend-closure-validation.md). Historical results and failures remain preserved in that report. Detailed local logs and candidate archives are excluded from Git.

## OAuth

Controlled HTTP tests cover all 29 built-in profile normalizers, with separate Apple signed-token and database callback tests. They reproduced and fixed JSON response negotiation for GitHub, Notion token-body/version-header handling, and the Microsoft OIDC subject mapping. OAuth race tests, API callback checks and the full userauth integration suite pass. Live public discovery and JWKS requests succeeded for five providers.

The user confirmed no external OAuth test application or account is available. An authorized external login was therefore not performed; controlled-provider results must not be described as such. [Provider verification and required inputs](oauth-provider-verification.md) records the scope and primary sources.

## Abrupt termination

- A real child process is killed during a local object replacement and after publication. Reopening the store returns the complete old or new bytes respectively, with no partial public object. The race-enabled test passes three runs.
- A stock-image drill kills the application and PostgreSQL with SIGKILL, verifies exit code 137, and restarts the same persistent database volume. An acknowledged record and its durable idempotency receipt survive; a deliberately uncommitted update is rolled back. The existing administrator session also remains usable.
- Existing updater tests cover recovery at staged publication boundaries. Native Windows checks now also execute backup, filestore and durablefile tests, including the cross-platform replacement and real child-process termination checks.

These are process-crash and restart tests. They do not physically interrupt power to storage hardware, disable a disk's cache, or establish guarantees for every filesystem or device. An interrupted unpublished local upload can leave an inaccessible temporary file; startup does not remove other writers' temporary files.

## CI cleanup regressions

The first PR PG17 run exposed a staging cleanup race. The full manual PG17 run also found a retained shared storage admission lock after a 65-file rollback. Both assertions remained unchanged. Closing a pgx connection sends Terminate without waiting for PostgreSQL to remove its session locks.

A transport fixture suppresses Terminate and keeps the server session alive, deterministically reproducing both premature-release failures. Dedicated filestore coordination sessions now wait for `pg_advisory_unlock_all()` before closing, including storage admission, owned staging and migration sessions. Borrowed staging sessions unlock only their object lock, preserving their enclosing lease. The two protocol regressions and existing staging controls pass 20 race-enabled repetitions (20.259s); the 65-file commit/rollback cases pass 10 race-enabled repetitions (34.808s). Original failing CI and local logs remain preserved.

## Managed backup image

The default image includes matching PostgreSQL 16.16, 17.12 and 18.7 client tools. The shipped Compose database is pinned to 16.16. These minor releases include [upstream security fixes](https://www.postgresql.org/support/security/); older 16/17/18 server fixtures remain in the compatibility matrix. Native installations keep their existing PATH behavior; bundled tool selection is restricted to `/opt/pgbase/postgresql/<major>/bin`. The exact-major validation remains in place.

The runtime keeps a nonroot user, read-only root support and no shell/server/package manager. It includes only the clients and required shared libraries, source licenses and package/source SBOM records. Bundled libpq 18.7 omits optional GSS/Kerberos and LDAP integration, retaining password/SCRAM and TLS. OpenSSL 3.0.22 and zlib 1.4 replace vulnerable transitive libraries. Native installations continue using their existing client tools. A writable private backup/file volume and `/tmp` remain required; those are storage requirements rather than a missing-tool restriction.

The first packaging attempt passed functional checks but failed the HIGH/CRITICAL image gate. That candidate was rejected. The rebuilt runtime removes the unused Kerberos/LDAP/GnuTLS dependency graph and updates the affected client and TLS/compression libraries. No vulnerability exceptions or blanket `ignore-unfixed` setting were added. Actual TLS testing also found and corrected a distroless NSS identity-lookup configuration issue.

Final ARM64 candidate **rc.8**, schema **67**, digest:

```text
206527269896dbd44555f481b756b90deae125c74dcb2ad9dce88343fc99b26f
```

| Check | Result |
| --- | --- |
| Go unit/race, lint, vet | PASS; lint 0 issues. |
| Same-digest E2E, twice shuffled | PASS, 66.344s; includes actual application/database SIGKILL recovery. |
| Same-digest restore and managed-runtime suite | PASS, 32.425s; PostgreSQL 16/17/18 online backup, isolated restore, restored file bytes and post-snapshot record rollback. |
| Bundled TLS clients | Trusted `verify-full` dump and restore succeed; wrong CA and hostname fail. |
| Image vulnerability gate | 0 HIGH/CRITICAL, no suppressions; SPDX output generated. |
| Release-phase packaging | The isolated raw-binary packaging context includes both library scripts; its built image passes the managed backup and TLS tests too. |

The initial local phase-image fixture copied its binary without executable permission and failed startup. Matching the workflow's existing `install -m 0755` fixed that fixture; the original log is preserved. Runtime build inputs remained unchanged during final verification.

Windows and AMD64 execution results are recorded by the native Windows and stock-image backup workflow runs for this change. These platform checks supplement the exercised ARM64 artifact; they do not establish power-loss durability or actual external OAuth consent/login.
