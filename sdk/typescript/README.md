# PgBase TypeScript SDK

Use an explicit auth realm for public users:

```ts
const members = client.realm("members");
const session = await members.register("person@example.com", "password");
await members.updateProfile({ displayName: "Person" });
const { authorizationUrl } = await members.oauthStart("github", "http://localhost:5173/callback");
location.assign(authorizationUrl);
```

`members.login` raises `MFAChallengeError` when a challenge must be completed. Tokens live only in the supplied `AuthStore`; pass `browserTokenStorage(localStorage)` only when persistent browser storage is intended.

The existing `TokenStorage` / `browserTokenStorage` contract stores only the opaque token; a newly constructed store has no cached record. To persist both together, opt into `new AuthStore(browserAuthStorage(localStorage))`, or implement the synchronous `AuthStateStorage` interface (`read`, `write`, `clear`). `browserAuthStorage` uses the separate default key `pgbase.user.auth`, stores one JSON snapshot, and ignores malformed stored snapshots. `save` writes token and record together; `clear` removes both. It does not migrate the existing `pgbase.user.token` key. Tokens remain opaque: `isValid` means a nonempty token exists, not that the server has verified it. Full-state persistence stores JSON-compatible record data; select the storage mechanism appropriate to your application.

Discover allowed sign-in methods and rotate a user token through the same explicit realm:

```ts
const methods = await members.methods();
if (methods.otpEnabled) await members.requestOTP("person@example.com");
const refreshed = await members.refresh(); // AuthStore now contains refreshed.token
```

Refresh tokens are single-use. The SDK saves a replacement only after it validates the complete response. If the refresh request may have reached the server but its response was lost, the old token might already be invalid and the replacement cannot be recovered; clear local auth and have the user sign in again rather than retrying with the old token.

## Request cancellation

Public-client GET/HEAD requests automatically cancel an older pending request with the same method and exact request path, including collection, record ID and serialized query/projection. The registration lasts through response body consumption. Different query strings remain independent; parameters are not reordered to canonicalize equivalent queries. Mutations participate only when given an explicit `requestKey`; cancellation cannot prove that a write was rolled back.

Record `list`, `get`, `getWithETag`, their JSON variants, and projection-bearing create/update methods accept `requestKey` in their options. Use `{ requestKey: "record-editor" }` to group requests and `client.cancelRequest("record-editor")` to stop the active one. `{ requestKey: null }` opts an individual request out; `client.autoCancellation = false` disables the registry globally. `client.cancelAllRequests()` stops registered requests. These options stay out of query strings. A caller-provided `AbortSignal` continues to work with either setting. Low-level `send` exposes the same `RequestOptions`; administrator requests and long-lived SSE streams do not use the public-client registry.

Parsed and exact-text response methods reject interrupted body reads. Parsed methods reject malformed success JSON with `invalid_response_body`; status 204 is a valid empty success. HTTP errors retain structured server details when valid JSON is available, and body transport failures retain their original error instead of masquerading as success.

## Realtime status and recovery

The existing `subscribe` callbacks keep receiving record/application events. Opt into shared-session status and asynchronous errors separately:

```ts
const stopObserving = client.realtime.onStatus((status) => {
  console.log(status.state, status.attempt, status.lastEventId);
  if (status.error) console.error(status.source, status.error);
});
await client.realtime.subscribe("notes/*", (event) => console.log(event));
// Later: stopObserving() removes this observer without unsubscribing records.
```

`onStatus` immediately delivers an immutable snapshot; `realtime.status` returns the current snapshot. States are `connecting`, `connected`, `reconnecting`, and `stopped`. Stopped reasons are `terminal`, `exhausted`, `unsubscribed`, or `reset`; the initial stopped snapshot has no reason. Reconnecting notices include the next attempt and original error. Empty sessions consume the reconnect budget even after a successful handshake; delivering an event to a subscribed topic resets it. A terminal `PB_ERROR` or HTTP failure preserves its `PgBaseError` code, including `cursor_expired` and `invalid_cursor`, and stops retries. Initial readiness still rejects as before.

Synchronous throws and rejected promises from event callbacks are reported with `source: "listener"` and `topic`, leaving delivery and connection state intact. Application helpers preserve the original application topic in these notices. Status observer failures are isolated and do not recursively produce notices. Async callbacks are not awaited, so an application needing ordered asynchronous processing must serialize its own work.

If a status observer changes the lifecycle synchronously, remaining observers receive the new snapshot; the superseded snapshot is not delivered afterward. Resetting or stopping from a reconnect notice interrupts its pending backoff immediately, including cancellation before the wait starts.

Ordinary unsubscribe/resubscribe retains the exact numeric replay cursor. When history has expired, call `await client.realtime.resetCursor()` to stop all subscriptions and discard that cursor, then reconcile current data and subscribe again. Reset does not fetch a snapshot, restore subscriptions, or recover expired events; the application owns reconciliation with concurrent writes. Observers remain registered across reset. Numeric checkpoint IDs may repeat across overlapping topic events and are never used to discard such events.

## Signup profiles

Password registration and OAuth completion accept typed custom profile fields:

```ts
await members.register<{ displayName: string; newsletter: boolean }>(
  "person@example.com", "password",
  { profile: { displayName: "Person", newsletter: false }, fields: "id,email" },
);
await members.oauthComplete("github", state, code, {
  profile: { displayName: "Person" },
});
```

`AuthCreationOptions<T>.profile` maps to the JSON request body's `fields` object. The existing `fields` option stays a response projection query parameter. The same option works in `oauthCompleteFromURL` and `oauthWithBrowser`, and existing `AbortSignal` argument forms remain accepted. Profile values use ordinary JavaScript JSON serialization; no implicit exact-number conversion is applied. The server validates custom fields and create rules during new-user creation. Omitted profile input preserves existing behavior; it cannot bypass required schema fields. Nonempty profiles on an existing OAuth login/link are rejected rather than updating that account. Signup responses remain sessions; read custom attributes through `members.profile()`.

## Exact JSON record I/O

Ordinary `list`, `get`, `create` and `update` methods use JavaScript objects and native JSON parsing/serialization. JSONB values outside JavaScript's number precision can therefore be rounded, including large integers and long decimals. For exact values, use the opt-in JSON text methods:

```ts
const records = client.records("measurements");
const pageJSON = await records.listJSON({ limit: 20 });
const { recordJSON, etag } = await records.getJSON("CaseSensitive01");

// Keep precise numbers in JSON text or use a lossless JSON library of your choice.
const inputJSON = '{"id":"CaseSensitive02","payload":{"count":9007199254740993,"ratio":0.123456789012345678901}}';
const created = await records.createJSON(inputJSON, "create-measurement-0001");
if (created.etag) {
  await records.updateJSON("CaseSensitive02", '{"payload":{"ratio":0.123456789012345678902}}', created.etag);
}
```

`listJSON` returns the complete page JSON envelope, including `items` and optional `nextCursor`. `getJSON`, `createJSON` and `updateJSON` return `{ recordJSON, etag }`: `recordJSON` is the complete record response body, with no additional envelope. Numbers remain exactly as received in the server's JSON text. Mutations send the supplied JSON object text verbatim; the server validates it. The server may normalize JSONB formatting, so equivalent JSON need not round-trip with identical whitespace or number spelling. Parsing these strings with native `JSON.parse`, or constructing them from already rounded JavaScript numbers, loses the precision benefit. Use a lossless parser to inspect page items while preserving exact values.

The JSON methods accept the same query/projection options and `AbortSignal` argument forms as their parsed counterparts. They use the same bearer authentication and `PgBaseError` status/details/ETag handling. A failed or interrupted response body rejects instead of returning partial JSON. Requests are not automatically retried. After an uncertain create response, explicitly retry the **same JSON body and idempotency key**. Updates use `If-Match`; after an uncertain update response, read the record again and reconcile its current ETag before retrying. Cancellation does not prove a mutation was rolled back. Batch, auth profile, and realtime methods retain their existing native JSON behavior.

## Text record identities

Record IDs default to UUIDs. Set a `recordId` policy when creating a collection to use exact, case-sensitive text IDs:

```ts
import type { CollectionChange, RecordIDPolicy } from "@pgbase/sdk";

const recordId: RecordIDPolicy = {
  kind: "text", minLength: 1, maxLength: 32, pattern: "^[A-Za-z0-9]+$",
};
const change: CollectionChange = {
  operation: "create", name: "notes", recordId,
  fields: [{ name: "title", type: "text" }],
};
// Use the administrator plan/apply flow below to create the collection.
await client.records<{ id: string; title: string }>("notes").create(
  { id: "CaseSensitive01", title: "An exact text ID" }, "create-note-0001",
);
```

`{ kind: "text" }` uses the server's default 15-character lowercase alphanumeric IDs and generator. Custom text policies require `minLength`, `maxLength` and `pattern`; supply `autogeneratePattern` to generate missing IDs, or provide `id` on every create. The server validates ID characters and policy limits. IDs are preserved exactly by the SDK, including relation IDs. A collection's record ID policy is immutable after creation; omit it on updates to retain the existing policy. UUID collections omit `recordId` in catalog/plan/export responses. Both ordinary and batch create inputs accept an optional string `id` for text collections.

Schema transfers also preserve authentication collections through optional `SchemaDocument.authCollections`. Each `SchemaDocumentAuthCollection` carries registration settings, nullable string rules, `SchemaDocumentAuthPolicy`, optional token TTLs, and optional `recordId`. Keep an auth entry paired by name with its `collections` entry (`type: "auth"`) and preserve the same text ID policy in both entries when transferring an exported document. The existing export and import preview/apply methods retain these values; importing into an existing auth collection cannot change its record ID policy. Documents without `authCollections` remain supported.

## Administrator schema and operations client

Use `PgBaseAdminClient` in a same-origin browser administrator tool. The browser keeps the session cookie; the client holds the CSRF token only in memory. It is separate from the public-user `PgBaseClient`.

`scaffolds()` returns schema-document templates: `base`, `view`, and `auth: { collection, authCollection }`. Pair the two auth templates by name in `SchemaDocument.collections` and `SchemaDocument.authCollections`; they are not plan/apply `CollectionChange` operations.

```ts
import { PgBaseAdminClient } from "@pgbase/sdk";
const admin = new PgBaseAdminClient(window.location.origin);
await admin.resume(); // or login(email, password)
const catalog = await admin.catalog();
const collection = catalog.collections.posts;
const change = {
  operation: "update" as const,
  name: collection.name,
  fields: collection.fields.map(({ id, name, type, required, unique, indexed, options, default: defaultValue, relation }) => ({
    id, name: name === "title" ? "headline" : name, type, required, unique, indexed, options,
    default: defaultValue,
    relation: relation ? {
      targetCollection: Object.values(catalog.collections).find(item => item.id === relation.targetCollectionId)!.name,
      cardinality: relation.cardinality, minSelect: relation.minSelect, maxSelect: relation.maxSelect, onDelete: relation.onDelete,
    } : undefined,
  })),
};
const planned = await admin.plan(change);
console.log(planned.preview.sql); // inspect the exact preview before applying
const result = await admin.apply(planned);
// Retry the same planned object if the response was lost; it reuses operation IDs.
console.log(result, await admin.operation(planned.operationID));
```

Existing field IDs preserve data when a field is renamed. Omitted options preserve the current constraints; `{}` clears them. `FieldOptions` covers text length/pattern/autogeneration, email/URL allow and deny domains, choices, numeric decimal bounds and JSONB multi-select counts. Required follows SQL NULL semantics for ordinary scalars; editor/password also reject empty strings, geoPoint rejects the zero point, and files require an attached value. See [advanced field contracts](../../docs/advanced-fields.md). The client also exposes `logs`, `jobs`, `configureJob` and `queueJob`.

## Storage administration and advanced fields

`FieldType` and `FieldOptions` include editor, geoPoint, autodate and password contracts. Password responses contain an empty string; autodates are generated by the server.

`PgBaseAdminClient` exposes `fileStorageSettings`, `updateFileStorage`, `testFileStorage` and their `backupStorage*` counterparts. File migration has preview/start/status/latest/resume/cancel methods. Generate one operation UUID before Start and reuse it after an uncertain response; recover with `fileStorageMigration(id)` instead of starting a second operation. Settings updates bind the current revision, and the API never returns the secret key.

See [file storage and migration](../../docs/storage-settings.md) and [independent backup S3 storage](../../docs/backup-s3.md) for destination changes and failure recovery.
