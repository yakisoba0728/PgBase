# Agent B: bounded runtime implementation decision

## Decision

**NO_PATCH / BLOCKED.** No reproducible avoidable connection hold, transaction leak, redundant durable write, or accounting defect was established in the reviewed sustained-load path. No runtime, test assertion, workload, connection-pool, durability, timeout or acceptance threshold was changed. This is not a performance pass and does not invalidate the failed 588.031143 ms p99 result.

Source reviewed and executed: `77bc070047c5a19d68439b75cb7b65bcde5d1c9a`, tree `07fc4145c03d636c1416e2e838ad701cebc3b03b`. The source is a documentation descendant of failed qualification candidate `89e883fc700ef0250024427a1f09bb88affe0383`. The comparison baseline is historical passing source `4091c3be20ec8db177886a2a85550924fce37ff5`; this is a source comparison, not a controlled timing comparison.

## Reviewed paths and findings

| Bounded question | Code and finding | Decision |
| --- | --- | --- |
| Does authentication retain a pool connection across the request? | `internal/userauth/service.go:Authenticate` performs token-policy/session/identity reads with sequential `QueryRow(...).Scan(...)` calls. `internal/api/user_handlers.go:userSessionMiddleware` invokes the endpoint only after authentication returns. | No nested pool acquisition with a retained row/transaction was found on this path. No cache was introduced: policy and session freshness remain unchanged. |
| Does a query or mutation retain its connection after its database work? | `internal/query/service.go:inTxCatalog`, `internal/recordops/transaction.go:ownedTx`, and `internal/database/tx.go:finishTransaction` own commit/rollback. HTTP serialization/enrichment in `internal/api/record_handlers.go` follows return from the service. Borrowed transactions deliberately remain caller-owned. | Waiting for durable commit is within the transaction contract. Returning/reusing the connection early or reporting success before commit would weaken it. No excess connection hold was reproduced. |
| Can schema or collection locks simply be removed? | `internal/schema/service.go:catalogInTx` calls `Store.PinnedSchemaVersion` (`FOR SHARE`); `internal/membership/traversal.go:ResolveTraversal` also acquires a schema pin in its combined statement. `internal/recordops/collection_mutation_lock.go` documents collection-level creation/truncate ordering; `internal/recordops/transaction.go` documents snapshot and delete-graph ordering. | A repeated schema-pin clause in the same transaction does not establish duplicate durable work. Removal without preserving DDL/snapshot and cursor membership fencing is not justified. The existing DDL-blocking control passes. |
| Does page expansion exhibit an unbounded query loop? | `internal/query/expand.go`, `compatibility.go`, and the existing read-kernel query-count control cover root reads, relation hydration and expansion. The controlled scalar, batched multi-relation and shallow/nested expansion cases pass their existing ceilings. | No N+1 regression was reproduced; broader query redesign is outside this recovery. These controls do not prove every dynamic rule/production query is bounded. |
| Does the load observer borrow one of the four request connections or alter accounting? | `pgbase/open_loop_diagnostics_integration_test.go` opens an independent `pgx.Conn`. `open_loop_load_integration_test.go` explicitly sets request pool four, 32 workers, queue 256, and records intended-arrival-to-completion latency. | No pool theft or measurement correction was established. The existing observer and latency population remain unchanged. |

[Runtime scope comparison](runtime-scope-comparison.json) independently compares Git mode/blob/path entries for 13 explicit runtime/protocol scopes. Every entry in those scopes is identical across the historical passing source, failed candidate, and recovery start. [Verifier](verify-runtime-scope.py) reproduces that comparison without load generation. Identical code does not exclude a pre-existing scalability defect, nor prove that the environment is the sole cause.

Agent A's separate raw-data analysis localizes the failure to prolonged sampled COMMIT/WAL waits and subsequent pool/worker queueing with similar work volumes. This implementation review found no safe code correction that follows from those correlations. The historical observer does not assign every sampled COMMIT to a request or measure its full transaction lifetime, so it cannot establish which work is avoidable.

## Focused executable controls

A fresh, disposable native PostgreSQL 17.9 cluster was run with Go 1.26.11, `-race`, `-tags=integration`, `-count=1`, `-p 1`, `GOMAXPROCS=4`, and a three-minute per-package timeout. Only the six named existing control tests were selected. The [runner](run-focused-controls.sh) contains the exact final command, provisioning and teardown. It never executes an HTTP load benchmark.

| Control | Outcome |
| --- | --- |
| `TestInTxRollsBackOnError` | PASS |
| `TestInTxRollsBackOnPanic` | PASS |
| `TestInTxExpiredDeadlineRollsBackAndReleasesConnection` | PASS |
| `TestListenerPreservesRequestPoolCapacityAcrossReconnectAndShutdown` | PASS; explicitly asserts pool maximum four and all four request connections remain usable during listener lifecycle |
| `TestPinnedRecordCatalogBlocksSchemaDDLUntilTheRecordTransactionEnds` | PASS; the pin is an enforced correctness boundary |
| `TestReadKernelQueryCountIsBoundedByPlanShapeInsteadOfRootCount` | PASS, including three subtests |

Actual totals: **6 parent tests + 3 subtests, 3 packages, zero failures, zero skips; race enabled**. [Machine summary](focused-controls-summary.json) and [original Go JSON events](focused-controls-race.jsonl) preserve exact counts. The listener test uses and logs pool four explicitly. Other unchanged controls use their existing fixture pool configuration; they are not claimed to reproduce the four-connection sustained HTTP workload.

PostgreSQL `fsync`, `synchronous_commit`, and `full_page_writes` are `on` in both saved settings snapshots. Test cleanup leaves zero `pgbase_test_*` databases; the cluster then stops successfully. No existing or operational database was accessed.

### Retained setup failures

The first provisioning attempt could not create a Unix-domain socket in this executor. Switching only the disposable fixture to loopback TCP allowed PostgreSQL to start. Running the test command in a subsequent executor invocation then returned connection-refused errors for all six parents before their runtime behavior executed. The original [failed setup test events](focused-race.jsonl) remain intact; these are **fixture failures, not application RED reproductions**.

The final runner keeps PostgreSQL startup, tests and teardown in the same executor process/network lifetime and disables Unix sockets. That fixture correction allowed the six controls to execute once successfully. It changes neither repository runtime nor the final CI protocol. [Initial provisioning logs](initial-provisioning-postgres.log) preserve both starts. No favorable performance host was selected and no 600-second load was run.

## Safety and resumption boundary

No Storage/Realtime/Jobs/SDK behavior changed; their prior qualifications retain their original source scope. No new whole-project or security qualification is claimed. There is no code correction for independent QA to approve, and these control passes cannot authorize the final sustained gate.

A next investigation needs a bounded, attributable reproduction: request/transaction IDs correlated with pool acquisition, database transaction work and COMMIT acknowledgement under recorded durable I/O conditions, or a concrete failing connection-lifetime/query-count assertion. If that identifies unnecessary work, preserve transaction atomicity, DDL/membership locks, authentication freshness and the load protocol in the smallest correction; then obtain separate RED-to-GREEN QA. Without such evidence, retain **BLOCKED / NOT_READY** and do not rerun the unchanged final candidate.
