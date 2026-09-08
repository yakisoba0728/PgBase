#!/usr/bin/env bash

set -euo pipefail

if (($# != 4)); then
  echo "usage: validate-private-package.sh before|after OWNER PACKAGE EVIDENCE" >&2
  exit 2
fi

mode="$1"
owner="$2"
package="$3"
evidence="$4"
: "${GH_TOKEN:?GH_TOKEN is required}"

if [[ "$mode" != "before" && "$mode" != "after" ]]; then
  echo "package privacy mode must be before or after" >&2
  exit 2
fi

package="$(tr '[:upper:]' '[:lower:]' <<< "$package")"
encoded_package="$(jq --null-input --raw-output --arg value "$package" '$value | @uri')"
body="${evidence}.body"
status="$(printf 'Authorization: Bearer %s\n' "$GH_TOKEN" | curl --silent --show-error \
  --output "$body" \
  --write-out '%{http_code}' \
  --header 'Accept: application/vnd.github+json' \
  --header @- \
  --header 'X-GitHub-Api-Version: 2022-11-28' \
  "https://api.github.com/user/packages/container/$encoded_package")"

case "$status" in
  200)
    jq --exit-status --arg owner "$owner" --arg package "$package" \
      'select((.owner.login | ascii_downcase) == ($owner | ascii_downcase) and
        .name == $package and .package_type == "container" and .visibility == "private") |
        {id, name, package_type, visibility, html_url, owner: .owner.login}' \
      "$body" > "$evidence"
    ;;
  404)
    if [[ "$mode" != "before" ]]; then
      echo "container package is missing after publication" >&2
      exit 1
    fi
    jq --null-input --arg status "$status" '{status: ($status | tonumber), state: "absent-before-publication"}' \
      > "$evidence"
    ;;
  *)
    echo "container package privacy lookup failed with HTTP $status" >&2
    exit 1
    ;;
esac
