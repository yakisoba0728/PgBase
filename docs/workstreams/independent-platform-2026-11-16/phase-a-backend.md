# Phase A — Backend checkpoint validation

This is the bounded Workstream B review by `/root/backend_integration`.
It qualifies selected existing changes for integration, not the whole project
or a later candidate automatically. No new backend refactor was started.

## Source and integration identity

- PR #148 began at `10dc0f8b59f1d59b5a6e745a07618ec2849fc06d`.
- The actual remote checkpoint was `7b53cb58e66d6e62d6416fc39ea676eecb3e2d6c`,
  tree `bbc76aeb0e9429b3330306b11180d4b6d78da03d`.
- GitHub compare and local Git ancestry agree: the checkpoint is 16 commits
  ahead, zero behind. These changes were published on the checkpoint branch
  but absent from the PR branch at the start of this session. A fast-forward
  retains their exact commit identities without merge conflicts.
- The complete 16-commit list, test source hashes, outcomes and raw-log hashes
  are in [the manifest](evidence/phase-a-backend/manifest.json).

| Existing logical change | Published commit(s) | Backend decision |
| --- | --- | --- |
| Retry relation lookup failures without advancing the replay cursor | `583226980d306ba658106667fbc29856944220b1` | Accept; independently reproduced and retested |
| Pass current request policy context to Realtime expansions | `da002b2f7630bbc71dee2bab12692ea20ffe4193` | Accept; independently reproduced and retested |
| Retry transient expansion failures, retain authorized snapshot controls | `ff4bb639e8d1ee5aea82aaa05881e096e0cc58a2` | Accept; independently reproduced and retested |
| Encode Extension SQL batch JSON before commit/savepoint release | `cf0a74dd01ce65b967a98bf79402f04172b9d423` | Accept; independent source review and fresh race execution |
| Auth request context, avatar redirect cap, file context/range and cleanup | `ea136122`, `6b710f50`, `120a2488`, `1708ad72` | Retain for the Security reviewer's focused acceptance; Auth source and HTTP regression independently checked here |
| Formatting, contracts, original evidence and checkpoints | Remaining eight commits | Preserve with original revision scope; no old pass is relabeled as this session's execution |

## Fresh before/after evidence

The before test uses the unchanged PR runtime plus exactly two checkpoint
Realtime test files. Its resulting test tree is
`370c89b6fd4a0ae2b5cef46d11dd1939a77147b3`. This is an explicit test overlay,
not a claim that those tests existed in the historical PR. The after test uses
the exact checkpoint runtime/test tree, with only new evidence files untracked.

| Execution | Observed result |
| --- | --- |
| PR-head Realtime reproduction, PostgreSQL17.9 + race, count 1 | Expected failure: three top-level failures / eight failure events, two top-level passing controls / five pass events; zero skips |
| Checkpoint focused Realtime, Extension/transaction, Jobs fencing and independent Auth HTTP, PostgreSQL17.9 + race, count 2 | Passed: 30 top-level executions / 140 test pass events; zero failures/skips |

The reproduced old runtime advanced the relation-filter cursor to sequence 10
after deadline/connection errors, acknowledged an event whose expansion lookup
failed, and omitted an allowed live expansion that the same-context direct read
returned. The unchanged checkpoint fixes all three paths. Genuine filter
nonmatches still advance, and missing/denied current rows retain the previously
authorized event snapshot without an expansion.

The after suite also verifies Extension NaN/Infinity rollback for owned and
nested transactions, finite-result commit, preservation of unrelated outer work,
failed savepoint cleanup, lost PostgreSQL response cleanup, transaction-bound
JavaScript, exact hook outcomes, and stale/expired job completion fencing.
The independent Auth HTTP case covers actual anonymous/authenticated list/view
request metadata with positive and negative controls.

Package elapsed times in the after run were API **2.258s**, Jobs **3.945s**, and
PgBase **15.713s**. These are correctness-test durations on a shared host, not
performance benchmarks. Both commands used Go 1.26.11, `GOMAXPROCS=2`, and a newly
initialized loopback-only PostgreSQL17.9 cluster that was stopped after the run.
The exact commands and untouched raw JSON streams are retained beside the
manifest. Historical recovery and Extension before/after log SHA-256 digests
were independently recomputed and matched their prior manifests.

## Independent review and compatibility

Source review found no blocker in the three Realtime changes or Extension
precommit encoding. Cursor update occurs only after a successful delivery/flush;
operational lookup errors emit a sanitized stream error. The authorized absence
case remains distinct from a transient database error. The extension encodes
the complete result inside the transaction callback, so encoding errors reach
the existing owned transaction/savepoint rollback path.

The Auth wrapper runs inside route admission and copies an owned method/header/
query/request-ID snapshot. The resolver copies only transport fields, constructs
body/presence/change data from the operation, and reloads the active database
actor. Its missing-transport behavior denies the whole request-dependent rule,
including nested and right-hand operands; this is documented upgrade behavior.
No blocker was found in that boundary. Fresh independent Auth HTTP execution is
included above; other Auth/file tests belong to the Security evidence.

`docs/realtime.md`, `docs/extensions-and-client-scope.md`,
`docs/auth-rule-request-context.md`, and `docs/storage-settings.md` already
document the relevant behavioral changes. The system schema remains 68.
Serialization failure now rolls back the SQL batch; later VM limits, transport
loss or subsequent script errors do not provide the same rollback guarantee.

## Limits and handoff

No additional Backend runtime patch was needed or implemented again. The
Coordinator owns fast-forward/integration, the final candidate identity and
full CI. These focused tests do not replace repository-wide Go/race,
PostgreSQL16/18, SDK/UI/browser, security scanner or sustained-load gates.
No unrelated unpublished Frontend work, general architecture audit or new
feature was recovered by this workstream.

Workstream B is **eligible for integration subject to the separate Security
acceptance and final combined validation**. This is not an overall Ready claim;
Workstream A and the final candidate's CI remain separate gates.
