# Realtime record events

An authenticated user subscribes to `GET /api/v1/user/collections/{collection}/events` with its Bearer token. The stream sends `record` events containing `eventId`, `collection`, `recordId`, `operation`, and `version`. It contains mutation metadata, not a full record or file payload. Fetch a record through the usual permission-checked API after an event. File attachment changes have their own API lifecycle and do not currently emit record mutation events.

The native collection event stream accepts no query parameters; `fields` cannot project its metadata envelope into a record payload. For filtered or projected record data, use the multiplex record subscriptions described below.

The SSE `id` is a monotonically increasing PostgreSQL `int64` delivery sequence, distinct from the event's UUID. Save the most recent SSE ID (including `cursor` frames) and supply it as `Last-Event-ID` on reconnect. Initial connections without a cursor start at the current head. A cursor older than retained events returns HTTP 410; reload records and start a fresh subscription. The TypeScript SDK exposes `RecordEvent.deliveryId` as a decimal string so values above JavaScript's safe-integer range remain exact. Pass `lastEventId` as a decimal string for such values; unsafe numeric inputs are rejected. The SDK handles stream framing and bounded reconnect attempts. Consumers should deduplicate by event UUID when applying effects.

The PostgreSQL outbox queues delivery in the same transaction as each record mutation. Workers claim bounded batches with expiring leases and fenced publication; delivery sequence allocation is serialized through commit. This does not use a UUID high-water mark, so a late-committing transaction is still delivered. Versions of one record are delivered in order. Failed publication backs off and reaches a dead letter after ten attempts. A dead predecessor blocks subsequent versions of that record until an operator retries it; unrelated records continue.

Administrators can inspect `GET /api/admin/realtime/status`, list dead events newest first through `GET /api/admin/realtime/dead?before={nextBefore}` (50 per page; `{items:[{eventId,collectionName,recordId,operation,recordVersion,attempts,createdAt,deadAt}],nextBefore?}`; the opaque cursor stays valid when listed events are retried; the event envelope and record snapshot are not returned), and retry a dead event through `POST /api/admin/realtime/{eventId}/retry`. Partial indexes keep dead-event listing and pending-queue ordering separate from delivered history. The exact delivered count scans historical delivery rows. Use `GET /api/admin/realtime/status?queueOnly=true` for frequent status checks. It omits the delivered count and reports the original enqueue time of the oldest pending event even after retry backoff. Retrying an already delivered event does not republish it.

Active user status, session validity, collection admission and current list/view rules are checked during delivery. Delete events retain the owner and row snapshot needed to evaluate permission after the base row is gone. Subscriptions are limited to four connections per user and 128 per process, with a 30-minute lifetime and 10-second write deadlines. Clients reconnect after normal lifetime expiry. Retention defaults to 24 hours; delivered outbox markers remain durable to prevent redelivery. Realtime retention is not audit retention.

## Queue and retention operations

System migration 68 installs the database-owned enqueue trigger and backfills
missing delivery rows. Older record writers that only insert their outbox row
still enqueue atomically. The migration preserves delivered/dead/retry state;
only pending/dead historical rows need ordering metadata. Follow the normal
[drained migration procedure](deployment.md#required-production-sequence), run the new binary's
`pgbase migrate up`, and require `pgbase migrate status` to report the latest
clean schema before serving. This does not declare old binaries compatible
with the new schema: the ordinary exact-version startup gate remains in force.

Retention checks at startup and at least once per minute. While expired replay
history remains, each ordinary delivery poll removes at most 1,000 leading
events, with a one-second transaction deadline plus bounded rollback cleanup.
It yields between batches so new events continue to be delivered. A failed pass
waits for the next maintenance tick. A fully fresh leading window stops the
catch-up; database clock corrections cannot expire a cursor across an
unexpired earlier event. Clock corrections or maintenance failures can thus
retain events longer than 24 hours.

Monitor pending/dead counts, the age of the oldest pending event, database
resource use, and the size of audit/outbox/delivery relations. Replay cleanup
does not delete durable audit events, outbox payloads, or delivered identity
markers. Those tables still require storage capacity planning. Do not truncate
delivery markers to reclaim space: that removes durable delivery history and
is outside the supported recovery contract.

## Multiplex record subscriptions

`GET /api/realtime` opens one stream and sends `PB_CONNECT` with a `clientId`. `POST /api/realtime` replaces that client's complete subscription list; an empty list unsubscribes everything. Record topics use a collection name, optional `/recordId` or `/*`, and optional `filter`, `expand`, and `fields` query parameters. Their payload is `{action, record}` and their SSE event name is the exact subscription string. Guest and user collection subscriptions require the live collection opt-in and current list/view rules. Administrator and scoped service access use their own live admission grants. Collection-shaped application broadcasts use the same collection admission; reserved application topics keep their separate contract.

The client ID in `PB_CONNECT` is not a replay position and is not sent as an SSE `id`. Multiplex numeric IDs are safe **checkpoints across every subscribed record topic**, including the `cursor` frames sent after polling. Delivery merges topic pages in sequence order and advances only through the minimum completely read watermark. If one mutation matches several topics, its checkpoint advances after the last matching topic is written. A disconnect between matches can therefore repeat a previously observed match on replay; consumers must tolerate at-least-once delivery and must not discard a multiplex message merely because its SSE ID repeats. The checkpoint is not a unique message ID.

Reconnect with the last numeric checkpoint, then resubmit subscriptions using the new `clientId`. Initial connections without a checkpoint start at the subscription's current head. Before the first numeric checkpoint has been received, reconnecting is another fresh subscription; replay of that initial gap is not guaranteed. Application topics do not advance the durable checkpoint. Subscription replacement preserves positions for unchanged record topics; newly added topics start at the current head. Reauthentication with a new credential for the same principal preserves those positions, while a guest-to-auth upgrade initializes the new principal's subscriptions at their current heads.

An expired reconnect checkpoint returns HTTP 410 from subscription replacement without changing client state; an invalid checkpoint returns 400. If retention expires while a stream is active, it receives `PB_ERROR` with `{"code":"cursor_expired"}` and closes. Invalid cursors use `invalid_cursor`; other delivery failures use `subscription_failed`. A transient database failure or lookup timeout includes `"retryable":true` in that frame. Clients can reconnect using their previous checkpoint. Reload records before starting fresh after retention expiry. Multiplex connections have the same 30-minute maximum lifetime and refreshed 10-second write deadlines as the collection stream. Slow consumers release both connection and broker admission when a write fails.

Relation filters that require current data and requested expansions use the stream's current request policy context. A failed current-record lookup, including a database timeout, closes the stream with `subscription_failed` without acknowledging the affected event; reconnecting can retry from the last received checkpoint. A successful relation-filter lookup with no match advances normally. If the current base row has been deleted or is no longer viewable, its already-authorized event snapshot can still be delivered without a live expansion; an operational lookup failure is not treated as that absence.

## Application topics

Extensions with the explicit `app-control` capability may call `subscriptionsBroker().publishApplication(rawTopic, data)` to publish an arbitrary JSON value. Clients subscribe through the same `GET`/`POST /api/realtime` connection using an exact topic in the reserved `@` namespace, for example `@app/notice`. A delivered frame has that topic as its SSE `event` and the published JSON as `data`; it has no record action, record projection, or durable cursor.

Application topics are 2–256 bytes, start with `@`, and cannot contain whitespace, control characters, `?`, `#`, or `*`. Topic spelling is exact. `publishApplication` accepts any nonempty UTF-8 raw topic up to 188 bytes and maps it once to `@app/` plus unpadded base64url UTF-8 bytes. SDK `subscribeApplication(rawTopic, listener)` performs the same mapping and reports the original raw topic to the listener. This makes `@topic/한 글? #` and a raw topic which itself starts with `@app/` distinct, without double-decoding or treating a raw prefix as a namespace. The namespace is deliberately separate from collection names: an unprefixed topic remains a collection subscription, so a missing, renamed, or denied collection cannot be used as an unguarded broadcast channel. PocketBase accepts bare arbitrary broker names; PgBase intentionally differs here and reserves them for collection authorization. Unknown encoded application topics are accepted until an authorized extension publishes the exact name. The broker accepts at most 128 clients, 1000 topics per replacement, 256 queued messages per client, and 64 KiB of valid JSON per application message. A full client queue drops the new message; disconnecting a stream removes its broker client and subscriptions.

Each application delivery rechecks the current authenticated user, service token, or administrator session before writing, including messages queued before credential revocation. Queued messages for a topic removed by subscription replacement are discarded. Application topics bypass collection polling and remain connected when no record topics are present. Collection-shaped application messages retain collection record/list/view authorization; only the reserved `@` namespace uses the application-topic path.
