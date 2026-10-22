#!/usr/bin/env bash
set -euo pipefail
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"
output_dir="${1:-$root_dir/.local/artifacts}"
mkdir -p "$output_dir"
version="${PGBASE_BUILD_VERSION:-dev}"
commit="$(git rev-parse --verify HEAD)"
built_at="${PGBASE_BUILD_AT:-$(git show -s --format=%cI HEAD)}"
schema_version="$(find migrations/system -maxdepth 1 -type f -name '[0-9][0-9][0-9][0-9][0-9][0-9]_*.sql' -print |
  sed -E 's|.*/0*([0-9]+)_.*|\1|' | sort -n | tail -1)"
if [[ ! "$version" =~ ^[A-Za-z0-9][A-Za-z0-9._+-]*$ ||
      ! "$commit" =~ ^[0-9a-f]{40}$ ||
      -z "$built_at" || "$built_at" == *[[:space:]]* ||
      ! "$schema_version" =~ ^[1-9][0-9]*$ ]]; then
  echo "local build metadata is invalid" >&2
  exit 1
fi
ldflags="-s -w -X github.com/yakisoba0728/PgBase/pgbase.Version=$version"
ldflags+=" -X github.com/yakisoba0728/PgBase/internal/buildinfo.Commit=$commit"
ldflags+=" -X github.com/yakisoba0728/PgBase/internal/buildinfo.BuiltAt=$built_at"
ldflags+=" -X github.com/yakisoba0728/PgBase/internal/buildinfo.SchemaMin=$schema_version"
ldflags+=" -X github.com/yakisoba0728/PgBase/internal/buildinfo.SchemaMax=$schema_version"
host_os="$(GOTOOLCHAIN=go1.26.10 go env GOOS)"
host_arch="$(GOTOOLCHAIN=go1.26.10 go env GOARCH)"
for target_os in linux darwin windows; do
  for target_arch in amd64 arm64; do
    suffix=""
    if [[ "$target_os" == windows ]]; then suffix=.exe; fi
    artifact="$output_dir/pgbase-$target_os-$target_arch$suffix"
    CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" GOTOOLCHAIN=go1.26.10 \
      go build -trimpath -ldflags "$ldflags" -o "$artifact" ./cmd/pgbase
    if [[ "$target_os" == "$host_os" && "$target_arch" == "$host_arch" ]]; then
      expected="{\"version\":\"$version\",\"commit\":\"$commit\",\"builtAt\":\"$built_at\",\"goos\":\"$target_os\",\"goarch\":\"$target_arch\",\"schemaMin\":\"$schema_version\",\"schemaMax\":\"$schema_version\"}"
      if [[ "$("$artifact" version --json)" != "$expected" ]]; then
        echo "native artifact build metadata does not match its inputs" >&2
        exit 1
      fi
    fi
  done
done
(
  cd "$output_dir"
  shasum -a 256 pgbase-* > SHA256SUMS
)
printf 'Local builds and checksums: %s\n' "$output_dir"
