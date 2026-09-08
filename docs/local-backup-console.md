# Local backup and restore

PgBase can create a consistent local backup of PostgreSQL and local file storage. Stop every PgBase server and any external SQL writer connected to the database before either operation. Older PgBase binaries and external SQL clients do not participate in this advisory-lock protocol and must remain stopped for the entire operation. The command takes an exclusive PostgreSQL advisory lock and refuses to run while a participating server is active.

The database URL and file settings come from the normal `PGBASE_*` environment. Backups support local file storage; S3-backed files need the storage provider's backup tooling.

```sh
pgbase backup create --directory /srv/backups/pgbase-2026-09-09
pgbase backup verify --directory /srv/backups/pgbase-2026-09-09
```

The destination must not exist. PgBase writes into a private temporary sibling and renames it only after the database dump, copied files, manifest, and SHA-256 inventory are complete. `manifest.json` records the PostgreSQL major version, creation time, database artifact, and every local file. Verification rejects unknown manifest fields, unsafe paths, symlinks, missing files, added files, and hash or size changes.

Restore requires the same major version for the PostgreSQL server, `pg_restore`, and the backup. The target database must be fresh and empty, with no other client sessions. A recovery set containing files requires a configured local file target. When local file storage is configured, `PGBASE_FILES_LOCAL_DIRECTORY` must point to a path that does not exist.

```sh
pgbase backup restore --directory /srv/backups/pgbase-2026-09-09
```

The database restore uses one transaction. Files are copied into a fresh staging directory and installed before the transactional `pg_restore` begins, so a successful database commit already has its files available. A failed restore removes only the new file target it installed. Restore never moves or removes files from the backup directory, so the same verified backup remains available for another recovery attempt.

Install `pg_dump` and `pg_restore` from the PostgreSQL major version used by the server. Connection settings are passed through libpq environment variables, and the password is written only to a temporary mode-0600 passfile; the database URL and password are not placed in child-process arguments.

Keep `PGBASE_TOKEN_SECRET` and the remaining protected project configuration separately: a database-and-files recovery set does not include process environment settings. Reuse the project token secret to decrypt persisted OAuth and SMTP secrets after recovery. Run `pgbase doctor --json` against the restored target before starting it.
