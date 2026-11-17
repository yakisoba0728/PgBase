# Main-to-PR change map

Status: **CONSOLIDATED; overall qualification BLOCKED by the current latency gate**. The counts below remain the immutable starting inventory. The four bounded correction areas are integrated in candidate89e883fc and independently verified; the current mandatory performance gate fails.
The [initial inventory](../evidence/final-master/initial-inventory.json) contains
every path, category, byte length and SHA-256.

## Git boundary

`main` remains `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`, tree
`5931b9aa15e2e963474295faa14fd2d82a133c0c`. Starting PR #148 HEAD was
`242f80aea730ee483cc22798fb6450bdb3064081`, 170 commits ahead and zero behind;
the merge-base is exactly main. **NO_MAIN_ROLLBACK_NEEDED**. Earlier merged UI,
security and operational work already in main is the preserved baseline, not a
new rollback target. The only open PR found by live reconnaissance was #148.

The work remains on `refactor/independent-platform-20261116` through that single
Draft/Open/Unmerged PR. Local author commits may be cherry-picked or published
through equivalent Git trees; the coordinator records actual publication/source
identity separately rather than treating local and remote SHAs as identical.

## Starting diff: 928 files

| Inventory category | Files | Meaning / review boundary |
| --- | ---: | --- |
| Production/runtime | 52 | Auth/HTTP rule contexts, transaction outcome, Storage, Realtime/Jobs, query codecs, listener, configuration/build roots |
| Migrations | 5 | Two new SQL migrations (68 and 69), migration registration/tests, and Storage migration runtime |
| SDK | 46 | TypeScript and Dart source/tests/contracts, including generated TS distribution files |
| UI | 27 | Session-safe settlement, record/file/realtime/backup consumers, browser cases and supporting tests |
| Tests | 56 | Runtime and integration regressions outside the SDK/UI-specific categories |
| CI/tooling | 20 | Complete qualification, source analysis, load evidence and observer tooling |
| Generated artifacts | 67 | Checked-in built administrator assets and related generated output |
| Documents | 40 | Operator guides and source-scoped reports |
| Raw evidence | 615 | Historical logs, manifests, archives, retained failures and analysis |

These are the original classifier's mutually exclusive categories; “runtime”
includes root documentation/configuration, “migrations” includes a Go runtime
file, and SDK includes its own tests/dist. They must not be added again to the
test/generated categories. See exact paths rather than inferring executable
line counts from these labels.

## Module and compatibility map

| Area | Main-to-PR behavior | Principal review files | Compatibility boundary |
| --- | --- | --- | --- |
| Auth and HTTP | Request policy metadata is bound at the HTTP adapter; embedded calls fail closed when transport context is required. OIDC/avatar/mail/OTP admission and session lifecycle contracts are tightened. | `internal/api/request_rule_context.go`, `internal/userauth/`, `internal/accessrule/` | Previously accepted unsafe/ambiguous requests may now be rejected. Real external-provider acceptance remains separate. |
| Transactions and records | Owned/borrowed transaction cleanup, panic outcome, savepoint failure and batch side effects preserve committed/rolled-back distinctions. Query field ordering avoids repeated copies when already ordered. | `internal/database/tx.go`, `internal/recordops/`, `internal/lifecycle/`, `internal/query/`, `pgbase/trusted_database.go` | External effects are not undone by a SQL rollback; final runtime performance must be measured on the integrated candidate. |
| Storage and recovery | Pre-Put durable intent, uncertain-write retention, protected publication, legacy-worker guards and independent restore namespace handling. | `internal/filestore/`, migration 69, Storage/restore integration tests | Stop/drain/migrate/start; do not discard retained intents or assume an older worker is compatible. |
| Realtime and Jobs | Durable pending queue/order/replay retention, lease/generation completion checks, occurrence identity and listener connection lifecycle. | `internal/realtime/`, `internal/jobs/`, `internal/schema/listener.go`, migration 68 | Dead predecessors can intentionally block later delivery; external Jobs effects need handler idempotency/fencing. |
| SDKs and administrator | Session generation follows queued work and pagination; stale callbacks, reconnect/dispose and async settlements cannot adopt a later identity. | `sdk/typescript/src/`, `sdk/dart/lib/src/`, `ui/admin/src/api/` and affected consumers | No new wire schema in this final-master correction set; `403 session_changed` is expected for obsolete SDK work. |
| Packaging and qualification | Full-source CodeQL, hardened runtime assets, browser/black-box recovery and fixed-arrival load evidence. | `.github/workflows/`, `scripts/`, `deployments/` | Passing a build/scanner is neither a release nor a zero-vulnerability conclusion. |

Source-relative operator guidance is in [migration and rollback](migration-and-rollback.md)
and [risk register](risk-register.md). This map is not a new exhaustive audit
claim for every file in those modules.

## New final-master corrections integrated and independently verified

| Local author identity | Reproduced problem / correction | Qualification state at this document snapshot |
| --- | --- | --- |
| `e59fc07668e0a282555f2f52dbddf890162254e0` with follow-up `4717aed` | Storage Put/Open/Delete and publication must use a lease owned by the receiving controller; scoped helper/test follow-up | Independent unit and physical-PG RED/GREEN complete; final PG/race jobs pass |
| `8ccf8ca7b5a7a973dc209563e2b3256966da89be` | Queued TS/Dart refresh captures original generation before queue wait; obsolete work cannot refresh the replacement user | Independent lifecycle RED/GREEN complete; final TS123/Dart142 and real-server contracts pass |
| `b56b74022eced2c767429b435a2abc4031ec37ee` | Jobs and Storage migration callbacks retain invocation session ownership | Independent7RED→15GREEN; final full UI458/build/browser checks pass |
| `6add53b53b57e6fc3826820ac7b6580b493629cb` | Offline HTTP comparison validates registered stage order, latency population and pool bound | Independent RED/GREEN and historical-log replay pass; current sustained gate fails latency with unchanged protocol |

These are P2 correctness/evidence corrections. The documented reproductions do
not claim a public HTTP privilege bypass, production data loss, or measured
throughput improvement. Schema remains69. From the starting source to frozen89e883fc, independent QA accounts for all65changed non-document entries across the four accepted correction areas (including generated assets); no extra runtime/tooling changes are hidden in the documentation closeout. Published IDs, exact source trees and current results are in [final qualification](final-qualification.md).

## Side branches: retain history, import no diagnostics blindly

| Existing branch | Reconciliation | Decision |
| --- | --- | --- |
| `coordinator/nonperformance-completion-20261116` | Its HEAD `fff75f80` is an ancestor of starting PR HEAD (18 commits behind). | No extra change to import. Preserve branch/history. |
| `agent/phase-b-storage-recovery-20261116` | Diverges at `a1a65228`; other non-doc patches were equivalent or ancestral in coordinator's local patch-id review. Remaining unique `610b1c2` adds load admission/host/slow-SQL diagnostics. | `HISTORICAL / NOT_IMPORTED`: changes observation overhead and CSV fields; no current reproduced need justifies adopting the experiment. |
| `coordinator/subagent-first-20261116` | Shares the above diagnostic lineage; no separate open PR. | Same decision; do not import the same experiment twice. |
| `coordinator/firefox-trace-20261116-final` | `88d4699` and `d055a48` add a branch-triggered diagnostic workflow pinned to old source `5206f30`, with optional original Chromium preconditioning. | `HISTORICAL / NOT_IMPORTED`: a diagnostic reproduction harness is not a runtime fix or current full browser gate. |

The evidence reviewer directly inspected those three unique commits' workflow
and diagnostic changes. This preserves the historical Firefox hypothesis without
claiming its root cause was established. No old branch/PR was deleted or merged.
