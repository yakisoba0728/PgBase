# N8 actual browser acceptance — 2026-09-10

The 13 agreed browser scenarios passed against the real isolated API and PostgreSQL fixture. No product implementation or static/unit checks were repeated. Lead accepted this browser scope and explicitly deferred additional restart/commit-unknown UI acceptance to the completed backend evidence.

## Exact source and retained fixture

- Browser source: `e57d233674f46120e5fa01fc3446fbf7a4e60ea5`, an immutable git archive of the lead's composed integration candidate.
- Archive: `sources/e57d233674f46120e5fa01fc3446fbf7a4e60ea5` beneath this directory. `candidate.json` records the full absolute path.
- UI owner branch remains clean at `cd092fb41859812dc333774d4522586f22642757`; its existing 17 focused tests were not rerun.
- Lead subsequently integrated backend `752d38c` (release control connection before replay lookup; remove dead compiler pool). Lead confirmed the browser evidence remains valid because that delta changes no UI/API/schema/grants and has its own synchronized backend regression evidence.
- PostgreSQL 17 on owner-coordinated port 55470; only database `n8_browser_ui_20260910`, roles `n8_browser_ui_compiler`, `n8_browser_ui_read`, `n8_browser_ui_writer`, and schema `n8_browser_ext` were provisioned for this browser task.
- Fresh candidate migration 41, two real administrators, allowlisted `n8_browser_ext.items(id,amount,label)`, and an unlisted table. Restricted writer has only the fixed receipt INSERT columns; accepted-write identity table remains control-only.
- Actual API was `127.0.0.1:58096`; actual UI and browser origin were `http://127.0.0.1:58097/admin/sql`. The Vite proxy explicitly used 58096.
- Protected credentials: `credentials.json`, mode 0600 inside this mode-0700 directory. Credentials were never printed or committed.
- API/UI/browser processes are stopped. Ports 58096/58097 were verified to have no listeners. Shared PG containers and other databases were preserved; live 8090/55440/59000 were untouched.

## Completed browser evidence

Actual headless Chromium, real login/cookie/CSRF composition, 1440×1050 viewport, real PostgreSQL statements and database assertions. Network-loss cases aborted real requests/responses; no success, result, or history payload was fabricated.

| Scenario | Observed result |
| --- | --- |
| Read default, caps, exact values and clipboard | Read-only initial state; one statement; omitted timeout uses advertised 1500 ms default/2000 ms maximum. Bigint, decimal scale, JSON large integer, NULL, empty string display and copy exactly. |
| Typed parameter | Decimal `9007199254740993.2300` stays exact through editor, request and returned cell. |
| Admin CSRF | Missing CSRF returns HTTP 403 with canonical `error.code=csrf_rejected`. Normal UI writes succeed with the composed session transport. |
| Allowlisted transactional write | Two statements commit once; database amount becomes `3.7500`; both result tabs exist. |
| Dry run | Both statements return results; inserted row count remains zero after rollback. |
| Later statement failure | Duplicate-key failure in statement two rolls back statement one; row count zero. |
| Forbidden targets | Managed administrator and unlisted extension writes fail; administrators unchanged; next read succeeds. |
| Cancel and poll | Running read cancel returns 202, UI reaches Canceled; subsequent read succeeds. |
| Durable history and Inspect | Reload lists durable entries without statement data; Inspect performs real GET and restores current-session exact rows. |
| Accepted response loss | After a real submitted write response is dropped, reload and Recheck recover via GET; one POST and one database row. |
| Loss before admission | GET 404 leads to replay with identical operation ID, idempotency key and body; one committed row. |
| Session identity | A→B logout/login and B session expiry produce different scopes; old unresolved attempt is not replayed, old ID GET returns 404, pending row stays absent. |
| Result cap | 1001-row result is rejected; next bounded read succeeds. |

Raw evidence is retained unchanged:

- `evidence/2026-09-09T23-28-07.018Z/browser-evidence.json`: initial run, nine completed passes plus four affected harness failures.
- `evidence/2026-09-09T23-29-28.368Z/browser-evidence.json`: focused rerun of only those four cases, all passed with no runtime errors.
- `evidence/accepted-browser-evidence.json`: consolidated 13 outcomes, each linked to its raw run.
- Screenshots: initial run `01-exact-read.png`, `02-transaction.png`, `03-canceled.png`, `04-reconciled.png`; focused run `05-session-isolation.png`. Initial failure screenshots are also preserved.

Initial harness corrections were limited to the task-local driver: expect the real `csrf_rejected` code, wait for the replay POST and matching execution ID before checking completion, and isolate rerun item IDs. The premature replay assertion affected the following session/cap scenarios. No product fix was required for these failures; no whole-browser suite was rerun.

## Findings and limits (08:36 update)

No functional product defect was observed in the 13 accepted scenarios. Screenshot inspection does show a presentation issue: the five-column exact-result table extends beyond the main card boundary at a 1440 px viewport, and adjacent type labels have little separation from names/values (`01-exact-read.png`). This did not prevent the exact display/copy assertions. Closed by UI `5235d9e` / integration `882a519`: existing `table-scroll` plus inline whitespace. Focused component14/14 and only the actual exact-read/clipboard case passed at1440px. The result viewport stays inside the shell and scrolls horizontally. Final full-composition typecheck/Vite build passes; bundle is `index-CTAdrZsH.js`.

Additional restart/commit-unknown UI work is **not accepted evidence**. A supplemental restart driver had reached its handoff marker after one ordinary committed fixture write when the lead directed scope closure. It was interrupted before any API restart and is excluded from the 13 outcomes. `restart-requested.json` records that fixture-only operation. No claim is made here for forced commit-unknown rendering, post-restart missing-result rendering, or wall-clock TTL expiry; lead explicitly relies on closed backend unknown/recovery evidence for that remaining mechanism.

## Resume point

No browser work remains in the lead-agreed acceptance scope. Lead owns final integration acceptance and common documentation. If the user later requests additional browser coverage, use the retained launcher/configuration and obtain the new exact source SHA from lead. `README.md` has launcher commands; `N8_CASE` selects only a named/regex subset of cases. Do not blindly rerun the full suite or delete the retained database/credentials. The optional `N8_RESTART` path is unfinished and must not be counted as tested.

Final presentation screenshot: historical task-local evidence from the N8 browser worktree; the external screenshot is not a durable repository artifact and is therefore not presented as a clone-resolvable link. Durable reproduction context remains in this document and `N8-review.md`.
