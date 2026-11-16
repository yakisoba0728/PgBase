# Functional correctness and independent QA — bounded handoff

## Decision and scope

**Overall product: Not Ready.** This work is limited to three reproduced functional
root causes. Firefox and all performance work are excluded. No repository-wide
audit, architecture redesign, new large feature, UI layout change, schema change,
production access, deployment, release, or package publication is included.

**Task result: complete for the three selected defects. Independent verdict:**
`ACCEPT_SCOPED_CHANGES`. Four subagents were created and all four completed.
This acceptance is restricted to the changes and checks documented here.

The live starting PR #148 HEAD was
`55f539895f85ca3b7d8731ab8f4c3b8999ab92bb`; main was
`8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`. The PR was open, Draft and unmerged.
The earlier 14/14 nonperformance CI result belongs to its historical source,
not to the fixes in this report.

## Reproduced defects and corrections

### FQ-A-01 — missing transport metadata could admit record mutations

Ordinary record create/update and batch operations compose body metadata even
when an embedded caller supplies no trusted HTTP request context. Previously,
negative method/header/query predicates could evaluate against empty values and
admit an ordinary-user operation. The unchanged implementation admitted both
create and update in all six denial cases; SQL assertions confirmed persisted
forbidden state. A batch also persisted its record, revision, audit and outbox.

The rule compiler now denies a metadata-dependent policy when transport context
or its method is absent. Dependency detection includes both comparison operands,
function arguments, and nested alternatives. Body-only and actor rules retain
their embedded use; real request metadata and legitimate absent-header semantics
remain covered by positive controls. Batch denial leaves no corresponding data
or side-effect records.

**Reachability limit:** this is a reproduced embedded/service/batch authorization
failure for an admitted ordinary-user principal. Normal native HTTP routes
already capture request metadata. An externally reachable HTTP exploit or
service-token bypass was not demonstrated.

### FQ-B-01 — rescheduling could reuse a previous Jobs occurrence

After failure or lease expiry, changing a job's schedule/timezone moved its next
run but retained retry occurrence state. The next claim reused the old occurrence
key. A handler deduplicating effects by occurrence could consequently suppress
the newly scheduled run.

All eight combinations of Schedule/Configure, schedule/timezone changes and
failure/lease-expiry history failed on the old source. Both update paths now
atomically renew retry occurrence state only when schedule/timezone actually
changes. Unchanged saves and explicit retries preserve identity. Existing lease,
token and generation fencing remain in force; schema 69 is unchanged.

### FQ-C-01 — SDK iterators could cross a session boundary

Both SDKs fenced individual responses but retained a page buffer and cursor
across a paused consumer. Replacing authentication or logging out could expose
remaining old-session rows to the consumer, or send the old cursor using a new
session. Four TypeScript and four Dart regressions failed on the old source.

Both iterators now capture the auth generation at iteration start and check it
before page requests and record delivery. A changed session terminates iteration
with the existing `403/session_changed` contract. The TypeScript distribution
was regenerated. Stable pagination and a fresh replacement-user iterator remain
covered. This is client session isolation; a server authorization bypass was not
established. Administrator UI source/design/layout were not changed.

## Actual review boundaries

- A: ordinary record rule compilation, operation-body composition, embedded batch
  authorization, and existing context/replay controls; limited Realtime reads
  informed selection but do not constitute Realtime requalification.
- B: bounded transaction/savepoint and Storage-ownership reads, then the uncovered
  Jobs reschedule/occurrence boundary and existing operator/lease fencing.
- C: SDK user lifecycle and paused pagination; relevant administrator record-form
  code was read only. Other SDK/admin flows were not requalified.
- Independent QA: only these integrated fixes, regression quality, preserved
  controls and affected executable contracts. It is not a fresh full audit.

New defect discovery stopped when these three root causes were reproduced.

## Author evidence

| Area | Before correction | After correction |
| --- | --- | --- |
| Record authorization | 2 top-level regressions fail; 6 denial subcases admit create/update; batch data and side effects persist | PG17.9 focused: 4 top-level + 15 subcases pass; related race: 13 top-level + 20 subcases pass; accessrule/recordaccess unit and related vet pass |
| Jobs | All 8 reschedule cases fail; unchanged retry control passes | PG17.9 Jobs suite: 18 top-level + 20 subtests pass, repeated under race; related vet passes |
| SDK iterators | TS 4 failures, Dart 4 failures | TS 119/119, Dart 138/138, no skips; Dart analysis clean; actual PgBase HTTP iterator contract 1 parent + 2 SDK subtests passes on PG17.9 |

Detailed commands, source identities, output and evidence are retained in
[`evidence/functional-qa/auth/`](evidence/functional-qa/auth/),
[`evidence/functional-qa/data/`](evidence/functional-qa/data/), and
[`evidence/functional-qa/sdk-ui/`](evidence/functional-qa/sdk-ui/).
Initial cross-exec fixture failures, an author control-test syntax mistake, and
Dart cache/bootstrap failures are retained separately. They are not counted as
product defects or successful reproductions. The long Dart bootstrap log is
losslessly compressed. No failed evidence was discarded to obtain a pass.

## Integrated source and independent verification

Local integrated candidate:
`ecff2d1ff1ddca96211832908b1cfb6c40faf8e3`, tree
`fd5f4c233f621e35badd9a5e401f5e090ddf310b`.
All three runtime/test fixes were already present at local `88447984`; the next
commit adds only the remaining author evidence.

The reviewer independently restored the original rule compiler and reproduced
missing/empty-metadata denial failures, batch side effects and replay admission.
All nine real-context/body-only controls passed on that reproduction. The
reviewer then restored the complete frozen candidate before green execution.

| Independent execution | Actual result |
| --- | --- |
| PostgreSQL 16.13, affected Go packages | 169 top-level + 129 subtests pass; 0 failed / 0 skipped |
| PostgreSQL 17.9, affected Go packages | 169 top-level + 129 subtests pass; 0 failed / 0 skipped |
| PostgreSQL 18.3, affected Go packages | 169 top-level + 129 subtests pass; 0 failed / 0 skipped |
| Actual PgBase HTTP contracts on PG17.9 | 5 top-level + 40 subtests pass; both SDKs, actual auth method/header/query, protected files and denied-batch rollback |
| TypeScript SDK | 119/119 pass |
| Dart SDK | 138/138 pass; analysis clean |
| Related Go static analysis | go vet clean |
| PG17.9 affected-package race | 169 top-level + 129 subtests pass; 0 failed / 0 skipped; completed within unchanged five-minute timeout |

The first PG16/18 matrix attempts failed because these fresh local toolchain
installs lacked `pg_trgm`, required by existing migration 66. Matching-version
extensions were installed from checksum-verified official source, without
changing product migrations. Initial fixture-failure logs are preserved
separately; only the corrected completed runs are reported as passing.

The independent verdict is **ACCEPT_SCOPED_CHANGES**; no blocking regression was
found within the reviewed scope. Every accepted GREEN execution has zero
failures and zero skips. The reviewer also reproduced the TypeScript distribution
without a generated diff. Raw results, compressed failure evidence, commands,
source hashes and detailed review are retained in
[`evidence/functional-qa/independent/`](evidence/functional-qa/independent/).

### GitHub source identities

| Change / evidence | GitHub commit |
| --- | --- |
| FQ-A-01 authorization | [`2b44c141`](https://github.com/yakisoba0728/PgBase/commit/2b44c14129d6f8e144ca291275019d7b7b99f0ff) |
| FQ-B-01 Jobs occurrence | [`e976b364`](https://github.com/yakisoba0728/PgBase/commit/e976b36498f220c25b6cfb5942399d713d238bca) |
| FQ-C-01 SDK iterator ownership | [`67e5e314`](https://github.com/yakisoba0728/PgBase/commit/67e5e3145808c6673329bf9f0c870e9ed5fd5be3) |
| Exact tree validated by independent QA, including author evidence | [`5bdfc309`](https://github.com/yakisoba0728/PgBase/commit/5bdfc309d45808d56ef532ffad60dfa2acb48f28) |
| Independent QA evidence only | [`e49e835f`](https://github.com/yakisoba0728/PgBase/commit/e49e835f376960c75e343239887f93a8645e9768) |

The tested local `ecff2d1f` and published `5bdfc309` both have tree
`fd5f4c233f621e35badd9a5e401f5e090ddf310b`. Commits were reconstructed through
the GitHub Git-data API because the local CLI had no GitHub write credential.
Each resulting Git tree was checked against its local counterpart before the
single non-force branch update. Commit hashes differ; code and committed
artifacts match exactly. The full mapping is in
[`coordinator/publication-map.json`](evidence/functional-qa/coordinator/publication-map.json).

The final checkpoint after `e49e835f` adds only this report, the continuation
entry and coordinator provenance. It is not another runtime source change or
another test execution. The exact final PR HEAD is available in PR #148 and the
final handoff comment; the report does not attempt to embed its own commit hash.

## Validation environment and execution policy

Go 1.26.11 and disposable UTF-8 PostgreSQL fixtures were used. PostgreSQL 16.13
and 18.3 test binaries were built from checksum-verified official source; 17.9
was an existing local toolchain. Fixtures bind loopback only, use dedicated
temporary data directories and per-test databases, and stop after their command.
Only disposable test identities are used. No production database or secrets are
required. Shared temporary compiler caches reduce redundant setup; test database
state is isolated.

Every PostgreSQL server and its test command must run within the same execution
invocation in this environment. The committed fixture wrapper documents this
requirement. Environment setup failures remain distinguishable from assertions.

This work does not execute the Firefox-containing whole CI or any performance
workflow. Existing workflow definitions, browser tests and required-check
settings are unchanged. Publication commits use `[skip ci]` to avoid starting
excluded automatic workflows. This is not a latest-source full-CI pass and does
not waive required checks or replace the historical gates.

## Actual agent accounting

Four distinct subagents were created: Agent A, Agent B, Agent C, and a new
Independent QA agent. Maximum concurrent subagents: three (four processes
including the coordinator). Created/completed: **4/4**; no subagent remains active. A/B/C authored isolated fixes in separate Git
worktrees; the fourth agent reviewed and executed tests in its own worktree.
The coordinator assigned work, integrated commits, prepared test toolchains and
handled publication. Previous sessions' agents are not included in this count.

## Remaining risks and next starting point

- Firefox issue NP-BROWSER-01 is excluded and unresolved here. No font analysis,
  browser retry, test removal or console filter was performed.
- The historical 200 RPS / 600-second run lost **547 of 120,000 arrivals**.
  That failure remains unresolved and assigned to the user. No benchmark, load,
  tuning, pool/WAL investigation or performance pass is claimed.
- This scoped independent QA does not retroactively certify the entire previous
  refactor or erase older source-specific qualification limits.
- Storage cleanup ambiguity, writer-quiescence requirements, real external
  OAuth/OIDC/SMTP acceptance and wider accessibility/device coverage remain as
  previously documented. They were not reopened or declared newly passed.
- Jobs external side effects still need handler-level idempotency or fencing;
  lease metadata fencing cannot revoke an already-issued external effect.
- Unexamined CRUD/file/expression combinations, all Realtime lifecycle paths,
  backup/restore/migration combinations, and other SDK/admin workflows remain
  outside this bounded pass; no additional reproduced functional bug is asserted
  solely from that lack of new coverage.

Next work starts by checking the live PR HEAD and this report's immutable source
mapping, then choosing an explicitly scoped remaining contract. Do not rerun a
repository-wide audit or resume Firefox/performance without a new instruction.
This session ends after the three fixes, independent QA and GitHub handoff.
**PR #148 stays Draft/unmerged; overall product remains Not Ready.**
