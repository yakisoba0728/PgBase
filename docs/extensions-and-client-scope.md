# Extension and client scope

PgBase embeds Go hooks and scheduled jobs in the application process. Before-mutation hooks execute in the record transaction and can reject a mutation; transactions may retry, so hook code must be deterministic and must not send external messages or perform irreversible effects. After-commit hooks run after a successful commit, are best effort, and cannot roll back it. Use an outbox consumer with idempotent external operations for durable effects. Batch operations share one transaction and publish their effects only on commit.

No embedded JavaScript engine is included in this milestone. An in-process JavaScript VM would require a separate module-loading, cancellation, memory, and API compatibility contract. Go extensions are trusted application code and have the same filesystem/network access as the host application; they are not a sandbox. Handler contexts are cooperative cancellation, not a way to kill uncooperative Go code. Scheduled jobs include fencing and a stable run identity; handlers must make repeated external actions idempotent. See [local jobs](local-jobs.md).

The TypeScript SDK is the supported client implemented here. Its package can be built, tested, packed and installed locally without publishing to npm. It wraps PgBase's HTTP contracts rather than claiming drop-in compatibility with PocketBase SDKs. A Dart SDK is deferred; Dart clients can call the documented HTTP endpoints directly, but no generated or maintained Dart client is claimed.

The [OpenAPI route contract](openapi.routes.json) is generated from the actual route registry and checked by a unit test. It documents paths, methods, optional module availability, and authentication admission. It intentionally does not infer a fixed record schema for dynamically defined collections or describe every payload. Feature documentation and TypeScript types supply the implemented payload contracts. Regenerate after route changes with:

```sh
PGBASE_UPDATE_OPENAPI=1 go test ./internal/api -run TestOpenAPIRouteContract
```
