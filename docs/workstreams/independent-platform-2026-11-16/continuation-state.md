# PgBase — final integrated qualification in progress

## Current checkpoint — candidate preparation

This entry supersedes the historical next-action instructions below. The user
has explicitly authorized the final full functional and performance round.
**No latest-source full-CI or 600-second performance result is claimed yet.**

- Starting PR #148 HEAD: `23b6c4090e453587325d9dfad8fec6d854f3bee2`.
- Existing accepted Auth, Jobs and SDK fixes remain intact.
- The final workflow now calls the existing full three-browser reusable suite.
  Assertions, timeouts, recovery checks and required criteria are unchanged.
- A bounded capacity test reproduced three available request slots in the old
  pool4 configuration. The schema listener now owns a dedicated connection,
  preserving initialized session security and the shared backup writer lock.
  This is an explicitly authorized capacity-policy separation, not proof of
  a leak or of the unique cause of the historical 547 dropped arrivals.
- Request pool remains4. Persistent test sessions are4 pooled requests +1 schema
  listener +1 existing backup gate +1 existing diagnostic observer =7, previously6.
- Combined local runtime/CI source before evidence-only commits:
  `ff5cd9ac3188923400d26297280faa87839b590d`, tree
  `1d30798382078731b9442e4a42aadb0a9d328a34`.
- Three real subagents are assigned functional CI, performance and independent
  QA in separate worktrees; maximum concurrent subagents3.
- Final gate remains200 RPS x600s,120000 arrivals, zero drops/unexpected errors/
  incomplete and end-to-end p99<=250ms, unchanged PostgreSQL durability.
- One final600s run is planned; a second requires a newly verified code correction
  and independent approval. No unchanged blind rerun or ABBA campaign is allowed.
- Main stays unchanged, PR Draft/unmerged. No production access, merge, deploy,
  release, tags, package publication or required-check weakening is authorized.

The evidence directory is `evidence/final-integrated/`. The coordinator will
record published source/tree identities, actual run/jobs, artifact hashes and
final two-axis verdict in `final-integrated-qualification.md` and
`final-performance-qualification.md` after the fixed-source execution.

---

## Historical checkpoints — retained with their original source scopes

# PgBase — functional correctness continuation

## Latest scoped outcome — three defects fixed and independently accepted

This section supersedes earlier next-action instructions preserved below.
**This bounded task is complete. Overall product remains Not Ready.**

- Live starting PR #148 HEAD: `55f539895f85ca3b7d8731ab8f4c3b8999ab92bb`.
- Fixed exactly three reproduced root causes: missing transport context in
  embedded record authorization, Jobs occurrence identity after rescheduling,
  and TypeScript/Dart iterator ownership across session changes.
- Independent reviewer verdict: **ACCEPT_SCOPED_CHANGES**. The reviewer separately
  reproduced the authorization failure against the original compiler and then
  restored the final source before GREEN checks.
- PostgreSQL 16.13 / 17.9 / 18.3: each **169 top-level + 129 subtests passed**.
  The same PG17.9 affected-package suite passed under **race**. Actual HTTP
  contracts: **5 + 40**; TypeScript **119/119**; Dart **138/138**; related Go vet
  and Dart analysis passed. All accepted executions have **0 failures / 0 skips**.
- Tested local source: `ecff2d1ff1ddca96211832908b1cfb6c40faf8e3`.
  Equivalent GitHub source: `5bdfc309d45808d56ef532ffad60dfa2acb48f28`.
  Exact common tree: `fd5f4c233f621e35badd9a5e401f5e090ddf310b`.
- Fix commits: authorization `2b44c141`, Jobs `e976b364`, SDK `67e5e314`.
  Independent evidence commit: `e49e835f376960c75e343239887f93a8645e9768`.
  The following final checkpoint changes documentation/provenance only.
- Actual subagents: **4 created, 4 completed, maximum 3 concurrent** (4 including
  coordinator). A/B/C and Independent QA used separate worktrees.
- All assertion failures and initial fixture/bootstrap failures remain retained
  and classified separately. PG16/18 initially lacked local `pg_trgm`; matching
  extensions repaired only the disposable environment. No failing evidence or
  existing test was removed or weakened.
- **No new full CI pass is claimed.** The earlier 14/14 run applies only to its
  prior source. No Firefox/browser analysis or run, performance investigation,
  benchmark/load test or pool/WAL study occurred. Workflows and required-check
  definitions are unchanged; publication uses `[skip ci]` to avoid excluded runs.
- Schema remains **69**; UI design/layout are unchanged. PR #148 remains
  **Open / Draft / Unmerged** and main remains
  `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`. No force push, main write, production
  access, deployment, release or package publication occurred.

Canonical new report: [`functional-qa-handoff.md`](functional-qa-handoff.md).
Durable evidence: [`evidence/functional-qa/`](evidence/functional-qa/).
The GitHub/local commit mapping and exact source manifests distinguish tested
runtime content from subsequent documentation commits.

### Remaining and next starting point

The selected defects have no outstanding reproduced failure within the scoped
verification. This is not whole-product or repository-wide certification.
Broader Storage/backup/Realtime and SDK/admin combinations remain outside this
pass. Existing external-provider, ambiguous Storage writes and external Jobs
side-effect idempotency/fencing limits remain documented.

**NP-BROWSER-01 remains unresolved and excluded.** The historical sustained HTTP
run lost **547 of 120,000 arrivals at 200 RPS / 600 seconds**; performance remains
unresolved and assigned to the user. These gates were not passed, deleted or
reclassified. Next work must first verify live PR HEAD and choose a bounded
remaining functional contract; do not resume Firefox or performance without a
new user instruction. This session stops after this handoff; no agent or test
continues running.

---

## Historical checkpoints — original source scopes, preserved verbatim

# PgBase — final nonperformance continuation

## Latest resumed outcome — CI completed, acceptance still Blocked

This section supersedes the historical checkpoint preserved below.

- [Nonperformance CI 43845969233](https://github.com/yakisoba0728/PgBase/actions/runs/43845969233) completed **14/14 successful jobs**, no unfinished jobs.
- Chromium **26/26**, Firefox **6/6**, WebKit **6/6**, hardened runtime and **30 black-box top-level executions** passed on candidate `527cc0a05f97ab24e089eb968cd70e92160ba973` / synthetic checkout `b48828a536b5b67436827e55ebdfd7e755dece52`, identical tree `9c0313270d4a508777a6d7a2a88ee5450729d487`.
- `527cc0a0` only repairs Playwright failure-artifact retention. It does not fix or prove the cause of the old font abort. All runtime, assertions, console/CSP/5xx checks, retries and timeouts are unchanged.
- `5206f30b9bf0a1813d4b70b627b677548f13e09b` integrates the prior documentation-only checkpoint. Subsequent report/evidence changes are documentation-only and are not another test execution.
- Two bounded diagnostics on immutable source `5206f30b` completed: fresh fixture [43847672228](https://github.com/yakisoba0728/PgBase/actions/runs/43847672228), then original Chromium prerequisite [43848675663](https://github.com/yakisoba0728/PgBase/actions/runs/43848675663). Both Firefox cases passed with retained traces. The first did not request subset 87; the second did, with HTTP 200 / 24,664 bytes and no console error. Neither reproduced the abort.
- **NP-BROWSER-01 remains unresolved:** navigation cancellation is still an inference, not proven historical causation. No speculative runtime fix or console exception was applied.
- **NP-QA-INDEPENDENCE remains Blocked:** no usable coding-subagent facility was available. Actual child agents created/completed/max-concurrent in this continuation: **0/0/0**. Coordinator inspection and CI are not independent QA.
- **Task acceptance: Blocked. Overall product: Not Ready.** No A-1/A-2 success or environment defect is claimed from non-reproduction. All authorized CI/diagnostic runs have finished; no automatic coding continuation is claimed.

Canonical current report: [`final-nonperformance-qa.md`](final-nonperformance-qa.md). Durable compact source/job/trace ledgers: [`evidence/final-nonperformance-handoff/`](evidence/final-nonperformance-handoff/). Raw Actions artifact URLs and November 30 retention expiry are recorded there; disposable auth values are excluded from Git evidence.

Next work is limited to an actual independent reviewer plus an evidence-backed reproduction of the historical request abort before a minimal fix. Do not repeat the existing diagnostics unchanged, restart a broad audit, or execute performance work. The isolated diagnostic workflow is preserved at `d055a48b8a40f4b2a2431f59618e427d105dd642` on `coordinator/firefox-trace-20261116-final`, not integrated into PR #148.

Schema 69, ambiguous-write tombstone retention, live-reference and legacy-worker protections, and writer-quiescence requirements for source-storage migration remain unchanged. Prior security classifications and deferred real-provider/accessibility coverage remain in force. The historical 200 RPS / 600-second test lost 547 of 120,000 requests; **performance remains the user's unresolved work**. Main remains `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`, PR #148 remains Draft/unmerged, and no production access, deployment, release/tag or package publication occurred.

---

## Historical checkpoint — retained verbatim; original source scope only

# PgBase — final nonperformance checkpoint

## Final outcome and stop

**Implementation integrated; nonperformance qualification Blocked (13/14 jobs).
Overall product Not Ready.** This session has ended. No agent or automatic
coding process continues after the report.

The user explicitly excluded performance work and required stopping repeated
same-defect investigation without a new evidence-backed hypothesis. Firefox's
strict-console font-abort failure reproduced on one unchanged-job retry.
No test/filter/timeout or required check was weakened to obtain success.

## Exact current source

- Draft PR #148, branch `refactor/independent-platform-20261116`.
- Tested PR HEAD: `56a881f47d0c27877b91959d7df641c2f603ef03`.
- Actual CI merge: `0a517baea58bf65e2293f15fb0a9fb54321f7eb7`.
- Both trees: `9b4a2e78540c3737f03f6fcc27398bfaa0424180`.
- Final run: [43836410887](https://github.com/yakisoba0728/PgBase/actions/runs/43836410887),
  13 successful jobs, one failed browser job, zero unfinished jobs.
- Main remains `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`.
- Final documentation/evidence checkpoint:
  `coordinator/nonperformance-completion-20261116`, documentation-only above
  the tested PR source. Use its immutable commit link from the PR description.

## Implemented and verified

- Durable pre-Put cleanup intent, schema 69, retained ambiguous writes,
  legacy-worker fencing and conservative migration/restore policies.
- Actual schema 68 dump/restore-before-migrate compatibility correction.
- TS/Dart delayed listener and admin Backup callback/session ownership.
- Conservative OAuth-avatar special-use address rejection with a separate reviewer.
- Obsolete Storage helper removal and doctor schema 69 fixture correction.
- Go/race/static/vet, PostgreSQL 16/17/18, isolated MinIO, live SDKs,
  admin builds, platforms, and fresh source/image security jobs passed.
- Three actual child agents performed separated author and independent review.

## Precise remaining nonperformance blocker

**NP-BROWSER-01**: both final-source browser attempts passed Chromium 26,
Firefox 5/6 and WebKit 6/6; the Firefox session case failed cleanConsole teardown
on a Pretendard font `NS_BINDING_ABORTED` message. Its functional login/reload/
signout assertions completed. Cause is not trace-proven because the failing
Playwright trace was not uploaded. Current-source hardened-runtime/black-box
steps were not executed after the failure; old 30523dda results remain historical.

If explicitly resumed, first retain a trace and request/console evidence for
that one case, determine the actual cause and make a justified narrow correction.
Do not run another broad audit, blindly ignore console errors or restart performance.

## Canonical reports

- `phase-nonperformance-validation.md`: decisions, exact jobs and attempt history.
- `phase-nonperformance-storage.md` + `phase-nonperformance-storage-ci.md`.
- `phase-nonperformance-sdk-ui.md` + `phase-nonperformance-sdk-ui-ci.md`.
- `phase-nonperformance-security.md` + `phase-nonperformance-security-ci.md`.
- `phase-nonperformance-deferred.md`: Open/Blocked/Deferred ownership and next actions.
- `evidence/phase-nonperformance-coordinator/`: publication mapping and job ledger.

## Performance remains the user's work

성능 검증은 사용자가 직접 처리할 예정이므로 미완료로 남겨둠.
전체 릴리스 판정 **Not Ready** 유지.
The historical 200 RPS/600s gate dropped 547 of 120,000 arrivals. No new performance
measurement or pass exists. Original performance workflows/criteria/evidence and
required-check settings were not modified. PR remains Draft and unmerged.
