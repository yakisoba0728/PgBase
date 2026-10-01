# Historical upgrade and recovery acceptance

The user confirmed on 2026-09-22 that no previous installation copy, backup, or external provider configuration is available on this Windows host. This acceptance therefore uses populated, disposable fixtures built from a pinned historical source revision. It does not claim to have upgraded the previous Mac installation or restored a user's real backup.

## Tested recovery path

`tests/restore/upgrade_test.go` builds historical commit `2939f1543e4889c0ffa967eae398c91d0ecbb6ce` and the current source as separate native binaries. The old binary creates the fixture through its real HTTP API: administrator and user authentication, collections and relations, ownership policies, files, records, and mutation receipts. A matching PostgreSQL custom-format dump and a separate file copy form the recovery set.

The candidate migrates the restored copy, runs `doctor`, then starts as a real process. The shared restore acceptance checks readiness, authentication, retained sessions, record and relation reads, file bytes, ETags, idempotent replay, owner policy enforcement, and reconciliation of an interrupted schema operation. The version advances from schema 40 to 65; the ledger contains 40 and 60 entries respectively because migration versions are not contiguous.

After the candidate process exits, the drill recreates only its own disposable target database and restores the pre-upgrade dump with a fresh copy of the old files. The historical binary then runs `doctor` and the same service acceptance against that recovery set. It does not attempt to run an old binary against the upgraded schema.

## Windows harness correction

The existing native restore drill failed on Windows before reaching its PostgreSQL 16/17/18 cases because its built candidate lacked the `.exe` suffix. The harness now names native Windows executables correctly and waits for the exact process it started when cleaning up. Unix cleanup uses SIGTERM; Windows cleanup uses process termination because Go's portable `os.Interrupt` delivery is unsupported there. Consequently this Windows drill also exercises recovery after process termination; it does not certify graceful Windows shutdown.

The historical archive extractor accepts regular files, directories, and Git's global PAX metadata header, rejects nonlocal paths and unexpected entry types, and writes only inside the test's temporary directory. All containers, databases, files, and processes used by these tests belong to the drill.

## Evidence

On the native Windows host:

```text
go test -tags=restore ./tests/restore -run '^TestRestoreDrill$' -count=1 -v
PASS: PostgreSQL 16, 17, and 18 (42.265s)

go test -tags='restore upgrade' ./tests/restore -run '^TestHistoricalUpgradeAndRollback$' -count=1 -v
PASS: populated upgrade and restored old-version rollback (37.391s)
```

The historical test first passed before the separate `upgrade` build tag was added; the tag isolates its full Git-history requirement from the ordinary release-candidate restore drill. Run it from a source checkout containing the pinned historical commit. Missing historical source is an error, not a simulated pass. The regular `restore` test continues to support digest-pinned release artifacts separately.

After the final production changes, the combined native run with `-tags='restore upgrade'` and `-run '^(TestRestoreDrill|TestHistoricalUpgradeAndRollback)$'` passed in 84.050s. The PostgreSQL 16/17/18 restore matrix took 50.27s, and the populated historical upgrade/rollback took 33.02s. Its log confirms schema version 40 → 65 with 40 → 60 applied migration entries.

External SMTP delivery, external OAuth account interaction, cloud S3 behavior, an actual installation-copy upgrade, and production-scale recovery timing remain unverified. Local SMTP/OAuth fixtures and MinIO tests establish local protocol behavior only. No external accounts or production data were created for this acceptance.
