# Final local verification — remaining development goal

Baseline: `abc94ef` on `codex/remaining-development`, following the user's selected `origin/main` snapshot `6993744a8f36db48ddfeaa8dae017656752d24c7`. The implementation and acceptance boundaries are summarized in [README.md](README.md). All sub-agents for this goal used the requested GPT-5.6 Sol model; model output was reviewed and checked by executable tests.

## Executed checks

| Check | Observed result |
| --- | --- |
| Complete Go unit suite | `scripts/test-go-unit.sh -race -p 2 -count=1 -skip '^TestRuntimeStructuralBudgetsDoNotDrift$'` passed, including the skip-policy guard. Linux ran without `CAP_DAC_OVERRIDE`, preserving meaningful permission checks. The Git-dependent structural check ran separately on Windows as listed below. |
| Query integration | PostgreSQL 17.9, full `internal/query`, race detector and skip guard: 103.799s. |
| Schema integration | PostgreSQL 17.9, full `internal/schema`, race detector and skip guard: 80.322s. |
| File integration | PostgreSQL 17.9 and actual MinIO, full `internal/filestore`, race detector and skip guard: 6.064s. |
| HTTP API integration | PostgreSQL 17.9, full `internal/api`, race detector and skip guard: 26.266s. |
| App integration | PostgreSQL 17.9 and MinIO, full `pgbase`, race detector and skip guard: 205.259s. |
| Native Windows updater | Full self-update and CLI tests: 18.811s / 2.271s. Lifecycle, ACL and coordination selection repeated three times: 33.347s. Independent review and native selection: 12.480s. Native Windows race instrumentation was unavailable because CGO is disabled. |
| Native restore and upgrade | Final combined run with `restore upgrade` tags: 84.050s; PostgreSQL 16/17/18 restore matrix and populated historical schema 40 → 65 upgrade followed by old-version recovery both passed. |
| Multi-process failure | Final source run: 4.793s. Two real server PIDs, 32 unique concurrent writes, 12 same-key writes, one kill/restart, realtime resumption, cross-process revocation and 35 durable records. The earlier implementation acceptance also passed three consecutive runs. |
| Real SDK consumers | Final real HTTP TypeScript and Dart administrator acceptance passed in 25.60s with fresh Dart/Node dependency state, using isolated PostgreSQL and local backup storage. Includes successful native `pg_dump` creation, import receipt, administrator management, and byte-exact upload/download/list/delete. |
| SDK package checks | TypeScript build, strict source/generated consumer types and 39 Node entries passed. Dart 3.13.0 analysis and 61 tests passed; subsequent upload-hardening and compatibility-deprecation selections passed, and the new real HTTP Dart consumer passed analysis. |
| Static analysis | Pinned `golangci-lint v2.12.2`: zero issues; Linux `go vet ./...` and native Windows self-update/CLI vet passed. Changed/new Go files are formatted. Workflow YAML parsing, `git diff --check`, and the status-index checker passed. |
| Security dependency checks | Frontend `npm audit --audit-level=high`: zero vulnerabilities. Pinned `govulncheck v1.7.0`: zero reachable symbols or imported packages affected. It reports one unused module advisory, described below. |
| Source budgets | Exact all-file Windows structural and data-group tests passed in 0.885s using an isolated temporary Git index that includes new source files. |

The UI source and embedded assets did not change after the checkpoint. Its 624 unit tests and eight Chromium flows remain recorded in the [preceding validation](../independent-product-2026-09-22/continuation-validation.md); they are not presented as a fresh run in this slice.

## Review-driven corrections

Cross-review and real execution found defects beyond the initial feature tests. Regressions now cover scalar metadata filters after any-match normalization, presentation-only view imports with a dependent view, signed decimal range rejection, S3 response consistency and disappearance, custom object-store range fallback, all EXIF orientations, animated WebP first-frame bounds/alpha, interrupted backup receipts, and Windows owner/ACL and replacement-lock coordination. The view-specific import helpers were moved into the same package to retain the 800-line per-file limit; no validation logic was removed for size.

## Exact source accounting

`tools/repostats --compare abc94ef --pretty` used the existing classifier and a temporary index containing all new files:

| Measure | Final value |
| --- | ---: |
| Handwritten Go + UI runtime | 106,112 lines |
| Go runtime | 94,955 |
| UI runtime | 11,157 |
| Delta from checkpoint | +1,116 / −116, net +1,000 |
| Data-group runtime | 28,540 |
| Query runtime | 7,904 |
| Go interfaces / exports / constructors | 104 / 1,595 / 64 |
| UI direct page hooks / exports | 190 / 317 |
| Runtime files over 800 lines | 0 |

The data group grows by 71 lines: query +29 and schema +42, including the three-line helper-file overhead. The final completion audit also replaces one stale Windows-helper comment about an unavailable Darwin-host test environment with the current coordination contract; this accounts for one additional addition/deletion without changing runtime behavior or the total. SDKs, generated outputs, tests, and documents keep their existing classifications; the Go/UI number is not an all-language total. Ratchets are set to the measured values without unused growth allowance.

## External and operational limits

The user confirmed that no real previous installation, backup, or external provider configuration is available. Historical-source restore, local SMTP/OAuth fixtures, and MinIO are local evidence. They do not establish actual provider-account behavior, production load or recovery timing, an installed-project replacement, or release publication.

The SDK acceptance's synthetic uploaded archive proves binary transport only; its empty dump is not restored. Successful backup creation uses the real server and `pg_dump`, while restore behavior is checked separately by the populated native recovery drill and SDK wire fixtures. The live SDK acceptance covers local backup storage, not S3 backups or browser CORS.

The only module-level advisory reported by the local Go scan is `GO-2026-5932` for the unmaintained `golang.org/x/crypto/openpgp` package. PgBase neither imports nor calls that package; its required `x/crypto` module supplies other functionality. The scan reports no affected imported package or reachable symbol, and no advisory is suppressed.

The existing [scheduled GitHub security run](https://github.com/yakisoba0728/PgBase/actions/runs/35581319166) could not start because GitHub reported failed account payments or an exhausted spending limit. Local checks do not substitute for a successful remote run, and no billing settings, required checks, or branch protections were changed to bypass it.

The same restriction was confirmed on [draft PR #54](https://github.com/yakisoba0728/PgBase/pull/54) for the implementation commit `e956cc28682aa496da44d5cc1e06a422a489c7f4`: [Backend](https://github.com/yakisoba0728/PgBase/actions/runs/35735119298), [Frontend](https://github.com/yakisoba0728/PgBase/actions/runs/35735117510), and [Security](https://github.com/yakisoba0728/PgBase/actions/runs/35735117506) all terminated before starting their jobs. The PR remains unmerged. Successful remote validation and the real-environment acceptance requirements are incomplete.

Automatic command review rejected cleanup of one task-owned diagnostic fixture with the reason `blocked by policy`, including a narrowed literal-path cleanup attempt. The ignored `.local/remaining-goal/windows-update/diag/fixture.exe` was left in place. It is not tracked or included in build/release assets. Shared validation fixtures and unrelated installations/containers were not removed.
