#!/usr/bin/env bash

set -euo pipefail

if (($# != 2)); then
  echo "usage: validate-release-candidate.sh CANDIDATE_IMAGE EXPECTED_VERSION" >&2
  exit 2
fi

candidate_image="$1"
expected_version="$2"
script_directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

"$script_directory/release-tag-policy.sh" "$expected_version" "$expected_version" >/dev/null

digest_pattern='^[^@[:space:]]+@sha256:[0-9a-f]{64}$'
if [[ ! "$candidate_image" =~ $digest_pattern ]]; then
  echo "candidate image must be an image@sha256 digest reference" >&2
  exit 2
fi

image_name="${candidate_image%@sha256:*}"
if [[ "${image_name##*/}" == *:* ]]; then
  echo "candidate image must not include a mutable tag" >&2
  exit 2
fi
