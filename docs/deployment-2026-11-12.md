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

## Korean UI update through API

- Deployed source: `c1e6ef3bfd341f935fcb236d9559b91535769a8f`,
  branch `codex/korean-admin-polish`.
- Used documented `compose.one`, `compose.update`, `compose.deploy` and
  `deployment.allByCompose` endpoints with API-key authentication.
  [Dokploy Compose API](https://docs.dokploy.com/docs/api/compose).
- Deployment `pc7aD7qiDsARn6y9L-K23` completed successfully at
  2026-11-12 19:29:24 KST. Automatic deployment remains off.
- Public `/health/live` and `/health/ready` returned 200. Both served assets
  matched the verified local build byte-for-byte by SHA256:
  `index-Dwg4IUKG.js` and `index-D2mchgJz.css`.
- First-administrator setup is still required. No account or credential was
  created or changed by this deployment. Database schema and file/DB volumes
  are unchanged.
- Computer Use was stopped; only agent-created tabs were closed. The update
  and verification used API/HTTP only. Local UI testing used isolated headless
  Chromium and disposable databases, without the user's Chrome profile.
- UI unit tests: 742 passed; browser scenarios: 32 distinct passed,
  four existing managed/MinIO opt-ins skipped. Go unit/race and measured
  repository policy checks passed. See `admin-korean-ui.md` for scope.
- API credentials are held in ignored, permission-restricted local storage;
  credential values are absent from Git and deployment evidence.
