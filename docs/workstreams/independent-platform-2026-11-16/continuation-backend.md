# Backend continuation after conversation recovery

## Source and recovered scope

This continuation starts at the actual published PR #148 head
`5285e9963251a73656dd22eb2f8a9254ba21678e`, rather than repeating the earlier
refactor. The previous backend ledger is [final-backend.md](final-backend.md).
Its job fencing, record panic outcomes, trusted savepoint cleanup, migration 68
queue admission, ordering, retention and history-poll changes are already in
the starting source. They were not reimplemented here. Historical test results
retain their original source scope.

Work used a new `continuation/backend-20261116` worktree. The recovered earlier
worktrees and their uncommitted files were read only. This report does not
claim their unpublished work was integrated; the lead owns recovery and
publication of those commits.

## Reproduced corrections

| Commit | Actual defect and correction | Evidence |
| --- | --- | --- |
| `f6c563088a1feeda466c29caafd7e09363b1782e` | A failed current-record relation-filter lookup was treated as an empty successful result. The stream emitted `id: 10` and advanced its durable checkpoint without delivering the matching event. Errors now produce sanitized `PB_ERROR` and preserve the previous checkpoint for retry. A genuine successful nonmatch still advances. | Pool deadline and PostgreSQL `08006` fault cases fail before; after recovery the same event is delivered and then acknowledged. |
| `59a854cc0d6b897829424a7604dede8f4d1786b9` | Live expansion omitted the request policy context passed by ordinary HTTP reads and relation-filter checks. A real PostgreSQL rule on `@request.headers.x_scope` allowed the direct expansion but SSE omitted it. Expansion now uses the current stream request context. | Real PostgreSQL 17.9 integration case fails before in the allowed case; both allowed and denied scopes match direct reads after the fix. This demonstrates a functional policy-context loss, not a demonstrated authorization bypass. |
| `7779f0977db0237a2d21c8d5587d2cfa74339702` | Expansion also swallowed operational Get failures, delivered a partial event and acknowledged it. Timeouts/connection errors now retain the previous cursor. `query.ErrRecordNotFound` and `recordaccess.ErrDenied` still omit only the live expansion of an event snapshot already authorized by the replay reader. | Both transient fault cases fail before, then pass with retry/recovery. Dedicated absence/denial cases preserve the prior snapshot behavior. |

These are consumer delivery defects. They do not demonstrate corruption or
loss of stored PostgreSQL records. No migration, schema version, public data
shape or subscription layout changed. The stream failure/retry behavior and
current-record absence boundary are documented in [Realtime](../../realtime.md).

## Executed verification

Final runtime: `7779f0977db0237a2d21c8d5587d2cfa74339702`.
Exact Git tree: `a06ecc1411fe2a037fc4a18f5d513e766060ac68`.

```sh
GOMAXPROCS=2 go test -race -tags=integration ./internal/api \
  -run '^TestRealtime(RelationFilter|ExpansionUsesCurrentRequestPolicyContext|ExpansionFailure|MissingOrDeniedCurrentRecord)' \
  -json -count=3
```

The disposable local PostgreSQL 17.9 cluster was available through
`PGBASE_TEST_ADMIN_URL`. This exact-source run passed **15 top-level executions
and 39 total test pass events**, with **zero failures or skips**. Package elapsed
time was **2.159 seconds**. These are repetitions of five top-level tests, not
15 distinct tests.

The earlier `f6c56308` source also passed the broader
`^TestRealtime(RelationFilter|Multiplex|Queued)` selection with race ×3 in
**50.413 seconds**. That selection includes overlapping-topic replay,
full-page watermarks, connection lifetime, slow-client release, revocation and
reauthentication. It is explicitly a check of the first fix's source, not a
claim that the later expansion fixes have already passed the final full CI.

The [evidence directory](evidence/continuation-backend/README.md) retains the
before failures, focused passes, raw final JSON events and SHA-256 manifest.
The final run used Go 1.26.11, `GOMAXPROCS=2`, an isolated newly initialized
PostgreSQL cluster at loopback port 56543 and `max_connections=100`. No production
services or credentials were involved. The cluster was stopped after each
test run. No hard CPU or memory quota was verified and these short fault tests
are not a capacity, real network outage, or recovery-time benchmark.

## Reviewed boundaries in this continuation

The following describes actual reads and checks, not package-wide certification.

| Source paths | Boundary examined | Result / remaining scope |
| --- | --- | --- |
| `internal/api/realtime_sessions.go` | GET connection admission, POST subscription replacement, request credential refresh, broker authorization, cancellation and cleanup | Traced the complete file and its interaction with delivery. Remaining lifecycle observer outcomes and all custom hooks are not independently qualified by this report. |
| `internal/api/realtime_delivery.go` | Per-topic minimum watermark, overlapping-topic checkpoint, current-record filter fallback, expansion, projection, write failure and final cursor update | Three reproduced defects above fixed. Focused transport/recovery tests and real policy-context test executed. Large fan-out, sustained dead backlogs and long-lived RSS remain performance/reliability work. |
| `internal/api/realtime_broker.go` | Exact topic snapshot, bounded per-client queue, payload copy, per-target authorizer and unsubscribe revalidation | Source traced alongside delivery and existing queue/revocation/slow-client tests. No new defect confirmed in this boundary; publication capacity was not measured. |
| `internal/recordops/read.go`, `types.go`; relevant `record_handlers.go` request-context construction; `internal/accessrule/rule.go` and `expression.go` request operand resolution | How List/Get carry rule context through the transaction and query layer; behavior of a missing request context | Confirmed the expansion discrepancy using real rules and PostgreSQL. This is not a full query/access-rule compiler audit. |
| `pgbase/app.go`, `background.go`; associated lifecycle tests | Start/Run/Close ownership, HTTP drain order, background cancellation and bounded cleanup | Rechecked source and previous coverage. New independent pool-exhaustion, whole-database restart and OS/network fault execution were not performed here; the lead's integrated gates remain required. |
| `internal/database/pool.go`, `internal/schema/listener.go`; pool/listener integration tests | Session configuration, pool closure, dedicated coordination ownership, reconnect readiness and cache publication | Source and existing test mechanisms reviewed. Prior listener tests terminate one backend and synchronize two caches; they are not equivalent to a whole PostgreSQL restart. No new execution result is invented. |
| `internal/observability/metrics.go`, `logger.go` | Closed telemetry labels, request route templates, request/status capture, token gate and logger redaction entry | No concrete defect reproduced in these reads. Arbitrary embedder/logger values, the remaining request-log surface and every metrics caller were not exhaustively audited. |

The first two source columns make this continuation's incremental manual
coverage reviewable. They do not erase omissions from `final-backend.md`.
The full schema compiler/planner/apply inventory, every record field/modifier,
CLI/configuration implementation, external storage fault matrix, arbitrary
extension hooks, and distributed sustained production qualification remain
outside this bounded continuation's completed coverage. Automated source
extraction and passing package tests do not constitute a manual audit of all
files.

## Integration handoff

Cherry-pick the three runtime commits in the order shown above, then this
documentation/evidence commit. Root independently reviews and publishes to
the existing draft PR, with no main merge, force push, release or deployment.
Run the required full-source CI on the final integrated source, including
PostgreSQL 16/17/18 and the browser fixture corrections owned by Frontend.
The tests above have not independently qualified that future integrated tree.

This agent left no running PostgreSQL, Go test or build processes. Further
local fault-suite repeats were deliberately not started because the independent
performance agent needs a quiet measurement window. The unexecuted tests remain
an explicit gate, not an inferred pass. The overall release decision remains
with the lead and the current CI/performance evidence.
