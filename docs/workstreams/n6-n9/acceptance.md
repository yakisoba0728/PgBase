# N6–N9 final local acceptance

Accepted product/UI source: `b7674f704e14b07104ebf6f1c9ee5e53092f367e`, on `codex/n6-n9-integration`. Later final commits contain evidence/status documentation only. Baseline is merged N1–N5 `2939f1543e4889c0ffa967eae398c91d0ecbb6ce`, with the installed-status record `117a1db` retained. This is local implementation and acceptance; no remote merge, live replacement or public distribution was performed.

## Feature acceptance

| Feature | Delivered contract | Evidence |
| --- | --- | --- |
| N6 JSVM | confined module snapshot, typed Go/JS records/files/mail/hooks/jobs/migrations, CLI/types/watch, cancellation/queue/Close and stable logical job occurrences | [N6](N6.md), [Nash review](N6-review.md), prior G3 external Go+JS/PG/SMTP/file and actual same-origin PublicDir evidence in [integration](integration.md) |
| N7 Dart SDK | exact numbers, auth/session isolation, CRUD/ETag/idempotency/file/SSE/admin contracts, external Dart and actual Flutter browser consumer | [N7](N7.md), [Newton review](N7-review.md), shared-state native/browser cross-flow accepted before G5 |
| N8 SQL console | opt-in three-role read/allowlisted transactional DML, fail-closed parser/catalog guards, exact bounded results, cancel/recovery, session-scoped UI, permanent compact write identity and 30-day heavy-history cleanup | [N8](N8.md), [Gauss review](N8-review.md), [13 actual browser scenarios and display closure](N8-browser.md) |
| N9 self-update | local signed/checksummed artifact selection, durable journal/swap/recovery, CLI and serve lock, consistent release metadata | [N9](N9.md), [Mill review](N9-review.md), native Darwin fixture and final six builds |

## G5 execution and final-delta accounting

The final result combines one broad baseline execution with the exact affected reruns below. Successful unchanged scopes were not repeated. Failed initial command exits remain in the logs; they are not rewritten as successful full-suite runs.

| Check | Source / observed result | Closure |
| --- | --- | --- |
| All-package no-DB unit/race + skip policy | `3f868f3`; initial failures were old structural limits, unpublished41 checksum and N8 PG-only files without integration build tags | all unrelated packages passed; migration/policy/SQLconsole affected JSON/race+skip rerun at `17ced7e` passed, and final SQLconsole/policy at `b7674f7` passed (1.342s/4.716s) |
| Full PostgreSQL18 + MinIO integration/race + skip policy | `d4fb6a2`, `scripts/test-go-integration.sh -race -count=1 -p 2`, GOMAXPROCS3, PG55472/S359007; 43 packages passed, 2 packages contained the specific fixture failures below | existing host Go1.27.0 ran this broad pass; all final changed-scope reruns and release artifacts use pinned Go1.26.8. PgBase 201.322s, query104.429s, SQLconsole10.366s, schema84.885s; no unapproved skip was reported |
| Watch integration capture race | `1f429e4`→`433a753`; concurrent test stdout/stderr `bytes.Buffer` capture synchronized | existing `^TestJavaScriptWatch` actual PG race passed2.494s; runtime/watch behavior unchanged |
| Release rewind migration41 fixture | `a31975f`; rewind to v6 now removes the final41 identity table as well as execution/receipt tables | exact `TestReleaseADirectConstructorRetriesRegistrationAfterMigrationM` PG race passed1.902s; no migration1–40 edit |
| N6 mechanical lint cleanup | `c4ebdeb`→`8c67424`; cleanup errors joined without losing primary errors; redundant private parameters removed | real CLI/JSVM/public-facade race passed3.818s/1.480s/2.069s; lead reviewed error and snapshot lifetime semantics |
| N8 owner final backend | `0e4ba42`/`752d38c` | full PG17 race8.526s, full PG18 6.982s, API/options race1.422s/1.466s; final migration41 PG17/18 .432s/.439s; duplicate8-way replay at poolcap4 and aged-write tests passed |
| N8 final terminal publication | `b7674f7`; durable Finish completes before terminal state is published; Get/Cancel/replay surface persistence failure | controlled row-lock regression failed before correction; exact five publication/failure/replay/retention/cancel tests passed PG18 race3.439s; Gauss reviewed only this concrete delta |
| Final static checks | `b7674f7` | pinned full lint0 issues; integration-tagged vet pass; no suppression or dependency change |
| Admin UI | initial full run reported one additive `sqlConsoleEnabled:false` expectation mismatch; corrected conformance92/92 pass | N8 adapter/page17 pass, narrow layout component14 pass; final tsc/Vite build and byte-identical dist regeneration pass. Current test collection contains513 cases; this collection count is not a second execution count |
| Actual SQL browser | real isolated API58096/UI58097/PG55470, base `e57d233`, final exact UI overlay `5235d9e` | 13 functional scenarios pass; only exact-read/clipboard/layout case repeated for the displayed table overflow. Harness CSRF spelling/async-wait corrections are recorded, not product failures |
| TypeScript SDK | isolated `17ced7e` | TS build/unit Node7/7, real HTTP/file/ETag/SSE/logout conformance2.292s, local tarball installed/imported in separate consumer; no publish |
| Dart/Flutter and N6 external cross | accepted G3 source; later N8 changes use separate optional routes | prior actual external consumer/cross/native/browser acceptance retained; no unchanged N7 rerun |
| OpenAPI/types/generated | focused shared API/options/OpenAPI checks pass; full Go suites include generated-type and route contracts | no final route/type change; final UI dist regenerated twice with zero Git diff |
| Structure/migration | final metrics: Go69,323 + UI6,667 =75,990; data-group22,693 unchanged; no production file >800 | [exact measured rationale](structural-budget.md), independent Gauss confirmation; migration1–40 byte-identical, only new41 checksum pinned |
| Six final binaries | exact `b7674f7`, Go1.26.8, CGO0, Linux/Darwin/Windows × amd64/arm64 | all six build/checksum checks pass; Darwin/arm64 `version --json` has version `n6n9-local`, exact commit and schema41..41; Windows CLI/selfupdate compilation passes on both architectures |

The final five-case PostgreSQL command is `go test -tags=integration -race ./internal/sqlconsole -run 'Test(ServicePublishesTerminalOnlyAfterLedgerFinish|ServiceSurfacesTerminalLedgerFailure|LedgerConcurrentReplaysReleaseAdmissionConnection|ServiceNeverReplaysExpiredWriteWithoutReceipt|ServicePersistsRejectReplayResultExpiryAndActualCancel)$' -count=1`, with pinned `GOTOOLCHAIN=go1.26.8`, GOMAXPROCS2 and the dedicated55472 admin fixture.

## Durable local artifacts

- Final copied logs/manifests: `.local/verification/n6-n9-g5/final-b7674f7/`, with `SHA256SUMS` for16 retained files. Initial full PG log and its nonzero exit remain under `.local/verification/n6-n9-g5/d4fb6a201194e01509e384abb49a469066fc32de/`; initial no-DB unit/vet/lint logs remain under `.local/verification/n6n9-g5/3f868f3021ac924beb6d0cb6b7edb6f706e799ec/`.
- Final binaries/checksums/native JSON: `/Users/yakisoba0728/.codex/worktrees/pgbase-artifacts-n6n9-b7674f7/.local/artifacts/n6n9-b7674f7`. Native Darwin/arm64 SHA256: `4064c03647231edba1cb12739e12c2f3759d49e07586fcb8deff667c5668936c`. Earlier `6e010f1` artifacts are superseded.
- Browser raw JSON, screenshots, protected launcher/credentials and exact provenance remain under `/Users/yakisoba0728/Documents/GitHub/PgBase-worktrees/n6-n9/n8-ui/.local/verification/n8-browser`; [browser record](N8-browser.md) maps them. No credential contents are committed.

## Resource and qualification boundaries

At08:45 KST, the six task-owned N7/N8/G5 containers were identified by exact Compose project/config labels and AutoRemove=false, then stopped without removal. Named/anonymous volumes, container data and browser evidence are preserved. N8 API/UI/browser processes were already stopped. Completed N6 AutoRemove PG removal on the earlier pause remains historical. No command targeted live8090/55440/59000, the installed binary or existing userdata.

Windows-native execution is unavailable and unclaimed; actual Windows helper code/compile and available Darwin-native tests satisfy the local boundary. JS code is trusted application code, with no hostile multitenant hard-heap sandbox claim. N8 supports its documented native expression/role/target subset; unsupported SQL fails closed. Compact write keys are retained indefinitely, so total key storage is not globally bounded. The additional browser restart/commit-unknown/real-time TTL exercise was not completed and is not claimed; deterministic backend and UI-unit evidence cover those mechanisms. No public release, npm/pub.dev publication, cloud deployment, remote merge or live update occurred.

## Post-acceptance local follow-up (2026-09-10)

The repository Git metadata was repaired before this follow-up: a backup was made at
`/tmp/pgbase-git-metadata-backup.ZF8RfK/git`, the final missing newline in
`packed-refs` was restored, and the missing local integration ref was recovered
only from the matching `logs/HEAD` and index transition. `git show-ref` and
`git fsck --full --connectivity-only` then passed. The recovered checkout is
`codex/n6-n9-integration` at `8d52c8d49f16ad491d4f0e821c94fef2324757d1`;
`origin/main` remains `2939f1543e4889c0ffa967eae398c91d0ecbb6ce`.

The previously excluded SQL-console browser restart/commit-unknown behaviour
now has a focused UI test: it retains the session-scoped write identity across
a browser reload, rechecks it, and does not submit it again. A matching
PostgreSQL integration test specifies two distinct outcomes: a committed write
whose commit response is lost creates a durable receipt and is reconciled by
the runner before it is published as `succeeded`; an already-durable unknown
outcome with no receipt remains `unknown` across a new service start. The
successful result also retains a non-expired result TTL after that restart. The no-DB SQL
console test package, focused UI suite (15 tests), and UI typecheck passed.
The new PostgreSQL test was not run because this host has no
`PGBASE_TEST_ADMIN_URL`; the Docker daemon is also unavailable, so no
disposable staging upgrade/rollback or PITR drill was claimed.

Six local CGO-free artifacts for the recovered source were built under
`.local/artifacts/n6n9-8d52c8d/` with `n6n9-local` version metadata and a
`SHA256SUMS` manifest. Native Darwin/arm64 `version --json` reports commit
`8d52c8d49f16ad491d4f0e821c94fef2324757d1` and schema range `41..41`.
Windows artifacts were cross-compiled only; native Windows replacement was not
executed. These are local release-preparation artifacts, not a public release,
remote merge, or live installation.
