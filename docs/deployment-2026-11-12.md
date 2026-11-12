# 2026-11-12 baseline administrator UI deployment

- Target: https://pgbase.yaki.kr/admin/ (Dokploy Compose production service).
- Source: `f7de2f5ef0ccc029f5e21e0386e58b62c79c4fb1`, branch `codex/pocketbase-admin-rebuild`.
- Previous deployment: `82411a18fa6a42a225b0609c9e3dd176b0916c14` on main.
- Automatic deployment stays off. No secrets, domains, database or file volume names changed.
- Before deployment PostgreSQL custom dump written with umask077 inside persistent database volume:
  `/var/lib/postgresql/data/pgbase-pre-f7de2f5/database.dump`.
  `pg_restore -l` succeeded; SHA256 `671714fdbbb5266a9da323df4c769b3ff1f16f78ae2511c022c82ce520db87e3`.
- App/files and migration containers rebuilt; migration and files-init exited successfully; app started.
- `/health/live` and `/health/ready` returned200. Public admin HTML references the expected `index-C4NLnJdU.js` and `index-BdOVtmRc.css`.
- Fresh browser reached first-administrator setup with the new UI. No administrator credentials created or entered by the agent.
- Source-built image metadata reports `dev/unknown`; deployed asset hashes and Dokploy source branch/build log identify this deployment.
- Database dump is retained on the same server volume, not an independent off-server backup. File volume unchanged; file backup was not taken for this schema-preserving update.
- Local screenshot: `.local/ui-rebuild-evidence/deployed-f7de2f5.jpg`.
