# Frontend and SDK continuation evidence

Recorded on 2026-11-16, in the isolated `final/frontend-20261116` worktree.
Node 24.22.1; Dart 3.13.3; locked project dependencies (TypeScript 7.0.2,
Vitest 5.0.5, Playwright 1.63.0). No production services or credentials were used.
The Node/Dart regressions use synthetic tokens and controlled in-memory streams;
UI tests use jsdom and deferred mock responses.

Latest runtime source tested here is `dcbfd4a2`; SDK lifetime source is
`97e7f7e8`, followed by TypeScript SSE cleanup `440466b2`. The managed browser
fixture is `737742c8`. The Dart suite ran after `97e7f7e8` and Dart source did
not change in the subsequent commits. Integrated branch/server qualification
is separate from this workstream evidence.

The nine local logs below are copied without filtering. In particular, expected baseline
failures and jsdom's document-navigation diagnostic remain visible. The manifest
records each raw file's byte count and SHA-256.

| Raw log | Command (from the corresponding SDK/UI directory) | Result |
|---|---|---|
| [ui-session-before.log](ui-session-before.log) | `npm test -- --maxWorkers=2 src/api/records.test.ts src/schema/apply.lifecycle.test.ts src/ops/RealtimeView.test.tsx src/collections/FileField.test.tsx src/collections/RecordsView.test.tsx` | Exit1;10 new regressions failed,14 controls passed before the session-scope fixes |
| [ui-pool-before.log](ui-pool-before.log) | `npm test -- --maxWorkers=2 src/collections/queries.test.ts` | Exit1;2 new pool regressions failed,7 controls passed before the pool fix |
| [ui-final.log](ui-final.log) | `npm test -- --maxWorkers=2` | Exit0;440 tests,78 files passed after all UI changes |
| [typescript-sse-before.log](typescript-sse-before.log) | `node --test test/sse-lifecycle.test.mjs` | Exit1;both cancellation regressions failed before the SSE fix |
| [typescript-final.log](typescript-final.log) | `npm test` | Exit0;build,public type contract,standalone SDK checks and113 Node tests passed |
| [dart-final.log](dart-final.log) | `dart test -r expanded` | Exit0;132 tests passed;the separate `dart analyze` command reported no issues |
| [ui-build.log](ui-build.log) | `npm run build` | Exit0;TypeScript and Vite production build |
| [ui-dist-parity.log](ui-dist-parity.log) | `npm run verify:dist` | Exit0;rebuild exactly matches committed dist |
| [playwright-discovery.log](playwright-discovery.log) | `npm run test:e2e -- --list` with all three distinct fixture URLs | Exit0;26 browser tests discovered,including the isolated managed restore |

The later [hosted backup failure excerpt](hosted-backup-failure-43797156393.log)
is a contiguous excerpt, not the complete job log, from
[job 133068458736](https://github.com/yakisoba0728/PgBase/actions/runs/43797156393/job/133068458736).
It records 25 passing cases and the managed-backup upload response's Chromium
inspector-cache failure after the first attempted correction. Its lines are
preserved as retrieved; the full hosted job and Compose failure artifact remain
the primary evidence. See the workstream report for the corrected diagnosis and
the new fixture's still-pending hosted execution.

The new spec/config also passed:

```sh
node_modules/.bin/tsc --ignoreConfig --noEmit --strict --target ES2022 \
  --module NodeNext --moduleResolution NodeNext --skipLibCheck --types node \
  playwright.config.ts e2e/managed-backups/backup.spec.ts
```

Workflow and Compose YAML parsed successfully. No local Docker or browser
execution is claimed. The managed-backup test must execute against the shipped
image in hosted E2E; listing that test is not restore qualification. See the
[workstream report](../../final-frontend-sdk.md) for reviewed boundaries, changes,
independent review status and unreviewed areas.
