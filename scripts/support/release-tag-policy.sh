#!/usr/bin/env bash

set -euo pipefail

if (($# != 2)); then
  echo "usage: release-tag-policy.sh TAG EXPECTED_VERSION" >&2
  exit 2
fi

tag="$1"
expected_version="$2"
channel=""

stable='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
prerelease='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-(alpha|beta|rc)\.(0|[1-9][0-9]*)$'

if [[ "$tag" =~ $stable ]]; then
  channel="latest"
elif [[ "$tag" =~ $prerelease ]]; then
  channel="${BASH_REMATCH[4]}"
else
  echo "unsupported release tag: $tag" >&2
  exit 2
fi

if [[ "$tag" != "$expected_version" ]]; then
  echo "release tag $tag does not match expected version $expected_version" >&2
  exit 2
fi

printf 'expected_version=%s\nchannel=%s\n' "$expected_version" "$channel"
