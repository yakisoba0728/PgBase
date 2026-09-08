#!/usr/bin/env bash
# Disposable PostgreSQL WAL recovery drill; never connects to a project database.
set -euo pipefail
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
result_dir="$root_dir/.local/verification"
mkdir -p "$result_dir"
container_name="pgbase-pitr-$(openssl rand -hex 6)"
volume_name="$container_name-data"
image='postgres:17.9-bookworm@sha256:47f917f7409eacd22fc5dfb1dee634e1b55cf0c01d1a7eb701be2227a03e0641'
cleanup() {
  if (( $? != 0 )); then docker logs "$container_name" > "$result_dir/pitr-failure.log" 2>&1 || true; fi
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  docker volume rm "$volume_name" >/dev/null 2>&1 || true
}
trap cleanup EXIT
docker volume create "$volume_name" >/dev/null
docker run --rm -v "$volume_name:/drill" --entrypoint sh "$image" -c 'mkdir -p /drill/archive; chown -R postgres:postgres /drill'
docker run -d --name "$container_name" -e POSTGRES_HOST_AUTH_METHOD=trust -e PGDATA=/drill/source \
  -v "$volume_name:/drill" "$image" postgres -c wal_level=replica -c archive_mode=on \
  -c 'archive_command=test ! -f /drill/archive/%f && cp %p /drill/archive/%f' >/dev/null
for attempt in $(seq 1 60); do
  if docker exec "$container_name" pg_isready -U postgres >/dev/null 2>&1; then break; fi
  sleep 1
done
sql() { docker exec "$container_name" psql -X -U postgres -v ON_ERROR_STOP=1 -Atc "$1"; }
sql 'CREATE TABLE recovery_marker(id integer PRIMARY KEY, label text); INSERT INTO recovery_marker VALUES(1,'"'base backup'"');' >/dev/null
docker exec --user postgres "$container_name" pg_basebackup -U postgres -D /drill/base -X stream -c fast >/dev/null
sql "INSERT INTO recovery_marker VALUES(2,'recover this committed write');" >/dev/null
sql "SELECT pg_create_restore_point('pgbase_local_checkpoint');" >/dev/null
checkpoint_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
sql "INSERT INTO recovery_marker VALUES(3,'exclude this later write');" >/dev/null
target_wal="$(sql 'SELECT pg_walfile_name(pg_current_wal_lsn())')"
sql 'SELECT pg_switch_wal();' >/dev/null
for attempt in $(seq 1 60); do
  if docker exec "$container_name" test -f "/drill/archive/$target_wal"; then break; fi
  sleep 1
done
docker stop "$container_name" >/dev/null
docker rm "$container_name" >/dev/null
# Recovery starts from the physical backup and consumes archived WAL to the named point.
docker run --rm --user postgres -v "$volume_name:/drill" --entrypoint sh "$image" -c '
  printf "restore_command = '\''cp /drill/archive/%%f %%p'\''\nrecovery_target_name = '\''pgbase_local_checkpoint'\''\nrecovery_target_action = '\''promote'\''\n" >> /drill/base/postgresql.auto.conf
  touch /drill/base/recovery.signal
'
recovery_started="$(date +%s)"
docker run -d --name "$container_name" -e PGDATA=/drill/base -v "$volume_name:/drill" "$image" >/dev/null
for attempt in $(seq 1 60); do
  if docker exec "$container_name" pg_isready -U postgres >/dev/null 2>&1; then break; fi
  sleep 1
done
actual="$(sql 'SELECT string_agg(id::text,'"','"' ORDER BY id) FROM recovery_marker')"
if [[ "$actual" != 1,2 || "$(sql 'SELECT pg_is_in_recovery()')" != f ]]; then
  echo 'PITR did not restore exactly the named checkpoint and promote.' >&2
  exit 1
fi
elapsed="$(( $(date +%s) - recovery_started ))"
cat > "$result_dir/pitr.txt" <<REPORT
PostgreSQL 17.9 local named-point WAL recovery: PASS
Checkpoint UTC: $checkpoint_time
Recovered committed marker IDs: $actual
Later committed marker 3: absent as required
Observed container recovery time: ${elapsed}s
Observed loss before the selected checkpoint: 0 marker transactions
This disposable local database drill is not a production RPO/RTO or a cloud staging test.
REPORT
cat "$result_dir/pitr.txt"
