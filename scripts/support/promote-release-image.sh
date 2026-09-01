#!/usr/bin/env bash

set -euo pipefail

if (($# != 4)); then
  echo "usage: promote-release-image.sh IMAGE TAG CANDIDATE_IMAGE EXPECTED_VERSION" >&2
  exit 2
fi

image="$1"
tag="$2"
candidate_image="$3"
expected_version="$4"
script_directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

policy="$("$script_directory/release-tag-policy.sh" "$tag" "$expected_version")"
channel="$(sed -n 's/^channel=//p' <<< "$policy")"
if [[ -z "$channel" ]]; then
  echo "release policy did not select a promotion channel" >&2
  exit 2
fi

"$script_directory/validate-release-candidate.sh" "$candidate_image" "$expected_version"
if [[ "$image" == *[[:space:]@]* || "${image##*/}" == *:* || "$candidate_image" != "$image"@sha256:* ]]; then
  echo "candidate image does not belong to the release image repository" >&2
  exit 2
fi
candidate_digest="${candidate_image##*@}"

inspect_digest() {
  local reference="$1"
  local output status digest
  set +e
  output="$(docker buildx imagetools inspect "$reference" --format '{{json .Manifest}}' 2>&1)"
  status=$?
  set -e
  if ((status != 0)); then
    if grep -Eiq 'manifest unknown|manifest[^[:alnum:]]+not found|not found[^[:alnum:]]+manifest|:[[:space:]]+not found([[:space:]]|$)' <<< "$output"; then
      printf 'absent'
      return 0
    fi
    echo "could not read registry digest for $reference: $output" >&2
    return 1
  fi
  if ! digest="$(jq --exit-status --raw-output '.digest | select(test("^sha256:[0-9a-f]{64}$"))' <<< "$output")"; then
    echo "registry returned a malformed digest for $reference" >&2
    return 1
  fi
  printf '%s' "$digest"
}

retag_same_digest() {
  local target="$1"
  local actual
  docker buildx imagetools create --prefer-index=false --tag "$target" "$candidate_image" >/dev/null
  actual="$(inspect_digest "$target")"
  if [[ "$actual" != "$candidate_digest" ]]; then
    echo "registry readback for $target is $actual, want $candidate_digest" >&2
    return 1
  fi
}

exact_reference="$image:$tag"
channel_reference="$image:$channel"

# The immutable exact tag is always the first registry state read. A conflict
# stops before even reading, let alone mutating, a moving channel.
exact_before="$(inspect_digest "$exact_reference")"
printf 'exact_before=%s\n' "$exact_before"
if [[ "$exact_before" != "absent" && "$exact_before" != "$candidate_digest" ]]; then
  echo "immutable exact tag $exact_reference already points at different digest $exact_before" >&2
  exit 1
fi

channel_before="$(inspect_digest "$channel_reference")"
printf 'channel=%s\nchannel_before=%s\n' "$channel" "$channel_before"

if [[ "$exact_before" == "absent" ]]; then
  retag_same_digest "$exact_reference"
fi
if [[ "$channel_before" != "$candidate_digest" ]]; then
  retag_same_digest "$channel_reference"
fi

exact_after="$(inspect_digest "$exact_reference")"
channel_after="$(inspect_digest "$channel_reference")"
if [[ "$exact_after" != "$candidate_digest" || "$channel_after" != "$candidate_digest" ]]; then
  echo "promotion readback did not preserve the candidate digest" >&2
  exit 1
fi
printf 'exact_after=%s\nchannel_after=%s\n' "$exact_after" "$channel_after"
