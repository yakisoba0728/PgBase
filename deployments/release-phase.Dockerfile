FROM golang:1.26.11-bookworm@sha256:dc40a13b61304647ed7e5d581d246b86413dfc29d63c6d018310feb93cd98fc0 AS certificates

FROM golang:1.26.11-bookworm@sha256:dc40a13b61304647ed7e5d581d246b86413dfc29d63c6d018310feb93cd98fc0 AS postgres-libraries
COPY deployments/build-postgres-client-libraries.sh /build-postgres-client-libraries.sh
RUN sh /build-postgres-client-libraries.sh

FROM postgres:16.16-bookworm@sha256:a49f7438ba03589f9b1ca062f2da82e899b48ea1dc50d2449fa09a70be83ebca AS postgres16
COPY --from=postgres-libraries /patched/lib/ /opt/pgbase/postgresql/lib/
COPY --from=postgres-libraries /patched/usr/ /runtime/usr/
COPY --from=postgres-libraries /patched/var/ /runtime/var/
ENV LD_LIBRARY_PATH="/opt/pgbase/postgresql/lib"
COPY deployments/postgres-client-runtime.sh /postgres-client-runtime.sh
RUN sh /postgres-client-runtime.sh 16

FROM postgres:17.12-bookworm@sha256:40865bc877a289da75963d97d72b59633f639fc5af7a4e856bfa3714cae5b536 AS postgres17
COPY --from=postgres-libraries /patched/lib/ /opt/pgbase/postgresql/lib/
COPY --from=postgres-libraries /patched/usr/ /runtime/usr/
COPY --from=postgres-libraries /patched/var/ /runtime/var/
ENV LD_LIBRARY_PATH="/opt/pgbase/postgresql/lib"
COPY deployments/postgres-client-runtime.sh /postgres-client-runtime.sh
RUN sh /postgres-client-runtime.sh 17

FROM postgres:18.7-bookworm@sha256:ece55f7868da684e400f82f1030a050718adbdd65c23aa500c486f6e2950dc0f AS postgres18
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
