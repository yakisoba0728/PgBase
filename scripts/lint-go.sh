#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repository_root"

exec go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run "$@" ./...
