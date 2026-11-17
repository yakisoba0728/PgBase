# Architecture boundaries and migration notes

This continuation keeps PgBase's PostgreSQL transaction and authorization model while fixing concrete lifetime, arithmetic and allocation defects. It introduces no schema migration, new service, distributed cache or public route removal. System schema remains **67**. The supported deployment boundary remains one project database with consistent configuration/secrets and suitable shared object storage across application instances.

## Changes and their invariants

### Request identity belongs to an operation lifetime

The administrator UI already had session-bound transport work in this PR. The same rule now covers TypeScript and Dart SDK administrator responses and bearer-authenticated operations: an operation captures the authentication generation, and obsolete results cannot complete normal SDK processing under a replacement identity. Checks cover success/error bodies and file preparation. Login/refresh have an additional adoption boundary; Dart must also check after asynchronous credential persistence. This removes a race that a transport-only check cannot cover.

An operation's own successful token adoption advances its generation intentionally. Public login/register capture the generation at start; a successful competing adoption, clear, cookie import or refresh invalidates the older operation. Starting another public login alone does not advance it, so this is not a latest-started-login-wins policy. Administrator login/resume instead advance their local generation when started. Independent review reproduced simultaneous login completions and delayed Dart storage, leading to final adoption/completion regressions. The implementation does not add arbitrary microtask delays to guess scheduling.

The SDK cannot retract an already fulfilled result or erase an application's cache. Application code must respond to identity changes, clear identity-bound caches and subscriptions, and avoid publishing already-held results into a new session. Caller-owned direct `fetch`/Dart transport requests and previously issued download URLs are outside this local operation guard. The administrator SDK's local generation also cannot observe an unrelated tab's cookie change unless the instance resumes/logs in, nor can it cancel a browser's already in-flight Set-Cookie update.

TypeScript also composes the `RequestInit.signal` accepted by `send()` with its explicit request-option signal and keyed cancellation. Listener cleanup extends through body reading and runs on every exit. It uses the existing `AbortController` API; it does not raise the runtime floor to `AbortSignal.any`.

### Immutable catalog views remove per-row copying

`internal/query/codec.go` previously cloned and sorted the complete field list for every record. Committed catalogs already store fields in ordinal order. `fieldsByOrdinal` now returns a **borrowed, read-only view** when ordered, and retains a copy-and-sort fallback for unordered caller-owned schemas. All inspected callers only iterate/read that view; future callers must not mutate it. Scan destinations start with length three and the known `len(fields)+3` capacity, avoiding slice growth.

This changes internal allocation behavior, not field order, field visibility, cursor signatures or public types. Regression tests retain the unordered-input path and verify caller input is not mutated. Security independently reviewed the six current helper call sites and repeated focused race tests.

### Page budgets remain valid after hydration

Scalar root rows are already JSON-measured while scanning. When there is no hydration callback, the assembler can reuse that byte count instead of marshaling the unchanged map again. The cache is deliberately limited to `request.hydrate == nil`.

Hydration callbacks can change a record's wire representation and are remeasured before assembly. Expanded wire candidates pass a separate final assembler; field projection remains a later, unchanged stage. An integration regression injects a non-JSON `NaN` through hydration and requires final serialization to reject it, proving the old root measurement is not reused. Existing assembler tests retain the byte-budget checks. The optimization does not relax response bounds, ownership filters or cursor continuation rules.

### Dynamic mail data is separate from trusted markup

Administrator templates remain trusted configuration. `RenderHTML` parses that markup separately and supplies existing placeholders as string-valued functions. Go's HTML template engine applies escaping for text and attribute/URL contexts; dynamic values no longer become trusted HTML through concatenation. Preview, account-action, OTP and alert mail share the renderer.

Invalid contexts fail before a template is saved, and failed execution exposes no partial mail body. This is a rendering contract, not a public HTML sanitizer. The administrator preview's separate node sanitizer remains in place.

### Resource bounds are checked before work

Archive extraction compares an entry's declared size against the remaining expanded-size budget before adding it. Cursor signing checks remaining envelope space before hashing/allocation. These checks preserve existing format/size limits while expressing the arithmetic without an overflowing intermediate.

The TOTP parser accepts exactly six ASCII digits. Valid existing OTPs and enrolled secrets are unchanged; signed aliases are no longer accepted.

### Runtime trust and time rules are explicit inputs

The final image now obtains public CA/timezone data from a separate, hash-pinned stage. Package hashes were checked against Debian's signed security metadata; names, versions and architecture are checked at build time. Data and package metadata move together. The existing native PostgreSQL library closure is preserved.

`SSL_CERT_FILE` selects the actual bundle for native OpenSSL as well as Go. Native libpq's default `/usr/lib/ssl` aliases were absent from the configured image, even though Go could locate `/etc/ssl/certs/ca-certificates.crt`. An exact-library probe confirmed zero roots under the missing paths and 148 under the explicit bundle. The new setting reaches backup subprocesses; it does not turn off certificate/hostname checks.

The x/crypto 0.59 update changes the module inventory but not PgBase's used `argon2`, `blake2b`, `acme` or `acme/autocert` source: these subtrees are byte-identical to 0.58. No password hash format or Go runtime requirement changes. The obsolete OpenPGP directory was not linked by PgBase and is removed upstream; independently importing downstream applications must account for that removal.

## Module review and validation coverage

This ledger distinguishes direct changes/review from broader automated coverage. It is not an exhaustive architecture certification.

| Boundary | Direct work in this continuation | Evidence / remaining scope |
| --- | --- | --- |
| HTTP routing and response writers | Followed actual digest-writer callers behind reflected-XSS findings; retained existing route/auth boundaries | Concrete writer types defeat those scanner traces. Full backend tests cover wider routing; custom application handlers require their own review. |
| Administrator/public authentication | TOTP format, password-hash call chain, SDK operation/adoption/persistence lifetimes | Failing-before regressions, affected-package race/PG integration and independent SDK review. External identity providers remain unqualified here. |
| Query planning and projection | Page limit bounds, immutable field ordering, scan allocation, scalar/hydrated byte accounting, cursor full-width comparison | 500000-row paired profile, post-hydration serialization regression, existing assembler byte-budget tests and mixed HTTP owner/relation/cursor assertions. No change to transaction isolation or SQL plan was claimed. |
| Durable record/schema transactions | Inspected existing repeatable-read transaction wrapper and borrowed-transaction ownership; preserved atomic record/revision/outbox behavior | Existing integration/multiprocess/crash gates are reused. Lock hierarchy redesign, adversarial deadlock schedules and replica/sharding semantics are separate work. |
| Backup/restore and files | Extraction size/path/inventory boundary, malformed archive cleanup, shared image limits inherited from prior batch | Local storage integration/race passes; S3 and process recovery require CI results. Large external-store outage and restore-duration qualification remain open. |
| Realtime | Bounded fan-out fixture, saturated subscriber queues and existing slow-consumer transport tests | Component benchmark and 21 existing top-level race tests; this is not measured end-to-end distributed realtime capacity. |
| Mail/logging | Contextual rendering, SMTP header/DATA controls, actual redacting structured sinks | Local SMTP fixture and concrete scanner-trace review. Arbitrary custom handlers and external providers are operator boundaries. |
| Administrator UI | Text-only rendering boundary, deterministic pagination-reset fixture, retained session transport and design | 427 tests and typecheck locally; broad keyboard/screen-reader, all-browser and large-table UX audit remains open. |
| SDK/build/CI | TS/Dart lifecycle parity, generated SDK file parity, example destination boundary, accurate SARIF reporting | Standard SDK suites, independent race reproductions, browser contract and opt-in full source qualification. No package publication. |

## Compatibility and migration

| Change | Application action |
| --- | --- |
| Default SSR cookie `pgbase_auth` (earlier PR batch) | Update server/client import and export together, or explicitly select the legacy key on both sides. See [cookie contract](../../sdk-auth-cookies.md). Set Secure on HTTPS. Cached identity remains untrusted until server validation. |
| SDK `session_changed` rejection | Discard obsolete results and clear identity-bound caches/subscriptions. Reissue a read only under the intended current identity. A refresh for the same user also advances the generation. Use separate stores for independent identities. |
| Pending mutation interrupted by an auth change | Do not automatically retry it: the server may already have committed under the previous identity. Resolve uncertainty using the operation's normal idempotency/version contract. The client guard does not cancel committed server work. |
| JavaScript example `?api=` removed | Serve from the intended API origin, use a development reverse proxy, or edit the example's deployment-owned API constant. An untrusted shared link must not choose the credential destination. |
| Stricter mail template validation | Preview and re-save configured templates. Repair incomplete/ambiguous HTML contexts if a previously stored template now fails rendering. Existing valid placeholder names and intended markup remain supported. |
| Exact six-digit administrator OTP | Send six ASCII digits, including leading zeroes. Do not send signed numeric forms. Re-enrollment is unnecessary. |
| Query allocation changes | No client/schema migration. Treat borrowed field views as read-only in future internal code. Hydrated responses must still be measured after modification. |
| Updated public CA roots | Endpoints chaining solely to roots removed by Debian must present a currently trusted replacement chain. Explicit operator trust configuration remains possible; no verification bypass was introduced. |
| tzdata 2026f | Review regional schedules against the corrected time rules. UTC schedules are unchanged in the tested control; affected regional offsets differ from the old bundled data. |
| x/crypto 0.59 | PgBase has no OpenPGP import. A downstream Go application independently importing the removed `golang.org/x/crypto/openpgp` package must remove/replace its own use when module selection chooses 0.59. |

Both SDK README files contain operation-specific migration guidance and examples. TypeScript generated files are rebuilt from an empty `dist` directory and checked against tracked output in CI, preventing source-only fixes from shipping stale JavaScript.

## CI and operational use

The `full-qualification` PR label opts a candidate into the existing full backend, security/image, frontend, browser/recovery and source-analysis workflows plus the bounded mixed HTTP fixture. The parent workflow uses read/package-read permissions, no inherited production secrets, and isolated hosted fixtures. It does not merge, deploy, publish a tag/package or promote a release. Platform-specific release artifact tests are tests against disposable fixtures.

Routine PR checks remain available without that label. Workflow concurrency keys distinguish routine checks from reusable full qualification so one does not cancel the other. Source-analysis artifacts retain findings for classification; analysis success is never a zero-vulnerability assertion. Use validation.md for the exact tested runtime revision, any later documentation-only head, job outcomes and exclusions.

Source qualification explicitly disables PR diff-informed queries and rejects incremental/diff-range SARIF; `source-qualification` can request that analysis alone. A complete extraction count cannot substitute for proof that alert reporting covers the configured source. Image qualification similarly records actual public runtime assets as well as scanner metadata. E2E-generated disposable credentials are masked before they are exported to later workflow steps.
