#!/usr/bin/env bash
set -euo pipefail

destination=${1:?usage: install-test-pg-tools.sh DESTINATION}
mkdir -p "$destination"

for tool in pg_dump pg_restore; do
  wrapper="$destination/$tool"
  sed "s/__PG_TOOL__/$tool/g" >"$wrapper" <<'WRAPPER'
#!/usr/bin/env bash
set -euo pipefail
: "${PGBASE_TEST_POSTGRES_IMAGE:?PGBASE_TEST_POSTGRES_IMAGE is required}"

docker run --rm --network host \
  --volume /tmp:/tmp \
  --volume "$PWD:$PWD" \
  --workdir "$PWD" \
  --env PGHOST --env PGPORT --env PGDATABASE --env PGUSER \
  --env PGPASSFILE --env PGSSLMODE --env PGSSLROOTCERT \
  --env PGSSLCERT --env PGSSLKEY --env PGSSLCRL \
  --entrypoint /usr/bin/__PG_TOOL__ \
  "$PGBASE_TEST_POSTGRES_IMAGE" "$@"
WRAPPER
  chmod 0755 "$wrapper"
done
