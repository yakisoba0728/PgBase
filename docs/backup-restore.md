# Backup and restore

A valid pgBase backup contains the project database's `pgbase_meta` and `app` schemas from one consistent point in time. `pgbase_meta` contains administrators, sessions, schema definitions, and migration history; `app` contains the physical collection tables. Backing up only one schema does not produce a recoverable project.

Environment configuration and secrets are outside PostgreSQL. Store the deployment configuration, `PGBASE_TOKEN_SECRET`, setup policy, TLS configuration, and database credentials separately in a protected secret/configuration system. Never put them in the database dump or backup logs.

## Logical backup with pg_dump

Use client tools compatible with the server and a PostgreSQL URL that does not appear in shell history or process listings. For example, inject `PGBASE_DATABASE_URL` from a secret manager and run:

```sh
umask 077
pg_dump \
  --dbname="$PGBASE_DATABASE_URL" \
  --format=custom \
  --no-owner \
  --no-acl \
  --schema=pgbase_meta \
  --schema=app \
  --file=pgbase.dump
pg_restore --list pgbase.dump > pgbase.dump.list
```

The custom format is compressed and supports selective inspection, but restore both schemas together. Schedule dumps when collection DDL is not being applied. `pg_dump` provides a transactionally consistent database snapshot; PostgreSQL point-in-time recovery is preferable when recovery-point objectives include writes after the last logical dump.

Encrypt backups at rest, restrict access, copy them off the database host, retain multiple generations, and test restoration regularly. A backup that has not been restored is unverified.

## Point-in-time recovery

For PostgreSQL PITR, configure regular physical base backups plus continuous WAL archiving using your PostgreSQL platform's supported procedure. Monitor WAL archival, retention, encryption, and restore tests. Recover the entire pgBase database to one target time so `pgbase_meta` migration/catalog state and `app` table state remain consistent; do not splice schemas from different recovery points.

## Restore drill

Restore into a new, empty database first. Do not restore over a running project.

1. Stop all pgBase instances that could connect to the restore target.
2. Create an empty target database owned by the pgBase database role.
3. Restore the custom dump:

   ```sh
   pg_restore \
     --dbname="$PGBASE_RESTORE_DATABASE_URL" \
     --no-owner \
     --no-acl \
     --exit-on-error \
     pgbase.dump
   ```

4. Configure the restored instance with the separately protected token secret and the restored database URL. If the original `PGBASE_TOKEN_SECRET` is not restored, existing sessions and signed cursors will be invalid; stored records and administrator password hashes remain in the database.
5. Using the same pgBase release that produced the backup, verify migration state and database invariants:

   ```sh
   PGBASE_DATABASE_URL="$PGBASE_RESTORE_DATABASE_URL" pgbase migrate status
   PGBASE_DATABASE_URL="$PGBASE_RESTORE_DATABASE_URL" pgbase doctor --json
   ```

   `PGBASE_TOKEN_SECRET` must also be present in the environment because every command loads the production configuration. Require the doctor JSON result to contain `"ok":true`.

6. Start one pgBase instance, require `/health/ready` to return HTTP 200, and verify representative collection records before allowing traffic.

For disaster recovery, promote a fully validated PITR target or restored database, update the secret-managed `PGBASE_DATABASE_URL`, and roll out all instances. Keep the damaged source isolated until the recovered project has been accepted.
