# Agent A — missing transport metadata at ordinary record authorization

## Source and bounded outcome

- Baseline: `55f539895f85ca3b7d8731ab8f4c3b8999ab92bb`.
- Fix and regression source: `5810ae6cfd2d7d345cb617f47f96ee8d5c422196`.
- One reproduced root cause; no further defect discovery after coordinator selected three overall.
- Author verification passed. Independent review is assigned separately; this author does not count as independent QA.

## Inspected boundary and actual failure

Ordinary record create/update and batch operations build body/isset/changed rule metadata even when an embedded caller supplies no trusted HTTP metadata. That nonnil object previously made negative method/header/query comparisons evaluate against empty strings. The auth-collection evaluator already guarded this condition, but ordinary record authorization did not.

`red.log` was run with the new failure tests and otherwise unchanged baseline source. All six subcases (missing method, header, query, nested alternative, right-hand request operand, and an empty metadata object) incorrectly admitted **both create and update** for an active ordinary-user principal. SQL assertions found the forbidden row and updated value. The batch reproduction also committed a row, revision, audit event and outbox event. Both top-level regression tests failed as expected.

This demonstrates an authorization failure for an admitted ordinary-user principal through the embedded record service/batch API when its caller omits transport metadata. It does **not** establish a remotely reachable native HTTP bypass; normal HTTP routes capture metadata. It is also not a service-token principal bypass.

## Narrow correction

`internal/recordaccess/rule_compile.go` denies a request-metadata-dependent policy when its context is absent or its HTTP method is empty. Dependency detection covers both comparison operands, function arguments and nested rules, including OR alternatives. Operation-body and actor operands remain separate. Valid HTTP metadata still permits a genuinely missing header to have its normal empty value. Administrator/service-scope behavior and schema 69 are unchanged.

## Executed evidence

Go `1.26.11`, disposable UTF-8 PostgreSQL `17.9`, loopback-only, random port, testutil-owned per-test databases, no production credentials.

| File | Actual result |
| --- | --- |
| `red.log` | Expected failure on baseline; 2 top-level tests, 6 failing denial subcases, persisted forbidden state demonstrated |
| `green.log` | 4 top-level tests and 15 subcases pass; original denial/rollback cases plus real method/header/query values, absent-header semantics and embedded body-only controls |
| `race.log` | 13 top-level tests and 20 subcases pass across `recordaccess` and `recordops`; includes idempotent replay denial/authorized replay, mutation body semantics, relation policies and request-rule validation |
| `unit.log` | Existing `accessrule` and `recordaccess` unit suites pass |
| `vet.log` | `go vet -p 1 ./internal/recordaccess ./internal/recordops` exits 0; empty output is success |
| `setup-cross-exec-failure.log` | Preserved initial fixture connection failure, **not** defect reproduction |
| `control-test-parse-failure.log` | Preserved initial newly added control using unsupported `title:changed` textual syntax; corrected to supported `@request.body.title:changed`; not a product failure |

The race execution used exactly the source files captured in the fix commit. The final integrated SHA and PostgreSQL 16/18 validation belong to the independent QA handoff.

### Commands

All Go invocations used `GOTOOLCHAIN=local`, `GOMAXPROCS=2`, shared temporary `GOMODCACHE=/tmp/pgbase-functional-modcache`, `GOCACHE=/tmp/pgbase-functional-gocache`, and `-p 1`. Each integration invocation initialized and started its own PostgreSQL process in the **same shell** as the test process, exported its `PGBASE_TEST_ADMIN_URL`, and stopped/removed only that temporary cluster on exit. Starting a server in a separate tool execution did not provide a usable endpoint.

```sh
go test -p 1 -tags integration ./internal/recordops \
  -run 'TestRecordMutationMissingTransportContextFailsClosed|TestBatchMissingTransportContextRollsBackAllArtifacts' -count=1 -v

go test -p 1 -tags integration ./internal/recordops \
  -run 'TestRecordMutationMissingTransportContextFailsClosed|TestBatchMissingTransportContextRollsBackAllArtifacts|TestRecordMutationTransportControlsAndBodyOnlyRules|TestMutationRulesEvaluateIssetAndChangedRequestContext' -count=1 -v

go test -race -p 1 -tags integration ./internal/recordaccess ./internal/recordops \
  -run 'TestRecordMutationMissingTransportContextFailsClosed|TestBatchMissingTransportContextRollsBackAllArtifacts|TestRecordMutationTransportControlsAndBodyOnlyRules|TestRecordCreateReplayRequiresTransportContext|TestMutationRulesEvaluateIssetAndChangedRequestContext|TestIdempotentCreateReplayUsesCommittedGeneratedRuleContext|TestClosureRelationRequestAndSurvivorAutoDate|TestRequestRulesSaveAndExecute|TestPocketBase|TestPreviewSupportsPocketBaseRelationsAndRequestValues' -count=1 -v

go test -p 1 ./internal/accessrule ./internal/recordaccess
go vet -p 1 ./internal/recordaccess ./internal/recordops
```

## Limits and next starting point

This pass narrowly inspected request-rule transport and operation-body composition, batch propagation, and existing Realtime revalidation code. It does not certify every CRUD/file rule combination, adapter, or Realtime/session transition. No additional independently reproduced functional issue is left by Agent A. No new full CI pass, external exploit, browser result or performance result is claimed. Independent QA should apply the fix to its own worktree and run the focused regression against its integrated source SHA. Overall product remains **Not Ready**.
