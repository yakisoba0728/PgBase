FROM node:24.21.0-bookworm-slim@sha256:2fe369e969550cde8e867afc3fe370b260140cab4a23d467074295b42163d553 AS admin-ui

WORKDIR /src/ui/admin
COPY ui/admin/package.json ui/admin/package-lock.json ./
# No dependency in the lock file declares preinstall/install/postinstall, and
# the native rolldown and lightningcss binaries arrive as per-platform optional
# packages rather than a download hook, so the build needs no install script.
RUN npm ci --ignore-scripts
COPY ui/admin/index.html ui/admin/tsconfig.json ui/admin/tsconfig.app.json ui/admin/vite.config.ts ./
COPY ui/admin/src ./src
RUN npm run build

FROM golang:1.26.8-bookworm@sha256:a688600ca24f8a4d3ca77f95b0dd40704a9fc787c826660eb7ba0b641b8b175d AS build

ARG VERSION=dev
ARG SOURCE_COMMIT=unknown
ARG BUILT_AT=unknown
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
COPY pgbase ./pgbase
COPY ui/admin/embed.go ui/admin/handler.go ./ui/admin/
COPY --from=admin-ui /src/ui/admin/dist ./ui/admin/dist
RUN schema_version="$(find migrations/system -maxdepth 1 -type f -name '[0-9][0-9][0-9][0-9][0-9][0-9]_*.sql' -printf '%f\n' \
      | sed -E 's/^0*([0-9]+)_.*/\1/' | sort -n | tail -1)" \
    && test -n "$schema_version" \
    && ldflags="-s -w -X github.com/yakisoba0728/PgBase/pgbase.Version=${VERSION}" \
    && ldflags="$ldflags -X github.com/yakisoba0728/PgBase/internal/buildinfo.Commit=${SOURCE_COMMIT}" \
    && ldflags="$ldflags -X github.com/yakisoba0728/PgBase/internal/buildinfo.BuiltAt=${BUILT_AT}" \
    && ldflags="$ldflags -X github.com/yakisoba0728/PgBase/internal/buildinfo.SchemaMin=$schema_version" \
    && ldflags="$ldflags -X github.com/yakisoba0728/PgBase/internal/buildinfo.SchemaMax=$schema_version" \
    && CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath -ldflags "$ldflags" -o /out/pgbase ./cmd/pgbase \
    && expected="{\"version\":\"${VERSION}\",\"commit\":\"${SOURCE_COMMIT}\",\"builtAt\":\"${BUILT_AT}\",\"goos\":\"${TARGETOS}\",\"goarch\":\"${TARGETARCH}\",\"schemaMin\":\"$schema_version\",\"schemaMax\":\"$schema_version\"}" \
    && test "$(/out/pgbase version --json)" = "$expected"

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

ARG VERSION=dev
LABEL org.opencontainers.image.source="https://github.com/yakisoba0728/PgBase" \
      org.opencontainers.image.version="${VERSION}"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=nonroot:nonroot /out/pgbase /pgbase
USER nonroot:nonroot
EXPOSE 8090
HEALTHCHECK --interval=5s --timeout=3s --start-period=10s --retries=12 \
  CMD ["/pgbase", "healthcheck", "--url", "http://127.0.0.1:8090/health/ready"]
ENTRYPOINT ["/pgbase"]
CMD ["serve"]
