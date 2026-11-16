# Final integrated qualification: functional orchestration

Initial inspection: 2026-11-16T11:44:47Z. This is a plan and source inventory,
not a successful test execution. Agent A owns this evidence directory.

## Verified starting state

- Repository: `yakisoba0728/PgBase`; PR #148 is open, Draft, unmerged.
- PR HEAD: `23b6c4090e453587325d9dfad8fec6d854f3bee2`.
- HEAD tree: `a4ef35408feca0e51dcbdc26d0cc16b49b07f4b4`.
- Base main: `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`.
- GitHub-reported synthetic merge: `5f913c7cdd8287cdee2b47cb61df51e517ca5a62`.
  This is metadata, not a claim that CI checked out or tested it.
- PR labels: `performance-qualification`, `final-performance-qualification`,
  `nonperformance-qualification`; no `full-qualification` label.
- The commit-workflow-runs connector returned no PR-triggered runs for HEAD.
  Historical green runs are not evidence for this source.

## Workflow coverage

The full qualification expands to 14 nonperformance jobs and one mixed/load job:

| Jobs | Required coverage |
| --- | --- |
| Go unit | Pinned static analysis, formatting, vet, entire unit graph with race and skip policy, private attestation integration |
| Four PostgreSQL jobs | PG16.13; PG17.9 core and application shards with race; PG18.3; schema/Storage/backup/restore/Auth/Jobs/Realtime integrations, isolated MinIO, SDK and administrator actual HTTP contracts |
| Frontend | Security examples, typecheck, complete tests, clean embedded build equivalence |
| Browser and recovery | Chromium, Firefox and WebKit, nonroot/shell-less runtime, black-box scenarios twice with shuffle |
| Security | Reachable Go vulnerabilities, pinned npm audit, production image build/SBOM/runtime assets, all-severity SARIF and unchanged HIGH/CRITICAL gate |
| Two CodeQL jobs | Go and JavaScript/TypeScript whole configured source, security-extended, raw SARIF and resolved metadata |
| macOS / Windows | Native terminal cancellation and Windows self-update/CLI contracts |
| Observability / release-phase artifacts | Dashboard/PromQL/metric contract and immutable artifact validation; no package publication |
| Mixed/load | Existing mixed HTTP check, then exactly one 200 RPS × 600-second open-loop execution; raw scheduled-arrival evidence |

The existing full backend additionally records its existing short query and
observability microbenchmarks once on PG16/17 core/18. These are not additional
600-second arrival tests. No repeated ABBA campaign is planned.

## Discovered qualification wiring gap

At the starting HEAD, `full-qualification.yml` calls `e2e.yml`, which installs
and runs only Chromium. `nonperformance-e2e.yml` preserves those same Chromium,
hardened-runtime and black-box commands and additionally installs/runs the
existing Firefox/WebKit smoke projects and retains Playwright failure traces.
The minimal proposed correction is to route the full qualification's browser
job to that existing broader reusable workflow. No assertion, filtering,
retry, timeout, or test selection is weakened.

## Coordinated trigger and source freeze

1. Integrate only independently accepted narrow changes and record candidate
   SHA/tree. Keep the `full-qualification` label absent while preparing the
   final candidate, so unfinished sources do not consume the final load attempt.
2. Remove the old `nonperformance-qualification` opt-in label before publishing
   the final candidate, then publish with a normal commit message without a
   skip-CI marker. No workflow listens to the `unlabeled` event. The subsequent
   `synchronize` event starts only routine Backend/Frontend/Security jobs: both
   qualification opt-in labels are absent. This avoids reliance on skip-CI
   behavior for a later labeled event.
3. Keep the old nonperformance label absent before the single
   full trigger. It would otherwise run duplicate complete functional jobs and
   collide with the shared `source-security-${github.ref}` concurrency group.
   The full run retains every required functional job and the broader browser
   workflow; no required-check configuration is changed.
4. Add `full-qualification` exactly once through the GitHub connector. Existing
   `performance-qualification` and `final-performance-qualification` labels must
   not be removed/re-added: their explicit labeled events run repeated ABBA.
   The repeated workflow listens only to `labeled`, and its job checks the exact
   newly added label, so a `full-qualification` event skips its compare job.
5. Hold PR runtime HEAD fixed throughout the complete run. Record run/job IDs,
   actual checkout SHA from logs, checkout tree and comparison to the candidate.
6. Preserve logs/artifact URLs and SHA-256 digests. Scanner execution success is
   not a claim of zero findings. Diagnose only actual failing gates.
7. Do not use a blind workflow rerun. A second final load run requires newly
   validated code plus independent QA approval under the user's bounded policy.

The `gh` CLI is present but has no authenticated host. Available connectors
support the native PR-label trigger, run/job reads, logs, and artifact downloads;
there is no exposed workflow-dispatch tool. All remote mutations remain under
coordinator ownership.
