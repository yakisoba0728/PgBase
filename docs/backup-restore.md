# Backup and restore

A valid pgBase backup contains the project database's `pgbase_meta` and `app` schemas from one consistent point in time. `pgbase_meta` contains administrators, sessions, schema definitions, and migration history; `app` contains the physical collection tables. Backing up only one schema does not produce a recoverable project.

Environment configuration and secrets are outside PostgreSQL. Store the deployment configuration, `PGBASE_TOKEN_SECRET`, setup policy, TLS configuration, and database credentials separately in a protected secret/configuration system. Never put them in the database dump or backup logs.

## Logical backup with pg_dump

Use client tools compatible with the server. Keep connection parameters in a protected libpq service file and passwords in a separate mode-`600` passfile so neither the URL nor the password appears in shell history or process arguments. Create the files outside the repository and open them with an editor:

```sh
umask 077
pgbase_secret_dir="${XDG_CONFIG_HOME:-$HOME/.config}/pgbase"
install -d -m 700 "$pgbase_secret_dir"
touch "$pgbase_secret_dir/pg_service.conf" "$pgbase_secret_dir/pgpass"
chmod 600 "$pgbase_secret_dir/pg_service.conf" "$pgbase_secret_dir/pgpass"
${EDITOR:-vi} "$pgbase_secret_dir/pg_service.conf"
${EDITOR:-vi} "$pgbase_secret_dir/pgpass"
```

Define backup and restore targets in `pg_service.conf`, using TLS verification for remote PostgreSQL:

```ini
[pgbase-backup]
host=database.example
port=5432
dbname=pgbase
user=pgbase
sslmode=verify-full

[pgbase-restore]
host=restore-database.example
port=5432
dbname=pgbase_restore
user=pgbase
sslmode=verify-full
```

Enter the matching credentials in `pgpass`; replace each password only inside the editor or through your secret provisioner:

```text
database.example:5432:pgbase:pgbase:replace-with-backup-password
restore-database.example:5432:pgbase_restore:pgbase:replace-with-restore-password
```

Escape literal `:` and `\` characters in passfile fields with `\`. Then select the protected files through libpq environment variables and run the dump without a connection argument:

```sh
export PGSERVICEFILE="$pgbase_secret_dir/pg_service.conf"
export PGPASSFILE="$pgbase_secret_dir/pgpass"
PGSERVICE=pgbase-backup pg_dump \
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
   export PGSERVICEFILE="${XDG_CONFIG_HOME:-$HOME/.config}/pgbase/pg_service.conf"
   export PGPASSFILE="${XDG_CONFIG_HOME:-$HOME/.config}/pgbase/pgpass"
   PGSERVICE=pgbase-restore pg_restore \
     --dbname='service=pgbase-restore' \
     --single-transaction \
     --no-owner \
     --no-acl \
     --exit-on-error \
     pgbase.dump
   ```

4. Configure the restored instance with the separately protected token secret and the restored database URL. If the original `PGBASE_TOKEN_SECRET` is not restored, existing sessions and signed cursors will be invalid; stored records and administrator password hashes remain in the database.
5. Have the secret manager inject the restored `PGBASE_DATABASE_URL` and `PGBASE_TOKEN_SECRET` into the operator environment without rendering either value in the command line. Using the same pgBase release that produced the backup, verify migration state and database invariants:

   ```sh
   pgbase migrate status
   pgbase doctor --json
   ```

   Require the doctor JSON result to contain `"ok":true`.

6. Start one pgBase instance, require `/health/ready` to return HTTP 200, and verify representative collection records before allowing traffic.

For disaster recovery, promote a fully validated PITR target or restored database, update the secret-managed `PGBASE_DATABASE_URL`, and roll out all instances. Keep the damaged source isolated until the recovered project has been accepted.

When the backup and restore work is complete, unset `PGSERVICEFILE`, `PGPASSFILE`, and `pgbase_secret_dir`. Retain or securely remove the protected libpq files according to the operator's credential-rotation policy.
