# Security hardening revalidation — 2026-09-08

PR [#13](https://github.com/webisnoteasy/PgBase/pull/13) was revalidated from
`a69ee1ec5a31eecb09c56859c843741bb01282a7` together with the latest main commit
`47210a126fe7e6bc2b5f7ef631345d2d5c702e74` (PR #20).

## Changes

- Reconciled schema and administrator UI conflicts while retaining both the
  hardening branch's admission, resource limits, and recovery safeguards and
  main's four audited fixes: post-DDL record-size validation/rollback, failed
  logout draft protection, paginated relation hydration, and CLI diagnostics.
- Fixed an additional draft-loss path: navigation to the already mounted
  resource, including browser-history URLs with equivalent percent encoding,
  used to clear the departure guard without unmounting the editor. Route
  identity is now checked before requesting departure. Three regression cases
  failed before the fix and passed afterward.
- Consolidated system-page request completion and catalog relation-index/error
  handling. A new regression confirms that a transport resolving after timeout
  cannot replace the failure state. Existing cancellation and catalog-integrity
  checks remain in place.
- Made best-effort cleanup of Windows password-input handles explicit, resolving
  three native Windows `errcheck` diagnostics without changing cleanup behavior.

No runtime budget was increased. The final measurements are 35,856 handwritten
runtime lines (limit 35,856), 16,745 data-group lines (limit 16,747), and 3,670 UI
runtime lines (limit 3,670). Export/interface/constructor budgets also pass.

## Verification

| Check | Result |
| --- | --- |
| Linux Go 1.26.8 unit suite, race detector and skip policy | Passed |
| PostgreSQL 17.9 full integration suite with race detector and integration vet | Passed |
| UI tests | 24 files, 403 tests passed |
| UI typecheck and rebuilt embedded distribution | Passed |
| Real Chromium onboarding and authenticated E2E | 8 tests passed, no retries or skips |
| Native Windows pinned golangci-lint v2.12.2 | Passed, 0 issues |
| Native Windows Go vet and CLI tests | Passed |
| Go vulnerability reachability scan | No reachable or imported-package findings |
| npm audit | 0 vulnerabilities |
| Git diff/format and runtime policy checks | Passed |

`govulncheck` also reports module-level advisory GO-2026-5932 for the unmaintained
`golang.org/x/crypto/openpgp` package. PgBase does not import that package; its
actual cryptographic imports and reachable symbols have no reported findings.

`actionlint` v1.7.12 does not understand the existing release workflow's
`concurrency.queue` key. All workflows pass after filtering only that diagnostic;
the retained `queue: max` setting is independently documented by
[GitHub](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency).
No workflow policy or release gate was relaxed during this revalidation.

## Environment and limits

The installed Docker Desktop fails at startup because its `sailor-ingest.sock`
cannot be opened. Validation therefore uses a disposable Ubuntu WSL instance
with verified Go 1.26.8 and PostgreSQL 17.9 downloads, a loopback-only test
database, and a source snapshot verified against the staged Git tree. The full
Docker image/backup/restore matrix and hosted release gates were not rerun.

GitHub-hosted jobs for the current main revision were blocked before execution
by the account's billing/spending-limit check. Local verification does not make
a new hosted check successful; the previous green PR checks belong to the
previous commit. No spending limit, paid runner, or billing setting was enabled.
