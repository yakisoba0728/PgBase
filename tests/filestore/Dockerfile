# Test-only S3 fixture image. Upstream stopped serving MinIO images to
# anonymous pulls (quay.io/minio/* and docker.io/minio/* answer 401), so this
# builds the same MinIO server and client releases the fixture used before
# from their pinned upstream commits. `go install module@version` resolves
# through the Go module proxy and verifies each module against the checksum
# database, so the build is reproducible without access to the archived
# GitHub repositories.
FROM golang:1.26.8-bookworm@sha256:a688600ca24f8a4d3ca77f95b0dd40704a9fc787c826660eb7ba0b641b8b175d AS build

# RELEASE.2025-07-23T15-54-02Z -> commit 7ced9663e6a791fef9dc6be798ff24cda9c730ac
ARG MINIO_RELEASE=RELEASE.2025-07-23T15-54-02Z
ARG MINIO_VERSION=2025-07-23T15:54:02Z
ARG MINIO_COMMIT=7ced9663e6a791fef9dc6be798ff24cda9c730ac
ARG MINIO_MODULE_VERSION=v0.0.0-20250723155402-7ced9663e6a7
# RELEASE.2025-07-21T05-28-08Z -> commit ee72571936f15b0e65dc8b4a231a4dd445e5ccb6
ARG MC_RELEASE=RELEASE.2025-07-21T05-28-08Z
ARG MC_VERSION=2025-07-21T05:28:08Z
ARG MC_COMMIT=ee72571936f15b0e65dc8b4a231a4dd445e5ccb6
ARG MC_MODULE_VERSION=v0.0.0-20250721052808-ee72571936f1

ENV CGO_ENABLED=0 GOFLAGS=-trimpath GOTOOLCHAIN=local GOBIN=/out

RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
	go install -ldflags "-s -w \
	-X github.com/minio/minio/cmd.Version=${MINIO_VERSION} \
	-X github.com/minio/minio/cmd.ReleaseTag=${MINIO_RELEASE} \
	-X github.com/minio/minio/cmd.CommitID=${MINIO_COMMIT} \
	-X github.com/minio/minio/cmd.CopyrightYear=2025" \
	"github.com/minio/minio@${MINIO_MODULE_VERSION}"

RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
	go install -ldflags "-s -w \
	-X github.com/minio/mc/cmd.Version=${MC_VERSION} \
	-X github.com/minio/mc/cmd.ReleaseTag=${MC_RELEASE} \
	-X github.com/minio/mc/cmd.CommitID=${MC_COMMIT} \
	-X github.com/minio/mc/cmd.CopyrightYear=2025" \
	"github.com/minio/mc@${MC_MODULE_VERSION}"

# The bucket bootstrap needs a shell, seq and sleep; both binaries are static.
FROM alpine@sha256:48b0309ca019d89d40f670aa1bc06e426dc0931948452e8491e3d65087abc07d

# scripts/test-s3-fixture.sh passes this file's digest and rebuilds when the
# existing image's label differs.
ARG DOCKERFILE_SHA256=unknown
LABEL org.pgbase.test-s3.dockerfile-sha256=${DOCKERFILE_SHA256}

COPY --from=build /out/minio /out/mc /usr/local/bin/
ENTRYPOINT ["minio"]
