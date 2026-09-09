# Lifecycle extension contract

This document maps pgBase's extension points to the pinned PocketBase v0.40.3
source in `.local/upstream/pocketbase`. The mapping is semantic: event names and
payloads are intentionally pgBase-native and are not wire-compatible with
PocketBase's Go or JavaScript hook APIs.

All pgBase hooks take one immutable registry snapshot. `Before` callbacks run in
priority order and may reject the operation. `After` callbacks observe the final
result with a detached, five-second context; their errors cannot undo work.
Callbacks recover panics at the registry boundary. A callback that runs inside a
retryable transaction must avoid external effects; an `After` callback is emitted
only after commit outcome resolution.

| PocketBase v0.40.3 surface | pgBase surface | Status and exact difference |
| --- | --- | --- |
| `OnBootstrap` | `app.boot` | **Complete semantic counterpart.** Runs before `Start`/`Run` activates background services. A veto prevents startup. New constructs database-backed resources earlier than PocketBase Bootstrap, so this is runtime bootstrap rather than constructor interception. |
| `OnServe` | `app.serve` | **Complete for owned server.** Runs for `Run` before listener/background activation. It can veto startup and inspect mode/address, but routes and server options are sealed at `New`; use `CustomRoutes` to extend routing. `Start` has no serve event because its caller owns transport. |
| `OnTerminate` | `app.terminate` | **Complete with cleanup-safe difference.** Runs once when `Close` starts. A Before error is returned from `Close` but cannot veto resource cleanup. After observes the combined cleanup error. Abrupt process death cannot guarantee delivery. |
| Generic model create/update/delete and collection proxy hooks | `collection.create`, `.update`, `.delete`; `record.before`, `record.after` | **Complete for persisted collections and records, narrower model domain.** Collection Before runs after a durable operation claim but before DDL. After runs only after commit/ambiguous-commit resolution; a replay emits no duplicate hook. Record validation may repeat on transaction retry and record After is commit-only. pgBase has no arbitrary ORM model surface. |
| Model/record/collection validate, execute, success and error subphases | The paired events above | **Applied differently.** pgBase deliberately exposes one veto point and one final observation rather than each PocketBase internal phase. Schema preview is pure and emits no mutation hook. |
| Record CRUD and collection HTTP request hooks, collections import, batch | `record.list`, `.view`, `.create`, `.update`, `.delete`, `.batch`; `schema.import.preview`, `.apply` | **Complete for PgBase API endpoints, narrower model domain.** Each runs after route admission and before the typed service call. Hooks may change documented request input, which the service then revalidates and authorizes; actor, record and idempotency identities are observations. Batch emits once for the complete decoded request. Preview may change the document; guarded apply is observational/veto-only so its checksum and replay identity remain intact. After receives the final service result/error, including replay resolution. |
| `OnMailerSend` and purpose-specific mail hooks | `mail.send`, `mail.otp`, `mail.verification`, `mail.password_reset`, `mail.email_change`, `mail.smtp_test` | **Complete for PgBase mail purposes.** Generic `mail.send` keeps its existing mutable `*MailMessage` payload. Purpose hooks receive a credential-free `MailEvent` immediately before transport delivery; subject/body/recipient may change but the service-selected purpose and all SMTP configuration remain immutable. After sees the delivery result. |
| Filesystem writer/delete and file request hooks | `file.upload`, `.delete`, `.token`, `.download`, `.transform` | **Complete for attachment mutation and read paths.** Token/download/transform events run after file authorization and before signing, opening, or derivative creation. `FileAccessEvent` is an immutable, credential-free observation; hooks can veto but cannot change file identity, principal, variant, object key, or storage configuration. Object cleanup remains governed by reference checks and GC locks. |
| Auth with password/OAuth2/OTP and verification/reset/email-change request hooks | `auth.password`, `auth.oauth.begin|complete`, `auth.otp.request|complete`, purpose-specific request/confirm hooks, `auth.impersonate` | **Complete for listed pgBase flows.** Payloads contain realm, flow, user/provider identity and secret-free result metadata. Opaque-session authentication has no refresh hook because pgBase does not mint PocketBase refresh JWTs. |
| Realtime connect/message/subscribe hooks | `realtime.connect`, `.subscribe`, `.message` | **Complete semantic counterpart for pgBase SSE.** Connect spans one admitted SSE lifetime, subscribe wraps every authorization/replay poll, and message wraps each durable record event write. Cursor heartbeats are protocol control frames and emit no message hook. This is not PocketBase's client-mutated topic subscription model. |
| Settings hooks | `storage.settings.list`, `.update`, `.test` | **Complete for persisted file-storage settings.** Update Before excludes credentials and vetoes before revision/driver mutation; After observes the committed controller result. Startup-only application options have no settings hooks. |
| Backup create/restore hooks | `backup.create`, `.restore`, `.delete` | **Complete for managed operations.** Before runs before job reservation or repository deletion. Create/restore After waits for execution and final receipt persistence, including restore activation; delete After observes repository deletion. Events contain backup/job identifiers and no database URL or repository credentials. Upload/list/download remain ordinary request operations. |

## Authentication hook design

Auth hooks use typed, secret-free payloads containing realm/collection ID,
flow, subject/user ID when known, provider name when relevant, and final session
metadata without raw tokens, OTP codes, passwords, OAuth codes, or provider
credentials. The kinds are `auth.password`, `auth.oauth.begin`,
`auth.oauth.complete`, `auth.otp.request`, `auth.otp.complete`,
`auth.verification.request|confirm`, `auth.password_reset.request|confirm`,
`auth.email_change.request|confirm`, and `auth.impersonate`.

Before wraps the public service operation and therefore runs before normalization,
rate admission, durable state changes, or provider calls. Non-disclosing request
vetoes use the same HTTP error shape for known and unknown accounts. Each After is
emitted once when the delegated operation returns after its transaction commit.
OAuth provider network work is included in `auth.oauth.complete`; its final error
distinguishes failure without exposing credentials or provider response bodies.
Session metadata is copied without bearer material.

## Validation

Unit tests cover hook ordering, paired outcomes, startup failure, cancellation,
and cleanup-safe termination. PostgreSQL integration tests prove record CRUD input
rewrites are revalidated, batch vetoes do not execute service work, panics produce a
paired error, schema-import vetoes precede apply, and file token/download/transform
events run through the real HTTP API. Existing collection, auth, realtime, storage,
schema-fanout and backup tests retain their respective transactional evidence.
