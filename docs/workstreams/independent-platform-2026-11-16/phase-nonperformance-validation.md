# Nonperformance integrated qualification

## Current checkpoint

The selected Storage/schema 69 candidate, two preserved SDK/admin session fixes,
schema 68 backup compatibility repair and conservative OAuth-avatar special-use
address policy have author tests and independent review. Integrated hosted
qualification is pending; this checkpoint is not a pass claim.

## Explicit CI scope

`nonperformance-qualification.yml` calls dedicated nonperformance Backend and
E2E workflows, plus the existing frontend, security and whole-source CodeQL
workflows. Full PostgreSQL 16/17/18 compatibility and PostgreSQL 17 race shards,
Go unit/race/vet/static checks, SDK/live HTTP contracts, MinIO, backup recovery,
platform jobs and image/dependency evidence are retained.

The dedicated Backend jobs are structurally identical to existing Backend jobs
except for omission of the two benchmark recording/upload steps. The original
`backend.yml`, `e2e.yml`, `full-qualification.yml` and performance workflows are
unchanged. This is a separate functional execution path, not a successful
substitute for their unresolved performance qualification.

The dedicated E2E workflow runs the unchanged default Chromium 26 suite first,
then 12 representative Firefox/WebKit cases with
`PGBASE_BROWSER_SMOKE_ENGINES=firefox,webkit` and
`npm run test:e2e -- --project='*-smoke-*'`. Shared disposable fixtures run
serially; onboarding/authenticated/managed-backup origins remain separate.

## Evidence and authorship

- Storage implementation and schema 68 actual backup regression:
  `phase-nonperformance-storage.md`.
- Preserved SDK/admin fixes, before/after tests and build reproduction:
  `phase-nonperformance-sdk-ui.md`.
- Independent Storage/SDK/workflow review and authored security fix with a
  separate reviewer: `phase-nonperformance-security.md`.
- Explicit open/excluded scope: `phase-nonperformance-deferred.md`.

Final source SHA, merge SHA, tree equality, per-job outcomes and scanner artifact
classification will replace this pending checkpoint after the hosted run.

## Overall product decision

**Not Ready.** Historical sustained qualification failed with 547 dropped
arrivals; performance remains assigned to the user and is not run in this session.
