# Local development

Prerequisites: Docker Compose, Go 1.26.10 (automatic toolchain download is supported), OpenSSL, and Node 24 for UI/SDK development. From the repository root:

```sh
scripts/dev-local.sh
```

The script creates a PostgreSQL 17 container on loopback port 55440, builds and starts PgBase at <http://127.0.0.1:8090/admin/>, and starts a local mail inbox at <http://127.0.0.1:8025>. SMTP listens on loopback port 1025. The first start generates private settings at `.local/pgbase/environment`, including the setup token required by the admin onboarding page. Open that local file to copy the setup token; do not commit it or share its contents.

User registration, mail actions, and local file storage are enabled. Verification, reset, and email-change messages appear in the local inbox; links open a confirmation page and require an explicit confirmation. This inbox is a development tool with no external mail delivery. The persisted database uses the Compose `data` volume; files live in `.local/pgbase/files`. Restarting the script reuses both. Ctrl-C stops PgBase and its mail process. Stop PostgreSQL without deleting data with:

```sh
source .local/pgbase/environment
docker compose -f deployments/local/docker-compose.yml stop
```

Do not run two copies on the same ports. If a port is already occupied, stop the previous local process before restarting. The generated environment is a Bash source file and should only contain locally trusted settings. The `.local/` directory is ignored by Git.

To change the admin UI, use `npm ci`, `npm run dev` in `ui/admin`; run `npm run build` to refresh the embedded production assets before rebuilding PgBase. The SDK and browser example are under `sdk/typescript` and `examples`. The CORS origin of the local example is `http://127.0.0.1:5173`.

This setup has no public domain, TLS termination, cloud deployment, real OAuth client credentials, or production email delivery. Use locally configured mock providers to develop OAuth flows. Jobs and Go mutation hooks are configured through the embedding API; see [local jobs](local-jobs.md).
