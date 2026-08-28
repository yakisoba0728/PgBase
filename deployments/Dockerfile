FROM node:24.18.0-bookworm-slim@sha256:6f7b03f7c2c8e2e784dcf9295400527b9b1270fd37b7e9a7285cf83b6951452d AS admin-ui

WORKDIR /src/ui/admin
COPY ui/admin/package.json ui/admin/package-lock.json ./
RUN npm ci
COPY ui/admin/index.html ui/admin/tsconfig.json ui/admin/tsconfig.app.json ui/admin/vite.config.ts ./
COPY ui/admin/src ./src
RUN npm run build

FROM golang:1.27.0-bookworm@sha256:ded31c68586d2e49e760acc2e65a884b23d032e9bbbed0ae0c55abd3fcaf4452 AS build

ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
COPY pgbase ./pgbase
COPY ui/admin/embed.go ui/admin/handler.go ui/admin/handler_test.go ./ui/admin/
COPY --from=admin-ui /src/ui/admin/dist ./ui/admin/dist
RUN go test ./...
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/webisnoteasy/PgBase/pgbase.Version=${VERSION}" -o /out/pgbase ./cmd/pgbase

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

LABEL org.opencontainers.image.source="https://github.com/webisnoteasy/PgBase"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=nonroot:nonroot /out/pgbase /pgbase
USER nonroot:nonroot
EXPOSE 8090
HEALTHCHECK --interval=5s --timeout=3s --start-period=10s --retries=12 \
  CMD ["/pgbase", "healthcheck", "--url", "http://127.0.0.1:8090/health/ready"]
ENTRYPOINT ["/pgbase"]
CMD ["serve"]
