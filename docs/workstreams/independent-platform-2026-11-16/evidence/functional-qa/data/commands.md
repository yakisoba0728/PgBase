# Reproduction commands

Commands ran from the agent worktree with Go 1.26.11 and PostgreSQL 17.9.
The wrapper creates a fresh UTF8, loopback-only PostgreSQL cluster under `/tmp`;
each Go integration test creates and drops its own random database. No prior
database or credentials were reused.

```bash
export PGBASE_QA_PG_BIN=/workspace/scratch/2a97293516a0/toolchains/postgres/bin
export GOTOOLCHAIN=local GOMAXPROCS=2
export GOMODCACHE=/tmp/pgbase-functional-modcache
export GOCACHE=/tmp/pgbase-functional-gocache
qa_go=/workspace/scratch/2a97293516a0/toolchains/go/bin/go
qa_wrapper=docs/workstreams/independent-platform-2026-11-16/evidence/functional-qa/data/run-postgres.sh

# RED: base production files, new regression test added; exit 1, 8 case failures.
bash "$qa_wrapper" "$qa_go" test -p 1 -tags integration ./internal/jobs \
  -run 'Test(JobRescheduleStartsNewOccurrenceAfterFailureOrExpiry|UnchangedJobSchedulePreservesPendingOccurrence)$' \
  -count=1 -v

# GREEN: source 47375c87669ea208813a1d4b1af71ef710db1d3b; exit 0 each.
bash "$qa_wrapper" "$qa_go" test -p 1 -tags integration ./internal/jobs -count=1 -v
bash "$qa_wrapper" "$qa_go" test -p 1 -race -tags integration ./internal/jobs -count=1 -v
"$qa_go" vet -p 1 -tags integration ./internal/jobs
```

The RED regression predates the additional assertion that a replacement claim
starts at attempt 1 with no prior error; that stronger assertion is present in
the GREEN source. All eight original RED assertions remain unchanged.

The initial environment failure used a server started in another exec invocation
and received connection refusal. Its log is retained and never counted as the
pre-fix functional failure. The wrapper resolves this environment requirement;
it does not alter product code or test assertions.
