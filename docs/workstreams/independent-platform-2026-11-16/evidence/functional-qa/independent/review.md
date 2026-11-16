# Independent functional review — accepted within scope

**Verdict: `ACCEPT_SCOPED_CHANGES`. Overall product: `Not Ready`.**

Reviewer `/root/independent_qa` did not author the runtime fixes. Review and
execution took place in a separate worktree at local commit
`ecff2d1ff1ddca96211832908b1cfb6c40faf8e3`, tree
`fd5f4c233f621e35badd9a5e401f5e090ddf310b`. This review covers the three bounded
defects below. No additional defect hunt or repository-wide audit was started.

## Authorization context

The record policy compiler previously accepted a body-populated request context
from embedded mutation calls even when trusted HTTP metadata was absent. A
negative method/header/query predicate could therefore authorize a write using
empty metadata. The new compiler guard returns a false predicate before policy
execution whenever a rule references transport metadata and the request or its
method is absent.

The reviewer traced field operands, the opposite operand, function arguments,
and nested rule branches through the guard. Auth and body bindings remain
exempt, consistent with the existing auth-collection boundary. A real request
whose header is absent remains distinguishable from an absent transport context.
Embedded callers that use HTTP-dependent rules must provide complete trusted
metadata; body-only rules continue to work.

The reviewer independently replaced only the fixed compiler with the base
version and ran the new tests. All six missing/empty-metadata cases failed;
batch rollback and idempotency replay authorization also failed. All nine
real-context/body-only controls passed. The compiler was then restored, and all
these regressions passed in every accepted database execution. Tests inspect
stored rows and count changes, plus revisions, audit events and outbox entries,
so rejection is verified against database effects rather than only return codes.

This reproduction concerns admitted embedded/service/batch calls with missing
trusted metadata. It does not establish an external HTTP exploit. Existing
actual-HTTP auth metadata, protected-file metadata, and denied-batch rollback
contracts also passed in the independent HTTP run.

## Jobs occurrence identity

The reviewer checked both Schedule and Configure SQL against claim and complete
semantics. Each accepted schedule/timezone change now clears attempts,
last-scheduled identity and prior failure in the same statement that changes the
schedule. Expressions use the old row values; unchanged settings preserve the
pending retry identity. Existing lease predicates and token/generation fencing
remain present.

The new regression covers both management APIs, schedule and timezone changes,
and failed/expired attempts. It asserts fresh occurrence identity, attempt one,
cleared error and advanced fencing. The unchanged-schedule/retry control and
existing active-lease, stale-completion and admin-edit contracts passed.
External effects still need idempotency or their own fence; this change does not
claim that a database lease can revoke a previously started external action.

## SDK iteration ownership

Both SDKs now capture the existing auth generation for a paginated operation,
check it before requesting another page and before yielding each buffered row.
They use the existing `session_changed` error contract. The reviewer checked
that an auth switch cannot send an old cursor with the replacement token and
that a paused consumer cannot receive a buffered old-user row afterward.

Both SDK test suites passed in the independent worktree. The live-server test
checks buffered/page boundaries, counts actual requests to reject a stale
follow-up, preserves stable-user pagination and verifies the replacement user's
fresh iterator sees no prior-user rows. The TypeScript distribution rebuilt
without a tracked diff. Public signatures, dependencies and UI layout did not
change.

## Executed evidence

| Independent execution | Actual outcome |
| --- | --- |
| PostgreSQL 16.13 affected Go packages | 169 top-level and 129 subtest pass events; zero failures/skips |
| PostgreSQL 17.9 affected Go packages | 169 top-level and 129 subtest pass events; zero failures/skips |
| PostgreSQL 18.3 affected Go packages | 169 top-level and 129 subtest pass events; zero failures/skips |
| PostgreSQL 17.9 affected packages with `-race` | 169 top-level and 129 subtest pass events; zero failures/skips; original five-minute timeout unchanged |
| Selected real HTTP contracts | Five top-level tests and 40 subtests passed, including two SDK subtests; zero failures/skips |
| TypeScript SDK | 119 passed; zero failures/skips |
| Dart SDK | 138 passed; zero failures/skips |
| Related Go vet / Dart analyze | Exit zero / no issues |

Affected Go packages are `internal/recordaccess`, `internal/recordops` and
`internal/jobs`. Counts distinguish top-level tests from subtest pass events;
the repeated database versions are separate executions of the same tests.

Initial PostgreSQL 16/18 fixture attempts failed because locally compiled
installs omitted the `pg_trgm` extension required by migration 66. Their full
failed logs are retained separately. Matching extensions were installed, then
the unchanged candidate passed. These attempts are not functional regressions
or expected-red defect proof.

`verification.json` binds outcomes and exit codes to artifacts;
`source-manifest.json` records all 13 changed non-document file hashes;
`compressed-artifacts.json` records raw and compressed hashes. Long logs are
losslessly retained as gzip. Evidence, including the author's decompressed
bootstrap log, was checked for private-key, GitHub-token, JWT and AWS-key
patterns with zero matches; this is a scoped credential hygiene check.

No introduced functional blocker was found within these changes. Existing
tests were not weakened. Workflow files, schema version 69 and UI files are
unchanged. No Firefox or performance activity occurred; no full-CI pass or
product-readiness claim is made. The historical browser issue, user-owned
performance gate and other previously documented limits remain outside scope.
