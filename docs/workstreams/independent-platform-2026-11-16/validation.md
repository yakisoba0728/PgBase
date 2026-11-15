# Implementation and verification handoff — 2026-11-16

PR [#148](https://github.com/yakisoba0728/PgBase/pull/148) contains an implemented, tested hardening batch. It remains a draft because the owner's broader whole-platform redesign and independent production qualification are **not complete**. No main merge, production access/deployment, database reset, tag, SDK registry publication or release promotion was performed.

## Exact revision boundaries

- Baseline: `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`, schema 67.
- Final runtime/test candidate for full manual validation: `f09f9d6ae01ddcb4339afbbda648515db7e724e0`.
- `cf50eed36e7d2f15915ade9f66a156faf34871ab` adds only the source-security workflow. Runtime, dependencies, SDKs, administrator source/dist and behavioral tests are unchanged from `f09f9d6a`.
- Routine PR checks associated with `cf50eed3` checked synthetic merge `1358b1b161912c29ee9c23d3f8d9db981b5fe1eb` against baseline main. That is a GitHub test merge, **not a merge into main**.
- The handoff cleanup changes only documents and workstream workflows: remove temporary launch/diagnostic/history helpers and make source-security explicitly manual. It does not change product or test code. The full runtime results below therefore apply to identical runtime inputs, not to a falsely claimed full rerun of the cleanup commit.

## Completed verification

| Check | Revision / run | Observed result |
| --- | --- | --- |
| Full Backend | `f09f9d6a`, [43751405236](https://github.com/yakisoba0728/PgBase/actions/runs/43751405236) | Go unit/race, repository/static checks, PostgreSQL 16.13 and 18.3 integration, PostgreSQL 17.9 core/application race shards, bounded multiprocess durability, Dart analyze/tests, TypeScript SDK and browser example, administrator SDKs against a real server, native Windows self-update and macOS terminal tests, release-phase artifact tests passed |
| Full Security | `f09f9d6a`, [43751408164](https://github.com/yakisoba0728/PgBase/actions/runs/43751408164) | Dependency/security audit and runtime image scan jobs passed |
| Full E2E | `f09f9d6a`, [43751410238](https://github.com/yakisoba0728/PgBase/actions/runs/43751410238) | Browser onboarding/authenticated workflows, hardened runtime, black-box tests including application/database crash recovery passed |
| Routine Backend | `cf50eed3` PR, [43751630182](https://github.com/yakisoba0728/PgBase/actions/runs/43751630182) | All required jobs passed |
| Frontend | `cf50eed3` PR, [43751630180](https://github.com/yakisoba0728/PgBase/actions/runs/43751630180) | Typecheck, **76 files / 422 tests**, clean build and exact committed-dist reproduction passed |
| Routine Security | `cf50eed3` PR, [43751630222](https://github.com/yakisoba0728/PgBase/actions/runs/43751630222) | Passed |
| History-drift regression | `cf50eed3`, [43751630186](https://github.com/yakisoba0728/PgBase/actions/runs/43751630186) | 30 race repetitions of the error classifier and 30 race/integration repetitions of the intentionally damaged-history fixture passed |
| CodeQL extended Go | `cf50eed3`, [43751625628](https://github.com/yakisoba0728/PgBase/actions/runs/43751625628), job `132926640587` | **Corrected:** 37 queries loaded; 27 SARIF results (15 error / 12 warning), requiring classification |
| CodeQL extended JavaScript/TypeScript | Same run, job `132926640381` | **Corrected:** 105 queries loaded; 3 SARIF results (2 error / 1 warning), requiring classification |

The full Backend job `132925952438` additionally completed the final benchmark-baseline recording/upload after its integration and SDK checks. S3 and database fixtures were isolated CI resources. Successful release-phase artifact *tests* do not mean a release was published.

**Evidence correction (independent recheck, 2026-11-16):** the previous zero-finding claim, query counts, extraction count and artifact identifiers in this document were incorrect. Both job logs explicitly print 27 Go and 3 JavaScript/TypeScript findings. The successful workflow only meant that analysis/artifact creation completed; its reporting step did not gate on findings. No evidence establishes why the earlier report substituted different numbers.

CodeQL used action v4.39.3 at pinned commit `33d7b85da1a04171e211befe2c739303b9a4c066` and CLI 2.27.4. Independently downloaded artifacts match the GitHub API digests:

| Language | Actual artifact | ZIP SHA-256 |
| --- | --- | --- |
| Go | `14176194587` | `d325c391955a94a1646d3055046e40d277738f0c9b433be48c083054c89a5092` |
| JavaScript/TypeScript | `14176811964` | `a77a9ff60abfd21cc128dfdae50fb208b36116e4a553a7c0d594e7b0b10a188a` |

The query logs end at `[37/37] Loaded` for Go and `[105/105] Loaded` for JavaScript. SARIF has 35/103 rule descriptors (including 1/2 metric rules); diagnostic descriptors explain the distinct counts. SARIF successfully-extracted-file notifications identify 483 Go and 323 JavaScript/TypeScript file URIs. These are exact source-specific observations, not interchangeable counts of security checks.

The old workflow looked up descriptors only in `tool.driver.rules`, which is empty in both artifacts. CodeQL stores these descriptors in `tool.extensions` and references them through `result.rule.toolComponent`; consequently all old `findings.json` severity fields were null. The new tested parser resolves the actual component, preserves every result and fingerprint, rejects incomplete analysis/unresolvable metadata, and records counts with file digests and source/run binding in `summary.json`. This parser defect explains the missing severity metadata, **not** the inaccurate zero-result prose. Static findings still require reachability and trust-boundary review; they are not automatically proven exploits.

## Reproductions and failures were retained

1. **Compression:** new negotiation cases reproduced 11 failures out of the first 18 cases on the unchanged implementation. The correction has 22 cases and a fuzz invariant. A completed 20-second CI fuzz run executed 110087 inputs without a failure. An earlier locally interrupted attempt was not counted as a completed fuzz run.
2. **Quota database error:** a deliberate CHECK violation on a readable existing quota row was misreported as exhaustion on baseline. The corrected service propagates the PostgreSQL error, remains fail-closed, and resumes admission after the injected constraint is removed. Five integration/race repetitions passed.
3. **Backup fixture:** a full run exposed a three-second polling assumption. The test now waits on actual operation drain, owns shutdown immediately, and closes its archive handle before retention deletion. Serialization, persistence, restart and retention assertions remain intact. This is a fixture fix, not a claim of repaired product data loss.
4. **Crash E2E:** cleanup addressed pre-crash container IDs after Compose replaced those containers. The test now forces replacement, verifies the IDs changed, resolves current IDs for cleanup and still asserts SIGKILL status, committed data survival, rollback of uncommitted data and idempotency replay.
5. **SDK example:** the newly split `auth-cookie.js` was missing from the example's closed module allowlist. Existing import-traversal tests caught the 404. The module was explicitly added; arbitrary file serving was not enabled.
6. **History drift fixture:** after deliberately deleting migration history, the background writer may also observe that exact invalid state. Cleanup now accepts only that injected error and rejects unrelated wrapped/joined errors. Runtime shutdown errors have not been suppressed.
7. **Load fixture:** the first 500000-row attempt lacked creation ordinals on directly seeded extra records. It failed before measurement. The corrected disposable fixture populates required sidecars and verifies physical, ordinal and native visible counts before timing.

Earlier UsersView/ApiView tests intermittently exceeded their existing assertion deadlines. Five focused repetitions and later full suites passed, but their initial timing cause was not conclusively established; they were not declared fixed by increasing deadlines. Final frontend logs still contain non-fatal React `act(...)` warnings and a jsdom navigation warning. These are remaining test-quality work, not clean-console evidence.

## Performance evidence

[performance.md](performance.md) records environments, raw samples, run IDs, zero-error counts and limitations. The quota and metadata-heavy WebP improvements are paired component measurements. Small WebP latency did not improve. The 500000-row query profile completed 12000 measured operations without errors but excludes HTTP, TLS/CDN, serialization, sustained mixed writes and external service failures. It is not a general production throughput claim. Page hydration allocates about 1.12MB per 50-row page in that profile and merits attribution before further optimization.

## Broader plan: what is and is not finished

| Planned phase | Actual status |
| --- | --- |
| Whole-codebase audit | Targeted manual inspection plus Go/JS/TS static analysis completed; exhaustive module-by-module independent audit not completed |
| Architecture redesign | Explicit lifetime, immutable-policy and bounded-decoder contracts implemented; wholesale replacement of all backend modules not performed |
| Verification foundation | Existing gates preserved; deterministic regressions, paired benchmarks, concurrent-query profile and source analysis added |
| Backend refactoring | Compression, quota policy, image pipeline and driver hardening implemented; not every backend module rewritten |
| Performance/security/scaling | Component improvements and bounded multi-instance/integration checks verified; sustained end-to-end service qualification still open |
| Frontend improvement | Session/request/upload correctness and terminology improved, visual design retained; complete UX/accessibility/browser audit still open |
| SDK/developer experience/identity | Cookie contract, TS/Dart parity, browser example, README and UI terminology implemented; About metadata and legacy-adapter retirement remain separate work |
| Integration/independent review | Full automated suites passed on the runtime candidate; no completed independent AI reviewer response observed |
| Release preparation | Source branch, draft PR, test evidence and breaking-change guidance delivered; no release-ready declaration or release publication |

No separate coding-agent spawn facility was available in this session. Parallel CI jobs are actual parallel execution, **not** independent AI agents. The GitHub `@codex review` request is not counted as a completed review. The local full toolchain could not be initialized after environment failures; repository execution claims above are backed by CI, not by pretending downloaded tools were run locally.

## Operational handoff

The SDK's default SSR cookie name is now `pgbase_auth`; old names require explicit import/export options. See [cookie contract](../../sdk-auth-cookies.md), including HTTPS Secure configuration and untrusted cached identity. The schema remains 67. Production deployment remains the owner's responsibility and is outside this change.

Temporary source/tool transport, automatic generated-source writers, workflow launchers and branch-specific diagnostic jobs have been removed. `query-load.yml` and `source-security.yml` remain explicit read-only manual qualification workflows. Their evidence-producing payloads were exercised; the final manual-only trigger cleanup is not itself a new analysis run. Original Backend/Frontend/Security/E2E definitions remain intact.

Before treating PgBase as unrestricted large-service infrastructure: establish application-specific SLOs and threat model; run sustained mixed HTTP load, real relation graphs and realtime fan-out under slow clients/resource pressure; assess storage/network failures and recovery timings; complete independent security and broad browser/accessibility review. Preserve the distinction between these open qualifications and the concrete fixes already delivered.
