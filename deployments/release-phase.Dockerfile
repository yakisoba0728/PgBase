FROM golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea AS certificates

FROM golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea AS postgres-libraries
COPY deployments/build-postgres-client-libraries.sh /build-postgres-client-libraries.sh
RUN sh /build-postgres-client-libraries.sh

FROM postgres:16.16-bookworm@sha256:ce3efec6bc404f6b15ebf5d9bed6b45a9d4655aea238d7dacc3467ccb05a1175 AS postgres16
COPY --from=postgres-libraries /patched/lib/ /opt/pgbase/postgresql/lib/
COPY --from=postgres-libraries /patched/usr/ /runtime/usr/
COPY --from=postgres-libraries /patched/var/ /runtime/var/
ENV LD_LIBRARY_PATH="/opt/pgbase/postgresql/lib"
COPY deployments/postgres-client-runtime.sh /postgres-client-runtime.sh
RUN sh /postgres-client-runtime.sh 16

FROM postgres:17.12-bookworm@sha256:837bed18782820fe967067cb5328b02b0d9992c1be9a37ee0c9c492cfc03af3b AS postgres17
COPY --from=postgres-libraries /patched/lib/ /opt/pgbase/postgresql/lib/
COPY --from=postgres-libraries /patched/usr/ /runtime/usr/
COPY --from=postgres-libraries /patched/var/ /runtime/var/
ENV LD_LIBRARY_PATH="/opt/pgbase/postgresql/lib"
COPY deployments/postgres-client-runtime.sh /postgres-client-runtime.sh
RUN sh /postgres-client-runtime.sh 17

FROM postgres:18.7-bookworm@sha256:194e55feb2597b1465dbee81cc9fd8d204566cd2eb42c8d22fd6c931a38781b1 AS postgres18
COPY --from=postgres-libraries /patched/lib/ /opt/pgbase/postgresql/lib/
COPY --from=postgres-libraries /patched/usr/ /runtime/usr/
COPY --from=postgres-libraries /patched/var/ /runtime/var/
ENV LD_LIBRARY_PATH="/opt/pgbase/postgresql/lib"
COPY deployments/postgres-client-runtime.sh /postgres-client-runtime.sh
RUN sh /postgres-client-runtime.sh 18

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

# Copy the newest compatible Debian/libpq dependency closure last.
COPY --from=postgres16 /runtime/ /
COPY --from=postgres17 /runtime/ /
COPY --from=postgres18 /runtime/ /
ENV LD_LIBRARY_PATH="/opt/pgbase/postgresql/lib"
ENV PATH="/opt/pgbase/postgresql/18/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

ARG SOURCE_COMMIT
ARG VERSION
LABEL org.opencontainers.image.source="https://github.com/yakisoba0728/PgBase" \
      org.opencontainers.image.revision="$SOURCE_COMMIT" \
      org.opencontainers.image.version="$VERSION"
COPY --from=certificates /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --chown=nonroot:nonroot pgbase /pgbase
USER nonroot:nonroot
EXPOSE 8090
HEALTHCHECK --interval=5s --timeout=3s --start-period=10s --retries=12 \
  CMD ["/pgbase", "healthcheck", "--url", "http://127.0.0.1:8090/health/ready"]
ENTRYPOINT ["/pgbase"]
CMD ["serve"]
