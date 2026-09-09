# Backup and restore

A valid pgBase backup contains the project database's `pgbase_meta`, `app`, and `pgbase_relation` schemas from one consistent point in time. `pgbase_meta` contains administrators, sessions, catalog/relation definitions, migration history, record revisions, audit events, outbox rows, and idempotency results. `app` contains physical collection tables and inline single-relation UUID columns/FKs/indexes. `pgbase_relation` contains ordered multiple-relation edge tables plus their integrity functions and triggers. Omitting any one schema does not produce a recoverable project.

Environment configuration and secrets are outside PostgreSQL. Store the deployment configuration, `PGBASE_TOKEN_SECRET`, setup policy, TLS configuration, and database credentials separately in a protected secret/configuration system. Never put them in the database dump or backup logs.

Administrator backup archive uploads and downloads are bounded to 2 GiB and a 30-minute transfer window. PgBase overrides the normal HTTP connection read and write deadlines only for those two streaming routes. Configure any reverse proxy or load balancer in front of PgBase with compatible request-body, response, and idle timeouts; an upstream timeout can still end the transfer earlier.

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
  --schema=pgbase_relation \
  --file=pgbase.dump
pg_restore --list pgbase.dump > pgbase.dump.list
```

The custom format is compressed and supports selective inspection, but restore all three schemas together. Schedule dumps when collection or relation DDL is not being applied. `pg_dump` provides a transactionally consistent database snapshot; PostgreSQL point-in-time recovery is preferable when recovery-point objectives include writes after the last logical dump.

Encrypt backups at rest, restrict access, copy them off the database host, retain multiple generations, and test restoration regularly. A backup that has not been restored is unverified.

## Point-in-time recovery

For PostgreSQL PITR, configure regular physical base backups plus continuous WAL archiving using your PostgreSQL platform's supported procedure. Monitor WAL archival, retention, encryption, and restore tests. Recover the entire pgBase database to one target time so `pgbase_meta` migration/catalog and record-artifact state, `app` records/inline relations, and `pgbase_relation` ordered edges remain consistent; do not splice schemas from different recovery points.

## Restore drill

Restore into a new, empty database first. Do not restore over a running project.

The repository runs this same procedure as an executable drill. It seeds a project through the administrator and record APIs, dumps it, restores it into an empty database, and then checks the restored project against the list below on PostgreSQL 16, 17 and 18:

```console
go test -tags=restore ./tests/restore
```

The drill starts its own PostgreSQL containers and builds pgBase from the working tree. A release runs the same command against the published candidate, which makes it refuse any image that is not digest-pinned and removes the source-build path entirely:

```console
PGBASE_RESTORE_CANDIDATE_IMAGE=ghcr.io/webisnoteasy/pgbase@sha256:... \
PGBASE_RESTORE_EXPECTED_VERSION=vX.Y.Z \
  go test -tags=restore ./tests/restore
```

Use the drill as the reference for what a restore must prove; the manual steps below are the same procedure against a real backup target.

1. Stop all pgBase instances that could connect to the restore target.
2. Create an empty target database owned by the pgBase database role.
3. Require the `pg_restore` client major to match the target server major. This prevents a newer client from replaying settings that an older server does not recognize:

   ```sh
   set -eu
   export PGSERVICEFILE="${XDG_CONFIG_HOME:-$HOME/.config}/pgbase/pg_service.conf"
   export PGPASSFILE="${XDG_CONFIG_HOME:-$HOME/.config}/pgbase/pgpass"
   client_major="$(pg_restore --version | awk '{print $3}' | cut -d. -f1)"
   server_version_num="$(PGSERVICE=pgbase-restore psql --no-psqlrc --tuples-only --no-align --command 'show server_version_num')"
   case "$client_major:$server_version_num" in
     *[!0-9:]*|:*) echo "could not determine PostgreSQL client/server major versions" >&2; exit 1 ;;
   esac
   server_major="$((server_version_num / 10000))"
   if [ "$client_major" -ne "$server_major" ]; then
     echo "pg_restore client major $client_major must match server major $server_major" >&2
     exit 1
   fi
   ```

   Use the server distribution's client tools or a PostgreSQL container pinned to the server major if the check fails.
4. Restore the custom dump:

   ```sh
   PGSERVICE=pgbase-restore pg_restore \
     --dbname='service=pgbase-restore' \
     --single-transaction \
     --no-owner \
     --no-acl \
     --exit-on-error \
     pgbase.dump
   ```

5. Configure the restored instance with the separately protected token secret and the restored database URL. If the original `PGBASE_TOKEN_SECRET` is not restored, existing sessions and signed cursors will be invalid; stored records and administrator password hashes remain in the database.
6. Have the secret manager inject the restored `PGBASE_DATABASE_URL` and `PGBASE_TOKEN_SECRET` into the operator environment without rendering either value in the command line. Using the same pgBase release that produced the backup, verify migration state and database invariants:

   ```sh
   pgbase migrate status
   pgbase doctor --json
   ```

   Require the doctor JSON result to contain `"ok":true`. Relation-aware doctor checks metadata bindings and all pgBase-owned relation columns, foreign keys, edge tables, constraints, indexes, functions, and triggers; treat any missing, mismatched, or orphaned relation object as a failed restore.

7. Start one pgBase instance and require `/health/ready` to return HTTP 200. Through the administrator-authenticated HTTP record API, verify representative scalar records, raw single/multiple relation UUIDs and their order, `?expand=` results, and matching ETags. Replaying an original `Idempotency-Key` with the same body must return the original record, ETag, and `Location` rather than creating a duplicate; a restore that lost the idempotency results would create one. Where the drill permits a disposable mutation, use `Idempotency-Key`/`If-Match` to exercise a relation PATCH and a configured delete action, then confirm the expected record state. Do not use direct SQL as the validation path because it bypasses the Record Operations contract being tested.

8. Confirm the restored instance closes any schema operation the lost source instance left in progress. `pgbase_meta.schema_operations` rows in `running` or `unknown` status are reconciled to `committed` or `failed` shortly after startup; a row that stays unresolved means schema-operation maintenance is not running against the restored database.

For disaster recovery, promote a fully validated PITR target or restored database, update the secret-managed `PGBASE_DATABASE_URL`, and roll out all instances. Keep the damaged source isolated until the recovered project has been accepted.

When the backup and restore work is complete, unset `PGSERVICEFILE`, `PGPASSFILE`, and `pgbase_secret_dir`. Retain or securely remove the protected libpq files according to the operator's credential-rotation policy.

## Local file objects

The raw PostgreSQL dumps described above include file metadata but not object bytes. The managed administrator backup console additionally snapshots referenced local/S3 object bytes; see [backup repositories and recovery](backup-s3.md). For manual PostgreSQL backups, Drain all PgBase writers and cleanup workers, then back up the database and the entire local file directory as one recovery set. Restore both into new locations with the same token secret before starting the application. The restore drill now copies the file directory independently and downloads a file through the restored authenticated HTTP API, checking its exact bytes and MIME type. S3-compatible production recovery must retain object versions for the intended PostgreSQL recovery point; this local filesystem drill does not qualify that deployment.

The disposable WAL marker drill is `scripts/test-local-pitr.sh`; details and measured limits are in [local verification](local-verification.md).
