# Extension and client scope

PgBase embeds Go hooks and scheduled jobs in the application process. Before-mutation hooks execute in the record transaction and can reject a mutation; transactions may retry, so hook code must be deterministic and must not send external messages or perform irreversible effects. After-commit hooks run after a successful commit, are best effort, and cannot roll back it. Use an outbox consumer with idempotent external operations for durable effects. Batch operations share one transaction and publish their effects only on commit.

Go embedding applications can register [custom HTTP routes](custom-routes.md) with explicit public/user/administrator/service authentication. They share the application's request middleware and expose a credential-free principal. This buffered `http.Handler` contract does not claim PocketBase route-group or event API compatibility.

An embedding application may also register `Options.UIExtensions` at startup. Each `pgbase.UIExtension` has an operator-chosen name and `fs.FS`; its files are available below `/admin/extensions/{name}/`, and an optional `main.js` is assembled into `/admin/extensions.js` after the administrator bundle. A `main.js` may call `window.pgbaseAdmin.registerScreen({ id, label, path, mount })` to add one screen below `/admin/ext/{id}`. `mount` receives the screen container and may return a cleanup callback. These browser contributions run only as assets the embedding operator registered in Go; JavaScript routes and browser code have no registration endpoint and cannot grant their own backend authority. Extension assets use the same `/admin` CSP and static-serving boundary as the administrator bundle, while API calls still require the existing administrator session and CSRF admission.

[Compiled user migrations](user-migrations.md) provide source-bound create/list/up/down. The [lifecycle mapping](lifecycle-parity.md) lists the additional app, schema, authentication, realtime, storage and backup hooks.

The current source includes an opt-in embedded JavaScript runtime for operator-reviewed bundles. It exposes documented record, file, mail, hook, job, and migration facades without bypassing PgBase service boundaries; see [JavaScript extensions](javascript.md). JavaScript and Go extensions are trusted application code with the host application's filesystem and network authority, not a tenant sandbox. Handler contexts are cooperative cancellation, not a way to kill uncooperative Go code. Scheduled jobs include fencing and a stable run identity; handlers must make repeated external actions idempotent. See [local jobs](local-jobs.md).

TypeScript and Dart SDKs are maintained local clients. Their packages can be built, tested, and installed locally without publishing to npm or pub.dev. They wrap PgBase's HTTP contracts rather than claiming drop-in compatibility with PocketBase SDKs; see [the TypeScript SDK](../sdk/typescript/README.md) and [the Dart SDK](dart-sdk.md). The current source also contains an opt-in administrator SQL console and local self-update tooling; their local acceptance is separate from remote merge, public release, or live installation.

The [OpenAPI route contract](openapi.routes.json) is generated from the actual route registry and checked by a unit test. It documents paths, methods, optional module availability, and authentication admission. It intentionally does not infer a fixed record schema for dynamically defined collections or describe every payload. Feature documentation and TypeScript types supply the implemented payload contracts. Regenerate after route changes with:

```sh
PGBASE_UPDATE_OPENAPI=1 go test ./internal/api -run TestOpenAPIRouteContract
```
