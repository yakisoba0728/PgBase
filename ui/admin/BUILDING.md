# Admin bundle provenance

`dist/` is a checked-in production artifact. Its only supported source is the
locked dependency graph and the TypeScript source in this directory; fonts
(Pretendard) and icons come from npm packages, so the build fetches nothing else.

Run `npm ci` followed by `npm run verify:dist` from `ui/admin`. The command
rebuilds the bundle (Vite empties `dist/` first) and fails if that rebuild
changes a tracked asset or leaves an untracked generated asset. Release
automation should use the same command. The Docker image builds the same output
from `package.json`, `package-lock.json`, `index.html`, `tsconfig*.json`,
`vite.config.ts` and `src/`.

## Browser tests

`npm run test:e2e` runs the Playwright suite in `e2e/` against real servers,
the way `.github/workflows/e2e.yml` does:

- `onboarding-chromium` (`e2e/onboarding`) needs an instance with no
  administrator at `PGBASE_ONBOARDING_BROWSER_BASE_URL` and its
  `PGBASE_SETUP_TOKEN`.
- `authenticated-chromium` (`e2e/authenticated`) runs only when
  `PGBASE_AUTHENTICATED_BROWSER_BASE_URL` is set. It must be a different
  backend whose administrator is `PGBASE_ADMIN_EMAIL` / `PGBASE_ADMIN_PASSWORD`.
  Specs create uniquely named collections, so reruns against one backend work.
- `PGBASE_BROWSER_MANAGED_BACKUPS=1` opts in to the backup scenario, which needs
  `pg_dump` and a writable backup directory on the server host.

Install the browser once with `npx playwright install chromium`.
