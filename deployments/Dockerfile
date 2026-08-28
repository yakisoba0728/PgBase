FROM node:26.7.0-bookworm-slim@sha256:4db36457f406501e6f608802e5da617e5fbd0e80b75901b6a09de1ae5a667d32 AS admin-ui

WORKDIR /src/ui/admin
COPY ui/admin/package.json ui/admin/package-lock.json ./
RUN npm ci
COPY ui/admin/index.html ui/admin/tsconfig.json ui/admin/tsconfig.app.json ui/admin/vite.config.ts ./
COPY ui/admin/src ./src
RUN npm run build

FROM golang:1.26.7-bookworm@sha256:e8c859f5632dcfde7b32d2012b4351728f6437930887c2f6a91ea242459e5514 AS build

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
