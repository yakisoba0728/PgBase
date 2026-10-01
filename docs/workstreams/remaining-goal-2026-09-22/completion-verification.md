# Completion verification — 2026-09-22

## Completion scope

The user resumed this goal and explicitly excluded actual prior installations/backups and external SMTP, OAuth, and cloud S3 accounts. These unavailable environments are not completion blockers. Local protocol fixtures, MinIO, populated historical-source recovery, and failure acceptance remain in scope. Public package/image publication and production deployment are separate activities.

The verification started from implementation head `9b1a6c373267a8a1ec2c0eaf9dd1a81b813e9d1e` on `codex/remaining-development`. An independent review found no unfinished feature implementation within the agreed query, files/images, schema import, SDK, Windows update, and recovery scope. The final browser and operational pass found and corrected an E2E fixture omission and a browser-example module-serving defect. All locally executable acceptance in the revised scope passed. The finishing changes are limited to that example, test fixtures, CI coverage, and verification records; the Go server and embedded administrator UI remain unchanged.

## Executed verification

| Check | Observed result |
| --- | --- |
| Go unit and race checks | `scripts/test-go-unit.sh -race -p 2 -count=1 -skip '^TestRuntimeStructuralBudgetsDoNotDrift$'` passed across the complete package graph with the skip-policy guard. The Git-dependent structural test ran separately on native Windows; Linux dropped `CAP_DAC_OVERRIDE` for permission checks. |
| PostgreSQL integration | Every package passed with `integration`, race detection, PostgreSQL 17.9, and MinIO after correcting the local fixture endpoint. The initial complete-package run passed everywhere except `internal/filestore` and `pgbase`; complete corrected reruns of those packages passed in 11.219s and 199.068s. The skip-policy guard remained enabled. |
| Native Windows updater and CLI | Both complete packages passed: 16.659s and 2.284s. Native vet passed. |
| Populated restore and upgrade | PostgreSQL 16/17/18 restore matrix passed in 44.42s. Historical schema 40 → 65 upgrade, current-service acceptance, and restored old-version recovery passed in 27.51s; combined command wall time 73.547s. |
| Structural/source budgets | Native structural and data-group checks passed in 0.837s. The finishing changes do not alter Go/UI runtime or its budgets; the browser example JavaScript keeps its existing separate classification. |
| Frontend | Typecheck, all 50 files / 624 unit tests, and production build passed. A clean embedded build reproduced the committed output byte-for-byte, with no extra untracked assets. |
| TypeScript SDK | Build, strict source/generated consumer types, conformance, and all 39 Node test entries passed. Generated output reproduced exactly. |
| Dart SDK | Dart 3.13.0 analysis found no issues; all 61 tests passed. |
| Actual SDK consumers | Real administrator HTTP acceptance passed in 20.93s for TypeScript and Dart, including native backup creation and exact binary transport. Public-record SDK conformance passed in 4.28s. |
| Multi-process failure | Passed in 5.15s: two server processes, 32 unique writes, 12 same-key attempts, 35 durable records, kill/restart, realtime resumption, and cross-process revocation. |
| Browser workflows | The fresh standard onboarding/authenticated suite passed all 27 applicable cases in 39.9s after the file-backend fixture correction. All four opt-in cases passed separately with local MinIO and a disposable managed server: managed archive create/upload/download/delete, archive restore activation, file migration local → S3 → local, and independent S3 backup storage. The separate local project passed all four cases in 6.4s after the example correction: 35 distinct browser cases covered across the appropriate fixtures. |
| Notes example regression | Both Node test entries passed, including recursive HTTP traversal of the SDK's relative ES-module graph, a 10-second bound, an ephemeral test port, and rejection of unintended paths. CI now runs these tests after building the SDK for current-source validation. |
| Docker black-box E2E | `go test -tags=e2e ./tests/e2e -v -count=2 -shuffle=on` passed in 94.335s. Compose shutdown/interpolation and tagged rendering checks also passed. |
| Historical process rollout | Both pinned historical release phases were built from a verified complete Git bundle. `go test -race -tags='integration releaseartifacts' ./tests/release -count=1 -v` passed in 36.883s, including real membership and schema-compiler process rollout/rollback. |
| Named-point WAL recovery | Disposable PostgreSQL 17.9 PITR passed: markers 1 and 2 restored, later marker 3 absent, promotion complete, observed container recovery 2s. This is local fixture evidence. |
| Six-platform artifacts | CGO-disabled Linux/macOS/Windows × amd64/arm64 builds passed; all six SHA-256 checks passed, and the native Windows artifact reported the expected build metadata. |
| Workflow/repository policy | After the E2E fixture change, Linux release-policy and repository-policy packages passed in 15.505s and 3.464s. Nine workflow/fixture YAML files parsed successfully; whitespace and the status index checks passed. |
| Self-hosted PR checks | All five applicable jobs passed on the existing Hyper-V repository runners for implementation head `9b1a6c3`. Together they covered observability, Go unit/static analysis/private Cosign attestation, PostgreSQL integration/SDK conformance, frontend, and dependency security. The finishing fixture/example/documentation commit uses the same checks, plus the new example regression; the current head's check receipts are retained on PR #54. |

Detailed command output is retained locally under `.local/final-verification-2026-09-22/`, partitioned into `backend`, `native`, `ui-sdk`, `e2e`, `managed-browser`, and `release-ops`. Local reports preserve initial failures as well as the corrected results; failed attempts are not counted as passes.

## Fixture corrections found by the final pass

The reused Linux runner initially pointed S3 tests at `http://minio:9000`. Storage settings intentionally allow insecure HTTP only for a loopback IP when explicitly enabled. The focused storage-controller test reproduced `invalid file`; routing the same MinIO instance through `http://127.0.0.1:59000` made it pass. The entire file-store and app integration/race packages then passed in 11.219s and 199.068s. The initial full-run failures were limited to three file-store and four app cases using that endpoint. The correction retains the production validation rule; all other packages had already passed the full run.

The browser required-file test exercised multipart record creation but the generic deployment Compose file did not configure a file backend. The API correctly rejected the request because files were unavailable. A browser-only Compose override enables a local file directory on the existing writable temporary filesystem; production deployment defaults stay unchanged. The existing browser case is the executable regression for that fixture correction.

The optional notes example exposed a separate code defect: its server offered the SDK entry as `/sdk.js`, but the entry's relative ES-module imports resolved to unserved root paths. The finishing change serves the allowed built SDK modules under `/sdk/`, updates the example import, and adds a real HTTP module-graph regression. The local browser fixture also selects its configured backend URL in the example form rather than assuming port 8090.

The managed file-migration fixture needed the explicit insecure-loopback S3 opt-in as well; enabling it only on the disposable server made the existing browser case pass. The distroless application image lacks the PostgreSQL client tools needed by managed backup tests, so those four cases ran on the separate managed fixture with matching tools. Playwright's pinned browser download timed out; the browser evidence uses the already-installed Chromium v1234 through an isolated local configuration. A reused example fixture timed out during service-token cleanup after its assertions; a freshly recreated project passed all four local cases, and no product fix is claimed for that timeout. Release shell-policy tests run on Linux; a native Windows invocation could not execute the Bash scripts, and the subsequent Linux run passed.

The first PITR command under Git Bash failed because MSYS rewrote a Linux container path. Running it with `MSYS_NO_PATHCONV=1` preserved the intended path and completed the drill. This changed the command environment, not recovery behavior.

## Runner procedure and boundaries

The repository already has online `hyperv-pgbase-01`, `hyperv-pgbase-02`, and `hyperv-pgbase-03` Linux/Docker runners. Existing workflow routing uses `PGBASE_CI_RUNNER=hyperv` only for an authorized internal PR head or a main push. The previous hosted-run failure occurred because `PGBASE_CI_TRUSTED_SHA` still named an older commit. Review and confirm each new internal PR head, update that variable to its full SHA, then run the applicable checks. Fork PRs and release/manual workflows keep their existing routing.

The initial successful self-hosted results are attempt 2 of [Backend](https://github.com/yakisoba0728/PgBase/actions/runs/35735771795), [Frontend](https://github.com/yakisoba0728/PgBase/actions/runs/35735771770), and [Security](https://github.com/yakisoba0728/PgBase/actions/runs/35735771825). [PR #54](https://github.com/yakisoba0728/PgBase/pull/54) links the current head and check results. Billing configuration and workflow requirements were not bypassed.

Cross-compilation does not claim native macOS execution. Native Windows race instrumentation was unavailable with its CGO-disabled toolchain; Linux checks use the race detector. Windows recovery fixtures terminate the process and do not certify graceful Windows signal handling. Local MinIO/mail/OAuth and disposable recovery results do not claim external-account or production-load certification.

Task-owned verification services were stopped or removed after their checks. Unrelated services and the existing installation were preserved. The previously policy-blocked diagnostic executable remains ignored, as recorded in the implementation verification; no further deletion attempt was made.
