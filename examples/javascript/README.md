# JavaScript extension example

This example is a normal external Go application: `main.go` imports only the public `pgbasecli` package and uses the standard PgBase CLI. Its bundle adds authenticated task routes, exact-number record writes, streamed file reads, a mail hook, a record observation hook, a retry-safe durable job, and a reversible JavaScript user migration. The browser page checks a record, strong ETag, exact JSON number, file bytes, a JavaScript route write, and a realtime event.

Prepare an isolated PostgreSQL database and the normal PgBase settings described in [the quickstart](../../docs/quickstart.md). Create an `n6_tasks` managed collection with required text `title`, JSON `metadata`, and file `attachment`; grant the test user the needed record and file rules. Provision a least-privilege service-token subject that can create `n6_tasks` records for the `n6_delivery` job. Configure a file driver and SMTP before enabling the matching capabilities.

From this directory:

```sh
export PGBASE_JS_DIR="$PWD/extension"
export PGBASE_JS_CAPABILITIES=records,files,mail,log
export PGBASE_JS_JOB_SERVICE_SUBJECTS='{"n6_delivery":"00000000-0000-4000-8000-000000000001"}'

go run . js check
go run . js types ./pgbase.d.ts
go run . migration list --js-dir "$PGBASE_JS_DIR"
go run . migration up --js-dir "$PGBASE_JS_DIR"
go run . serve --js-dir "$PGBASE_JS_DIR"
```

Replace the placeholder service subject with the UUID created for this application. The migration creates `app.n6_delivery_receipts` and its Down removes that table. The durable job intentionally throws after its first committed record create; its retry uses the same `occurrenceKey` and requires a replay before it succeeds. The next schedule receives a new key and creates a new delivery record. This demonstrates database idempotency, not exactly-once external delivery.

Serve `public/` from the configured PgBase public directory, or from a separate development origin listed in `PGBASE_CORS_ALLOWED_ORIGINS`. Same-origin requests are the default. For a separate origin, set the deployment-owned `apiBase` constant near the top of `public/app.js` to your disposable server's origin, without a trailing slash:

```js
const apiBase = "http://127.0.0.1:8090";
```

Migration: the previous `?api=...` page URL parameter is intentionally ignored. A shared link must not select the destination for entered passwords or bearer tokens. Configure the trusted destination in the served asset instead; use HTTPS when the page and API are not on loopback development hosts.

Enter an existing disposable user's credentials. Shared record/file IDs are optional; when present, the page first verifies state created by another SDK before running its own route, file, and SSE flow. Use only isolated development credentials in this page.

Run the browser script's destination and realtime cleanup regressions from the repository root with `node --test examples/javascript/test/browser-security.test.mjs`.

`routes.js` conditionally omits the optional upload checksum when absent. The native bridge uses a strict JSON codec, so an explicitly assigned `undefined` property is invalid; see [JavaScript extensions](../../docs/javascript.md) for the complete runtime, authority, migration, cancellation, and resource contract.
