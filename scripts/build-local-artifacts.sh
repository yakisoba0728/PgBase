#!/usr/bin/env bash
set -euo pipefail
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"
output_dir="${1:-$root_dir/.local/artifacts}"
mkdir -p "$output_dir"
for target_os in linux darwin windows; do
  for target_arch in amd64 arm64; do
    suffix=""
    if [[ "$target_os" == windows ]]; then suffix=.exe; fi
    artifact="$output_dir/pgbase-$target_os-$target_arch$suffix"
    CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" GOTOOLCHAIN=go1.26.8 \
      go build -trimpath -o "$artifact" ./cmd/pgbase
  done
done
(
  cd "$output_dir"
  shasum -a 256 pgbase-* > SHA256SUMS
)
printf 'Local builds and checksums: %s\n' "$output_dir"
