#!/usr/bin/env bash

set -euo pipefail
export LC_ALL=C

if (($# < 4 || $# > 5)); then
  echo "usage: promote-release-image.sh IMAGE TAG CANDIDATE_IMAGE EXPECTED_VERSION [LEGACY_CHANNEL_VERSION]" >&2
  exit 2
fi

image="$1"
tag="$2"
candidate_image="$3"
expected_version="$4"
legacy_channel_version="${5:-}"
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
canonical_version_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(alpha|beta|rc)\.(0|[1-9][0-9]*))?$'

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

read_version() {
  local reference="$1"
  local output status config labels
  inspected_version=""
  inspected_version_present=0
  set +e
  output="$(docker buildx imagetools inspect "$reference" --format '{{json .Image}}' 2>&1)"
  status=$?
  set -e
  if ((status != 0)); then
    echo "could not read registry image metadata for $reference: $output" >&2
    return 1
  fi
  if ! jq --exit-status . >/dev/null 2>&1 <<< "$output"; then
    echo "registry returned malformed image metadata for $reference" >&2
    return 1
  fi
  if jq --exit-status '(.config | type) == "object"' >/dev/null <<< "$output"; then
    config="$(jq --compact-output '.config' <<< "$output")"
  elif jq --exit-status '(. ["linux/amd64"].config | type) == "object"' >/dev/null <<< "$output"; then
    config="$(jq --compact-output '.["linux/amd64"].config' <<< "$output")"
  else
    echo "registry image metadata for $reference has no linux/amd64 config" >&2
    return 1
  fi
  labels="$(jq --compact-output 'if has("Labels") then .Labels else null end' <<< "$config")"
  if [[ "$labels" == "null" ]]; then
    return 0
  fi
  if [[ "$(jq --raw-output type <<< "$labels")" != "object" ]]; then
    echo "registry image metadata for $reference has malformed labels" >&2
    return 1
  fi
  if ! jq --exit-status --arg key 'org.opencontainers.image.version' 'has($key)' >/dev/null <<< "$labels"; then
    return 0
  fi
  # Validate before Bash command substitution can strip trailing newlines or
  # NUL bytes from a present-but-malformed JSON string.
  if ! jq --exit-status \
    --arg key 'org.opencontainers.image.version' \
    --arg pattern "$canonical_version_pattern" \
    '.[$key] as $value | (($value | type) == "string") and ($value | test("[^v0-9a-z.-]") | not) and ($value | test($pattern))' \
    >/dev/null <<< "$labels"; then
    echo "registry image metadata for $reference has a malformed version label" >&2
    return 1
  fi
  inspected_version="$(jq --raw-output --arg key 'org.opencontainers.image.version' '.[$key]' <<< "$labels")"
  inspected_version_present=1
}

numeric_relation() {
  local left="$1"
  local right="$2"
  if ((${#left} < ${#right})); then
    printf '%s' -1
  elif ((${#left} > ${#right})); then
    printf '%s' 1
  elif [[ "$left" < "$right" ]]; then
    printf '%s' -1
  elif [[ "$left" > "$right" ]]; then
    printf '%s' 1
  else
    printf '%s' 0
  fi
}

version_relation() {
  local candidate="$1"
  local current="$2"
  local selected_channel="$3"
  local candidate_body="${candidate#v}"
  local current_body="${current#v}"
  local candidate_core="${candidate_body%%-*}"
  local current_core="${current_body%%-*}"
  local candidate_major candidate_minor candidate_patch
  local current_major current_minor current_patch
  local relation index
  local -a candidate_parts current_parts

  IFS=. read -r candidate_major candidate_minor candidate_patch <<< "$candidate_core"
  IFS=. read -r current_major current_minor current_patch <<< "$current_core"
  candidate_parts=("$candidate_major" "$candidate_minor" "$candidate_patch")
  current_parts=("$current_major" "$current_minor" "$current_patch")
  for index in 0 1 2; do
    relation="$(numeric_relation "${candidate_parts[$index]}" "${current_parts[$index]}")"
    if [[ "$relation" != 0 ]]; then
      printf '%s' "$relation"
      return 0
    fi
  done
  if [[ "$selected_channel" == "latest" ]]; then
    printf '%s' 0
    return 0
  fi
  numeric_relation "${candidate_body##*.}" "${current_body##*.}"
}

canonical_channel() {
  local version="$1"
  local version_policy
  if ! version_policy="$("$script_directory/release-tag-policy.sh" "$version" "$version")"; then
    echo "channel version $version is not a canonical release version" >&2
    return 1
  fi
  sed -n 's/^channel=//p' <<< "$version_policy"
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

# An immutable exact-tag conflict is the first registry decision. It stops the
# promotion before the candidate or moving channel is read and before any tag
# can be mutated.
exact_before="$(inspect_digest "$exact_reference")"
printf 'exact_before=%s\n' "$exact_before"
if [[ "$exact_before" != "absent" && "$exact_before" != "$candidate_digest" ]]; then
  echo "immutable exact tag $exact_reference already points at different digest $exact_before" >&2
  exit 1
fi

read_version "$candidate_image"
candidate_version="$inspected_version"
printf 'candidate_version=%s\n' "$candidate_version"
if ((inspected_version_present == 0)) || [[ "$candidate_version" != "$expected_version" ]]; then
  echo "candidate image version is $candidate_version, want $expected_version" >&2
  exit 1
fi
if [[ "$exact_before" == "$candidate_digest" ]]; then
  read_version "$exact_reference"
  exact_version_before="$inspected_version"
  if ((inspected_version_present == 0)) || [[ "$exact_version_before" != "$expected_version" ]]; then
    echo "immutable exact tag version is $exact_version_before, want $expected_version" >&2
    exit 1
  fi
fi

channel_before="$(inspect_digest "$channel_reference")"
printf 'channel=%s\nchannel_before=%s\n' "$channel" "$channel_before"
channel_version_before="absent"
channel_version_source="none"
if [[ "$channel_before" != "absent" ]]; then
  read_version "$channel_reference"
  channel_version_before="$inspected_version"
  channel_version_source="label"
  if ((inspected_version_present == 0)); then
    if [[ -z "$legacy_channel_version" ]]; then
      echo "moving channel $channel_reference has no OCI version label; provide an explicit legacy channel version" >&2
      exit 1
    fi
    legacy_channel="$(canonical_channel "$legacy_channel_version")"
    if [[ "$legacy_channel" != "$channel" ]]; then
      echo "legacy channel version $legacy_channel_version belongs to $legacy_channel, not $channel" >&2
      exit 1
    fi
    legacy_exact_reference="$image:$legacy_channel_version"
    legacy_exact_digest="$(inspect_digest "$legacy_exact_reference")"
    if [[ "$legacy_exact_digest" != "$channel_before" ]]; then
      echo "legacy channel version $legacy_channel_version is not anchored by immutable exact tag $legacy_exact_reference" >&2
      exit 1
    fi
    channel_version_before="$legacy_channel_version"
    channel_version_source="legacy-exact-tag"
  else
    current_channel="$(canonical_channel "$channel_version_before")"
    if [[ "$current_channel" != "$channel" ]]; then
      echo "moving channel $channel_reference carries version $channel_version_before for channel $current_channel" >&2
      exit 1
    fi
  fi
fi
printf 'channel_version_before=%s\nchannel_version_source=%s\n' "$channel_version_before" "$channel_version_source"

if [[ "$channel_before" != "absent" ]]; then
  if [[ "$channel_before" != "$candidate_digest" ]]; then
    relation="$(version_relation "$expected_version" "$channel_version_before" "$channel")"
    if [[ "$relation" != 1 ]]; then
      echo "release $expected_version would not advance $channel from $channel_version_before" >&2
      exit 1
    fi
  elif [[ "$channel_version_before" != "$expected_version" ]]; then
    echo "moving channel digest already matches the candidate but its version is $channel_version_before, want $expected_version" >&2
    exit 1
  fi
fi

# All immutable-tag, artifact-label, channel-version, and legacy-bootstrap
# checks above are read-only. Mutations begin only after regression is ruled out.
if [[ "$exact_before" == "absent" ]]; then
  retag_same_digest "$exact_reference"
fi
if [[ "$channel_before" != "$candidate_digest" ]]; then
  retag_same_digest "$channel_reference"
fi

exact_after="$(inspect_digest "$exact_reference")"
channel_after="$(inspect_digest "$channel_reference")"
read_version "$exact_reference"
exact_version_after="$inspected_version"
exact_version_present="$inspected_version_present"
read_version "$channel_reference"
channel_version_after="$inspected_version"
channel_version_present="$inspected_version_present"
if [[ "$exact_after" != "$candidate_digest" || "$channel_after" != "$candidate_digest" ]]; then
  echo "promotion readback did not preserve the candidate digest" >&2
  exit 1
fi
if ((exact_version_present == 0 || channel_version_present == 0)) ||
  [[ "$exact_version_after" != "$expected_version" || "$channel_version_after" != "$expected_version" ]]; then
  echo "promotion readback did not preserve version $expected_version" >&2
  exit 1
fi
printf 'exact_after=%s\nchannel_after=%s\n' "$exact_after" "$channel_after"
printf 'exact_version_after=%s\nchannel_version_after=%s\n' "$exact_version_after" "$channel_version_after"
