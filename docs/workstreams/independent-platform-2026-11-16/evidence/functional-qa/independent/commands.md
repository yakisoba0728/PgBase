# Independent QA commands and source binding

All final executions used local commit
`ecff2d1ff1ddca96211832908b1cfb6c40faf8e3`, tree
`fd5f4c233f621e35badd9a5e401f5e090ddf310b`, in the independent review worktree.
The later evidence commit changes only this evidence directory. Publication may
recreate commit metadata; compare the exact tree and `source-manifest.json`
rather than claiming the local and published commit IDs are identical.

## Disposable database execution

```bash
export GOTOOLCHAIN=local GOMAXPROCS=2
export GOMODCACHE=/tmp/pgbase-functional-modcache
export GOCACHE=/tmp/pgbase-functional-gocache
qa_go=/workspace/scratch/2a97293516a0/toolchains/go/bin/go
qa_wrapper=docs/workstreams/independent-platform-2026-11-16/evidence/functional-qa/data/run-postgres.sh

# Repeat once per PostgreSQL version with its matching bin directory.
# 16.13: /tmp/pgbase-functional-postgres/installed-16.13/bin
# 17.9: /workspace/scratch/2a97293516a0/toolchains/postgres/bin
# 18.3: /tmp/pgbase-functional-postgres/installed-18.3/bin
PGBASE_QA_PG_BIN=<matching-bin> bash "$qa_wrapper" "$qa_go" test -p 1 \
  -tags integration ./internal/recordaccess ./internal/recordops ./internal/jobs \
  -count=1 -json -timeout=5m

# PostgreSQL 17.9, race-enabled affected packages, original timeout unchanged.
PGBASE_QA_PG_BIN=<pg17-bin> bash "$qa_wrapper" "$qa_go" test -p 1 -race \
  -tags integration ./internal/recordaccess ./internal/recordops ./internal/jobs \
  -count=1 -json -timeout=5m

# PostgreSQL 17.9, real HTTP rule/file/batch and both SDK iterator contracts.
# Put the Dart 3.13.3 binary directory on PATH for subprocess execution.
PGBASE_QA_PG_BIN=<pg17-bin> bash "$qa_wrapper" "$qa_go" test -p 1 \
  -tags 'integration sdkconformance' ./pgbase \
  -run 'Test(SDKIteratorSessionHTTPConformance|AuthRecordHTTPRulesUseActualRequestMetadata|AuthSessionHTTPRulesUseActualRequestMetadata|ProtectedFileHTTPRetainsRequestRuleContext|PublicBatchDenialRollsBackEarlierOperationHTTP)$' \
  -count=1 -json -timeout=5m

"$qa_go" vet -p 1 -tags integration \
  ./internal/recordaccess ./internal/recordops ./internal/jobs
```

The wrapper starts PostgreSQL and the test process within the same invocation,
binds only loopback, uses a fresh UTF8 cluster, and stops it at exit. Integration
fixtures create independent random databases. The initial PostgreSQL 16/18
installs lacked `pg_trgm`; matching extensions were then built from verified
version-specific PostgreSQL sources. The initial failed fixture logs are retained
under `fixture-missing-pg-trgm-*`; no migration or assertion was changed.

## Independent critical-auth RED

At local candidate `88447984d2b5eee406a53bf9ab05b5031463ead2`, only
`internal/recordaccess/rule_compile.go` was temporarily restored to its bytes at
base `55f539895f85ca3b7d8731ab8f4c3b8999ab92bb`. All new regression tests remained.
The following selection failed as expected, with real database assertions:

```bash
PGBASE_QA_PG_BIN=<pg17-bin> bash "$qa_wrapper" "$qa_go" test -p 1 \
  -tags integration ./internal/recordops \
  -run 'Test(RecordMutationMissingTransportContextFailsClosed|BatchMissingTransportContextRollsBackAllArtifacts|RecordMutationTransportControlsAndBodyOnlyRules|RecordCreateReplayRequiresTransportContext)$' \
  -count=1 -json
```

The compiler was restored to the fixed candidate before any GREEN execution.
The worktree then fast-forwarded to `ecff2d1`, whose additional changes were
author evidence only. This is a scoped production-file reversal, not a claim
that every source file in the RED checkout came from the base commit.

## SDK and source checks

```bash
cd sdk/typescript
npm test
cd ../dart
dart test --no-color --reporter expanded
dart analyze
```

TypeScript dependencies used a matching-lock dependency directory. Dart's copied
package configuration resolves `pgbase` through `../` in the independent
worktree, so the SDK under review was this worktree's source. No compiled test
cache from the author worktree was reused. The TypeScript build regenerated
tracked distribution output without a diff. Final `git diff --check` passed.

Raw JSONL logs are losslessly gzip-compressed; `compressed-artifacts.json`
records both raw and compressed SHA-256 hashes. `verification.json` distinguishes
expected RED and failed fixture attempts from accepted GREEN executions.
