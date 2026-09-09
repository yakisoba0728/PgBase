# P7 local product contracts

This increment starts at PR #30 (`b942391`). The PocketBase comparison uses the checked-out v0.40.3 source snapshot. Implementation, acceptance, merge, and installation status are tracked separately in [TODO](../TODO.md). This document defines PgBase behavior; it does not claim PocketBase wire compatibility.

## Field presentation and visibility

Fields support `hidden`, `presentable`, and `help`. Help text is limited to 300 Unicode code points. A hidden field cannot also be presentable. Administrators can view and edit hidden values. Nonadministrator record responses omit hidden values, including expanded records and batch results. Explicit writes to hidden fields are rejected; hidden filter/sort paths and forward/reverse relation expansion cannot be used to recover their contents. Existing validation still runs before response projection.

Hidden file fields use the current catalog policy for uploads, staging, listing, deletion, token issuance, downloads, and transforms. Making a field hidden invalidates nonadministrator access through previously issued file tokens. Administrator downloads and backup inventory retain their existing behavior. Realtime events carry record metadata, rather than a hidden-field payload. The designer persists all three properties, the record form displays help, and relation selectors use presentable fields.

## Email OTP and impersonation

Email OTP login is available in the default and named authentication collections. It only authenticates existing active users and does not register an account or automatically mark an email address verified. Policies configure a 60–900 second expiry and 1–10 attempts. Request limits, one active challenge per user, resend/redeem serialization, and policy/user invalidation apply. Users with active TOTP still complete the MFA challenge. Tests use an actual local SMTP sink and PostgreSQL; real external SMTP accounts remain outside the authorized local-only scope.

An authenticated administrator can issue a marked impersonation session for an active user. The lifetime is 1–15 minutes and cannot exceed the issuing administrator session. Each authentication rechecks the administrator and originating session. Revocation or deactivation invalidates the impersonation token. Sensitive password, MFA, email, verification, and OAuth linking actions reject impersonation. The administrator UI requests five minutes, shows the temporary token, and supports dismissal; it does not replace the administrator session with that token. Named-realm clients retain their authentication collection URL, because public user responses do not add an `authCollectionId` field.

## Schema transfer and Go routes

[Schema import/export](schema-import-export.md) defines the versioned logical document, merge behavior, preview, transaction, and operation recovery contract. Logical names replace database identifiers; PgBase schema documents are not PocketBase collection JSON. Omitted collections, fields, and indexes remain intact. Import does not infer a rename or perform `deleteMissing` deletion.

[Go custom routes](custom-routes.md) exposes startup registration with explicit public, user, administrator, or service authentication. Existing request limits, recovery, logs, metrics, and hooks apply. Authenticated responses enforce `Cache-Control: no-store` even if a handler attempts to override it. Handlers remain responsible for their application-specific resource authorization. Streaming, WebSocket upgrades, and runtime route mutation are not part of this interface.

## Remaining scope

P7 does not complete editor, geoPoint, user-defined autodate, or general password fields; S3 settings/test UI; user-authored migration creation/up/down; the rest of the PocketBase hook surface; JSVM; Dart SDK; an arbitrary SQL console; or self-update. PostgreSQL, UUIDs, revocable sessions, ETags, cursors, and file-token URLs remain intentional PgBase contracts. External deployment, real OAuth applications and mail credentials, public package publishing, and operational service objectives require separate work.

## Acceptance evidence

New browser scenarios cover field presentation/help, schema export/import with exact JSON numbers and preserved records, authentication settings saves/reloads, impersonation creation/revocation, and response-loss recovery after a committed schema import. The complete administrator browser suite passed 15 scenarios; the subsequent P7 run passed all five scenarios including the added response-loss case. The UI unit suite passed 466 tests, typecheck and build passed, and the SDK suite passed four administrator tests plus its existing main-client checks.

Go unit/race, lint, vet, the OpenAPI registry contract, and historical migration checks passed. Migration SQL 1–28 is unchanged. A read-only dump of the actual local PostgreSQL 17 v28 database was restored into a separate disposable database, upgraded to v32, and passed `doctor`; the original installation remained available. A separate populated-v28 PostgreSQL regression proves that required metadata and record data survive and new field presentation properties default correctly.

The complete final PostgreSQL 18/MinIO race rerun passed with the skip policy, and all six local OS/architecture builds passed. PR checks, merge, and installation are tracked in TODO. The initial broad run exposed old-catalog capability detection and a historical test fixture rewind omission; both were corrected. A later backup acceptance run exposed a test poll that rejected the managed host's advertised activation transition. The poll now waits on the same durable job through that specific 503 response; the actual restore/activation test then passed five consecutive race runs.

## Measured structural scope

Compared with PR #30 `b942391`, runtime changes are +2303 / -141 lines (net 2162). Migration embedding adds a further 16 lines counted by the mechanical Go runtime metric but classified as support in the diff. These additions implement the approved P7 features, including schema import transaction/recovery, field visibility, OTP and impersonation, and authenticated custom routes. The import ledger is separate from planning; old-database capability detection shares one query with the previous field-options check. No test or explanatory comment was removed to fund the new scope.

| Metric | PR #30 | P7 measured ceiling |
| --- | ---: | ---: |
| Go runtime | 48,643 | 50,587 |
| UI runtime | 5,181 | 5,415 |
| Handwritten total | 53,824 | 56,002 |
| Data package group | 20,175 | 21,243 |
| Query package | 5,543 | 5,704 |
| Go interfaces | 43 | 45 |
| Go exported declarations | 803 | 829 |
| Go constructors | 39 | 39 |
| UI page state/effect calls | 90 | 97 |
| UI runtime exports | 176 | 187 |
| Production files over 800 lines | 0 | 0 |

Reproduce with `git add -N .` followed by `GOTOOLCHAIN=go1.26.8 go run ./tools/repostats --compare b942391 --pretty`. Limits equal the measurement and provide no additional headroom. Built-in routes are independently verified with all optional dependencies: 134 method-specific routes and 2 method-agnostic routes. The method-agnostic ceiling remains 3; up to 64 custom routes have a separate explicit bound.
