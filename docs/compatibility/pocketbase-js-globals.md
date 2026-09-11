# PocketBase JavaScript global compatibility (v0.40.3)

PgBase derives this compatibility surface from the pinned PocketBase `plugins/jsvm/binds.go`, `plugins/jsvm/internal/types/types.go`, and `core/app.go`. The compatibility runtime exposes real JavaScript objects backed by PgBase services. Host calls remain bounded by the script manifest, operator configuration, the current principal, and PostgreSQL authorization.

## Global namespaces

| Global | Compatibility behavior |
|---|---|
| `$dbx` | Typed, buildable expressions with variadic operands and `match()` support. Host adapters accept only validated expression ASTs and bind values as parameters; raw expressions do not create an authority bypass. |
| `$security` | Hash, HMAC, bounded random/regexp generation, JWT, and encryption helpers execute in the VM. Invalid or unbounded regexp inputs fail. |
| `$filepath`, `$os` | Pure path helpers execute in the VM. Glob, walk, file operations, commands, environment access, and process control require their manifest capability and operator allowlist/root. |
| `$http` | Performs bounded requests only to operator-approved origins with redirect revalidation. |
| `$filesystem` | `local` and `s3` return working PocketBase-shaped System, reader, writer, and attributes objects. Streams support seek, `writeTo`, `readFrom`, close, abort, and cancellation. Serve and thumbnails use invocation-bound handles and rooted/capability-checked native adapters. File descriptors from bytes, paths, URLs, and multipart bodies are validated before use. |
| `$apis` | Authentication middleware, static serving, gzip, body limits, auth responses, and record enrichment use the normal route admission and response pipeline. |
| `$mails` | All five auth mail helpers call server-side signing/template services. Scripts pass record identifiers and never receive token, link, password, or signing material. |
| API errors and constructors | PocketBase error types plus Record, Collection, FieldsList/field types, request/event/message, command, middleware, timezone, form-data, and SQL-expression constructors are callable. Mutable model state is held behind branded methods and is revalidated by native adapters. |

Permission denial is an implemented result: operations with side effects require both the declared manifest capability and the corresponding operator policy. `$app` schema/control operations additionally require an authenticated administrator principal. Scripts cannot increase their own capability ceiling.

## `$app`

The pinned `core.App` inventory contains 192 public methods. Every method has an exact lower-camel JavaScript entrypoint and a wrapper-to-host-to-native path. The row-by-row evidence is in [PocketBase `$app` method appendix](pocketbase-app-methods.md).

PgBase uses PostgreSQL as the single database authority. `AuxDB` variants therefore share PostgreSQL while preserving the advertised transaction callback boundary. A transaction callback receives an invocation-scoped token; nested record/model mutations use the same `pgx.Tx`, and commit, rollback, cancellation, and cross-invocation reuse are checked by the host.

Model operations accept PgBase's allowlisted Record, Collection, and Model descriptors. `SaveNoValidate` may skip opted-in user hooks, but it does not skip access checks, schema validation required for storage safety, or identity/file invariants.

Catalog and auth auxiliary methods expose their exact PocketBase names. Collection queries and external-auth expressions accept validated `$dbx` ASTs. External-auth bulk deletion is atomic and retains the last-login-method guard. Auth-record token lookup validates signature, expiry, token type, active record, and collection association; returned records omit credential and token material.

All `On*` methods register during bootstrap against operator-declared hook slots. Registrations use deterministic handler IDs, closures are rebuilt for each VM, and the registry freezes before invocation. Each exact name maps to a native phase, including OTP request/auth, record enrichment, collection requests, settings requests, model phases, backup, realtime, file, and mail hooks.

`$app.txInfo()`, `$app.newMailClient()`, and `$app.newFilesystem()` are exact aliases over their native compatibility facades. Bootstrap/reset and process control call operator-provided lifecycle callbacks and fail when the callback or permission is absent.

## Approved compatibility mappings

- Aux database methods use the same PostgreSQL authority rather than introducing SQLite.
- Raw Go pointers, database pools, secrets, and unrestricted SQL are not exposed to JavaScript.
- Process exit/restart and command execution require explicit operator callbacks or allowlists.
- Filesystem and HTTP access remain rooted, origin-bound, size-limited, and capability-gated.
