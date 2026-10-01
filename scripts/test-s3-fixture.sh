#!/usr/bin/env bash
# Starts or stops the MinIO fixture in tests/filestore/compose.yaml as one
# Compose project. Everything it creates (containers, network, anonymous
# volumes) is scoped to that project, so jobs sharing one Docker daemon must
# pass distinct PGBASE_TEST_S3_PROJECT values; `down` removes only that project.
#
#   up    start MinIO, create the bucket, and print the single line
#         PGBASE_TEST_S3_ENDPOINT=http://127.0.0.1:<port> on stdout
#   down  remove the project's containers, network and volumes
#
# PGBASE_TEST_S3_HOST_PORT selects the loopback port (default 59000); 0 lets
# Docker pick a free one, which `up` resolves and prints.
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
project="${PGBASE_TEST_S3_PROJECT:-pgbase-test-files}"
compose=(docker compose --project-name "$project" --file "$repository_root/tests/filestore/compose.yaml")

case "${1:-}" in
	up)
		# Only the endpoint line belongs on stdout; callers append it to a
		# GitHub step output or eval it. The image is built from source (see
		# tests/filestore/Dockerfile) and labelled with that file's digest, so
		# an existing image (including one restored by `docker load`) is
		# reused only while the Dockerfile is unchanged.
		dockerfile="$repository_root/tests/filestore/Dockerfile"
		if command -v sha256sum >/dev/null; then
			digest="$(sha256sum "$dockerfile" | cut -d' ' -f1)"
		else
			digest="$(shasum -a 256 "$dockerfile" | cut -d' ' -f1)"
		fi
		image="$("${compose[@]}" config --images | sort -u)"
		built="$(docker image inspect --format '{{ index .Config.Labels "org.pgbase.test-s3.dockerfile-sha256" }}' "$image" 2>/dev/null || true)"
		if [[ "$built" != "$digest" ]]; then
			"${compose[@]}" build --quiet --build-arg "DOCKERFILE_SHA256=$digest" minio >&2
		fi
		"${compose[@]}" up --detach minio >&2
		"${compose[@]}" run --rm create-bucket >&2
		address="$("${compose[@]}" port minio 9000)"
		if [[ ! "$address" =~ ^127\.0\.0\.1:[0-9]+$ ]]; then
			echo "could not resolve the MinIO port of Compose project $project: '$address'" >&2
			exit 1
		fi
		echo "PGBASE_TEST_S3_ENDPOINT=http://$address"
		;;
	down)
		"${compose[@]}" down --volumes --remove-orphans
		;;
	*)
		echo "usage: $0 up|down" >&2
		exit 2
		;;
esac
