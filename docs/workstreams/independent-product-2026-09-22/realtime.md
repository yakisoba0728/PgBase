# Realtime delivery correctness — 2026-09-22

This work fixes PgBase's own connection, authorization and durable replay contracts. It does not add PocketBase option/header semantics, alter topic permissions or change storage schemas. The comparison audit remains unchanged.

## Changes

- Application topics no longer enter the collection polling reader. Queued application messages recheck current credentials, subscription membership and authorization before writing.
- Multiplex connections use a 30-minute context and refresh a 10-second write deadline before writes. The production server's ordinary write timeout no longer cuts them off. A blocked consumer releases connection and broker admission after the deadline.
- `PB_CONNECT` carries its client ID only in data. It cannot overwrite the numeric SSE replay cursor with a UUID.
- Record topic pages are merged by delivery sequence through the minimum fully read watermark. Pending memory is bounded to 100 selected messages plus the remaining subscriptions matching the final sequence, rather than retaining 100 full records for every admitted topic. An overlapping sequence is acknowledged only after all its matching topics are written.
- A shared checkpoint is sent after each durable poll. Partially received topic groups may repeat on reconnect; no undelivered match is acknowledged. Subscription replacement stays serialized with delivery, preserves unchanged topic cursors and uses the refreshed credential. Expired resume positions fail replacement with 410 before changing state.
- Midstream `PB_ERROR` retains `cursor_expired` or `invalid_cursor`; other failures use `subscription_failed`. No status field was added to this frame. SDK owners were notified of the exact payload.

Initial connections without a checkpoint still start at the subscription's current head. Reconnect before receiving any numeric checkpoint is a fresh subscription; this initial gap is explicitly outside the replay guarantee. No global-head API was introduced. Custom application messages remain non-durable, exact-topic broadcasts. Current list/view authorization, relation-filtered delete limitations and record projection rules were preserved.

## Diagnosis method

The diagnosing-bugs skill was used. The preceding source audit had already identified the concrete causes, so speculative hypothesis ranking and debug logging were unnecessary and were explicitly skipped. Real HTTP regressions were written and run before editing production code. They cover the actual multiplex handler, HTTP server deadlines, interruption/reconnect and the reader's bounded-page contract. PostgreSQL tests additionally exercise the real reader, outbox and app routing.

## Red evidence

Command:

```text
go test ./internal/api -run '^TestRealtime(ApplicationTopicSurvivesCollectionPolling|Multiplex)' -count=1 -v
```

Before the fix, all six new tests failed (16.510s):

| Test | Observed failure |
|---|---|
| TestRealtimeApplicationTopicSurvivesCollectionPolling | After first poll, broker delivered=0 because connection was removed. |
| TestRealtimeMultiplexOutlivesHTTPServerWriteTimeout | Actual http.Server WriteTimeout=40ms caused unexpected EOF after a 100ms wait. |
| TestRealtimeMultiplexReconnectBeforeFirstRecord | Last-Event-ID from PB_CONNECT caused HTTP400 invalid_cursor. |
| TestRealtimeMultiplexOrdersTopicsBeforeAdvancingResumeCursor | First event was later topic seq20, before earlier topic seq10. |
| TestRealtimeMultiplexInterruptedOverlappingTopicsReplayUndeliveredMatch | First match acknowledged seq10 before the second subscription received its match. |
| TestRealtimeMultiplexDoesNotAdvancePastAnotherTopicsFullPage | Later seq200 preceded a busy topic's first 100-record page. |

A separate regression was then run before changing subscription validation:

```text
go test ./internal/api -run '^TestRealtimeMultiplexRejectsExpiredResumeBeforeReplacingSubscriptions$' -count=1 -v
```

It failed with status204 instead of410 (0.121s).

## Green evidence

- `go test ./internal/api -run '^TestRealtime(ApplicationTopicSurvivesCollectionPolling|Multiplex|Replace)' -count=1 -v`: all seven regressions plus existing replacement serialization test passed (7.532s).
- `go test ./internal/api -run '^TestRealtimeMultiplexSlowConsumerReleasesAdmission$' -count=1 -v`: passed (10.186s). A real TCP connection with a small receive buffer stopped consuming data; the server closed the blocked stream and freed admission without client cancellation.
- `docker exec -w /workspace pgbase-remaining-runner go test -race -p 2 ./internal/api ./internal/realtime -count=1`: passed; API 19.738s, realtime 1.014s. Includes queued unsubscribe/revocation and same-principal reauthentication tests.
- `docker exec -w /workspace pgbase-remaining-runner go test -tags=integration -p 2 ./pgbase -run '^TestRealtimeMultiplexPostgres' -count=1 -v`: passed (5.108s), including `TestRealtimeMultiplexPostgresInterruptedOverlapReplaysEveryTopic` and `TestRealtimeMultiplexPostgresApplicationTopicSurvivesPollAndRevocation`.
- `docker exec -w /workspace pgbase-remaining-runner go test -race -tags=integration -p 2 ./internal/realtime ./pgbase -run '^Test(HeadOnlyRead|ReadFor|LateCommit|LeaseFence|DeliveryFailure|PocketBaseRealtime|Realtime)' -count=1 -v`: passed; realtime 1.850s, pgbase 23.050s. This covers current policy, deleted snapshots, lease fences, retention, retry, guest/admin/service access, lifecycle hooks, session revocation and the new PostgreSQL regressions.

The fixture uses PostgreSQL17.9 and Go1.26.8. No new Docker service was created or restarted by this work. No production file exceeds 800 lines (realtime_sessions.go 464; realtime_delivery.go 251). No repository policy ceiling, SDK source or immutable comparison audit was edited. Production scope is the API realtime session/delivery boundary; the database realtime reader and schemas were unchanged.
