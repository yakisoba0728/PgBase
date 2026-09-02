# Admin UI test traceability — R9a

Plan section 10 forbids deleting a test before a table connects its observable
contract to a replacement surface. This is that table for the R9a admin UI
deepening. Every row names the test as it existed at `618c4ea`, the behaviour it
pinned, and where that behaviour is pinned after the refactor.

Replacement surfaces:

| Surface | File | What it may assert |
|---|---|---|
| Route | `ui/admin/src/routing/admin-location.test.ts` | pure route parse/format/identity/canonicalisation, both history adapters |
| Conformance | `ui/admin/src/backend/conformance.test.ts` | semantic input/output, cancellation and typed errors, run against the HTTP adapter *and* the scripted-memory adapter |
| Wire | `ui/admin/src/backend/http-backend.test.ts` | HTTP-adapter only: path, method, status, headers, CSRF, idempotency, precondition, duplicate JSON keys, lossless bodies |
| Designer | `ui/admin/src/designer/collection-designer.test.ts` | `CollectionDesigner` interface: draft, preview staleness, destructive confirmation, delete plan, per-resource isolation |
| Workspace | `ui/admin/src/workspace/record-workspace.test.ts` | `RecordWorkspace` interface: list filter/sort/cursor, tri-state draft, codec, partial PATCH, stale recovery, relation picker |
| Codec | `ui/admin/src/workspace/record-codec.test.ts`, `ui/admin/src/designer/collection-draft.test.ts` | pure value contracts (defaults, relation bounds, record encoding) |
| DOM | `ui/admin/src/pages/*.test.tsx` | thin smoke: the page renders the module state and forwards actions |
| E2E | `ui/admin/e2e/*.spec.ts` | real backend browser workflow |

A row marked **deleted with the capability** removes a test because plan
section 5 removes the unused production capability it pinned; the column says
which capability went and where the surviving behaviour is asserted.

## `src/api/client.test.ts` → Wire + Conformance

| Test | Behaviour pinned | Replacement |
|---|---|---|
| round-trips JSON numbers that are unsafe for JavaScript Number | responses parse through `lossless-json`, not `JSON.parse` | Wire: "reads and writes bodies losslessly" |
| retains response status and headers without changing the existing data-only API | `requestWithResponse` exposed status/headers | **deleted with the capability**: `APIClient.requestWithResponse` is gone; every header the UI needs is now consumed inside the adapter. ETag consumption is pinned by Wire "reads a strong ETag into the record version" |
| retains error response headers for failed preconditions | `APIClientError.headers` | **deleted with the capability**: no caller read error headers. The 412 contract survives as Conformance "a changed record fails with the stale reason" |
| rejects a 2xx whose body cannot be parsed instead of resolving without data | unreadable 2xx is a failure | Wire: "treats an unreadable success body as a failure" |
| still resolves an empty successful body as undefined | 204/empty body is a success | Wire: "accepts an empty successful body" |
| reports the transport status when a failure body is unreadable | unreadable error body still fails | Wire: "reports a failure whose body cannot be read" |
| reads every response through text() so the shipped parser is never bypassed | `response.json()` is never called | Wire: "never calls response.json()" |
| notifies the session owner for every protected-request 401 | protected 401 ends the session | Conformance: "a protected 401 announces the session expiry" |
| does not notify the session owner for public authentication-flow 401 responses | status/session/setup/login 401 is silent | Wire: "keeps authentication-flow 401 responses silent" |

## `src/api/records.test.ts` → Wire + Conformance + Codec

| Test | Behaviour pinned | Replacement |
|---|---|---|
| keeps a create key for identical retries and rotates it after input changes or success | `CreateAttempt` key lifecycle | Codec: "reuses one idempotency key per unchanged create body" (now on the opaque `CreateAttempt`) |
| returns GET ETags while preserving lossless record numbers | GET yields a version and lossless numbers | Wire: "reads a strong ETag into the record version" |
| sends idempotency and precondition headers on safe mutations | `Idempotency-Key`, `If-Match`, `X-PgBase-CSRF`, exact body | Wire: "sends idempotency, precondition and CSRF headers" |
| preserves canonical single, null, and ordered multiple relation values across API boundaries | relation values survive the round trip byte for byte | Wire: "keeps relation values byte-identical across a round trip" |
| fails closed for an absent or non-strong ETag (4 cases) | weak/absent/list ETags are rejected | Wire: "fails closed for an absent or non-strong ETag" (same 4 cases) |

## `src/auth/session.test.tsx` → Conformance + DOM

| Test | Behaviour pinned | Replacement |
|---|---|---|
| clears the authenticated shell when the server session expires | expiry returns the shell to login | DOM `session.test.tsx`: "returns to sign-in when the session expires" (scripted backend, no wall-clock race) |
| clears authenticated state when a protected request returns 401 | a protected 401 signs the administrator out | DOM `session.test.tsx`: "returns to sign-in when the backend reports the session expired" + Conformance "a protected 401 announces the session expiry" |

## `src/app.test.tsx` → Route + Designer + Workspace + DOM

| Test | Behaviour pinned | Replacement |
|---|---|---|
| resets records state when popstate changes the collection resource | changing collection resets definition, page, filter, sort, cursor | Workspace: "changing collection resets definition, page, filter, sort and cursor atomically"; DOM `app.test.tsx`: "history navigation swaps the record list between collections" |
| ignores a completed record mutation after popstate selects another record | late record mutation does not steer the new resource | Workspace: "a late save result never lands on another record"; Route: "a guard captured for one identity goes false once the outlet moves" |
| ignores a completed collection mutation after popstate selects another collection | late delete result does not steer the new collection | Designer: "a late delete result never lands on another collection" |
| does not let a completed create redirect an edit route after a new-to-edit transition | late create does not redirect | Designer: "a late create result never redirects another route" |
| resets record drafts across new and edit resources and ignores the old create result | drafts do not leak between new/edit; late create ignored | Workspace: "a new-record draft never leaks into an existing record" and "a late create result never lands on another record" |

## `src/pages/collections-page.test.tsx` → DOM

| Test | Behaviour pinned | Replacement |
|---|---|---|
| shows relation field counts in the collection catalog | field and relation counts render | DOM `collections-page.test.tsx`: "lists collections with their field and relation counts" |
| ends the loading state when the collections request fails at the network boundary | a transport failure ends the loading state | DOM `collections-page.test.tsx`: "reports a failed catalog load instead of loading forever" |

## `src/pages/system-page.test.tsx` → DOM + E2E

| Test | Behaviour pinned | Replacement |
|---|---|---|
| navigates to the read-only redacted system status | System renders only the allowed fields, no secret leaks | DOM `system-page.test.tsx`: "renders the redacted system status"; E2E `system.spec.ts` against a real backend |
| falls back safely for malformed encoded administrator paths | an undecodable path segment falls back to the collection list | Route: "an undecodable segment falls back to the collection list" |

## `src/pages/login-page.test.tsx` → DOM

| Test | Behaviour pinned | Replacement |
|---|---|---|
| shows validation errors and does not submit incomplete credentials | client-side required checks, focus, aria | DOM `login-page.test.tsx`: unchanged assertions on a scripted backend |
| shows safe credential errors, clears the password, and disables the pending form | safe message, password cleared, pending disabled | DOM `login-page.test.tsx`: unchanged assertions on a scripted backend |
| moves to the authenticated shell after a successful login | login reaches the shell | DOM `login-page.test.tsx` + E2E `session.spec.ts` (logout/login against a real backend) |

## `src/pages/setup-page.test.tsx` → Wire + DOM + E2E

| Test | Behaviour pinned | Replacement |
|---|---|---|
| sends credential-included JSON requests and decodes field errors without retrying a POST | POST shape, credentials, content type, no retry | Wire: "sends one credential-included JSON POST per setup attempt" |
| adds the CSRF header on logout and passes abort signals to session fetches | logout CSRF headers, abort signal forwarding | Wire: "sends both CSRF headers on logout" and "forwards the abort signal to the session probe" |
| reaches first-run setup through the unauthenticated backend status contract | 404 `setup_required` reaches the setup screen | Conformance: "an unset instance resumes as setup required"; DOM `setup-page.test.tsx` |
| conceals the setup token unless the administrator explicitly reveals it | the token field is a password until revealed | DOM `setup-page.test.tsx`: unchanged |
| validates required values and matching passwords before calling the server | client-side validation and focus order | DOM `setup-page.test.tsx`: unchanged |
| allowlists server field errors, clears password fields, and focuses the first invalid field | only allowlisted messages surface | DOM `setup-page.test.tsx`: unchanged, driven by scripted `SessionError` reasons |
| explains the password byte bounds before sending a request the server would reject | byte-bound message | DOM `setup-page.test.tsx`: unchanged |
| stays on setup and shows a safe field error when the setup token is rejected | rejected token keeps the screen | DOM `setup-page.test.tsx`: unchanged; Conformance "a rejected setup token fails with the invalid-credentials reason" |
| moves to the authenticated shell after a successful setup | setup reaches the shell | E2E `onboarding.spec.ts` (already real-backend) |
| disables repeated logout and shows a safe failure without dropping the session | one logout at a time, safe failure keeps the session | DOM `app.test.tsx`: "refuses a second sign-out and keeps the session after a failure" |

## `src/pages/collection-defaults.test.ts` → Codec

| Test | Behaviour pinned | Replacement |
|---|---|---|
| emits the exact server representation for every non-trivial default | exact JSON for all default types | Codec `collection-draft.test.ts`: same assertion, imported from the designer module |
| rejects boolean and integer defaults that the backend will reject | local default validation | Codec `collection-draft.test.ts`: unchanged |
| emits numeric bounded relation members without draft-only state | relation bounds become numbers | Codec `collection-draft.test.ts`: unchanged |
| rejects out-of-range, inconsistent, and required set-null relation drafts | relation consistency rules | Codec `collection-draft.test.ts`: unchanged |

## `src/pages/collection-create-page.test.tsx` → Designer + DOM

| Test | Behaviour pinned | Replacement |
|---|---|---|
| does not silently select self when the collection name is entered after choosing relation type | self-relation is only what was chosen | Designer: "keeps a relation target the administrator chose when the collection name changes" |
| keeps a chosen relation target when the typed collection name passes through it | a typed name passing an existing target does not retarget | Designer: same test |
| retargets a relation chosen as self when the collection name changes | an explicit self target follows the name | Designer: "retargets a self relation when the collection name changes" |
| builds a self relation from the live catalog and previews normalized relation SQL | self target offered from the catalog, preview renders | DOM `collection-designer-page.test.tsx`: "previews a self relation" |
| keeps multiple relation bounds, required state, and uniqueness consistent | field-editor coupling rules | Designer: "keeps relation bounds, required and unique consistent" |
| previews normalized server output and applies the exact preview input and checksum | apply sends exactly what was previewed | Conformance: "apply carries the approved plan"; Wire: "posts the approved checksum and base version" |
| invalidates changed previews and requires a fresh preview after a conflict | editing invalidates the preview; a conflict clears it | Designer: "an edited draft invalidates the preview" and "a same-version conflict clears the preview" |

## `src/pages/collection-edit-page.test.tsx` → Designer + Conformance + DOM

| Test | Behaviour pinned | Replacement |
|---|---|---|
| resolves persisted relation target IDs back to names and round-trips relation settings | id→name resolution and round trip | Designer: "loads a persisted relation as a named target and round-trips it" |
| explains staged required relation migration and displays the backend validation code | staged-migration note, server code shown | Designer: "explains a staged required relation migration"; DOM `collection-designer-page.test.tsx` renders the note |
| requires explicit confirmation before applying a change that deletes field data | destructive confirmation gate | Designer: "requires confirmation before a change that drops field data" |
| previews and applies a schema update with the update operation | update operation reaches the port | Conformance: "an update plan applies under the update operation" |
| round-trips now timestamp defaults without treating them as strings | `{kind:"now"}` survives | Codec `collection-draft.test.ts`: "round-trips a now timestamp default" |
| round-trips jsonb defaults, including string scalars, without changing their type | jsonb defaults keep their type | Codec `collection-draft.test.ts`: "round-trips jsonb defaults" |
| distinguishes a data-conflict 409 from a catalog-change 409 when applying | 409 disambiguation by catalog version | Conformance: "a same-version conflict and a moved catalog are different reasons"; Designer: "names the two conflicts differently" |
| explains a 422 data-constraint rejection with the existing-data message | 422 message | Designer: "explains a data-blocked change" |
| requires confirming the deletion plan before dropping the collection | delete preview then confirm | Designer: "requires the deletion plan before dropping a collection" |
| shows the stable collection-referenced code when a relation blocks deletion | referenced message and code | Conformance: "a referenced collection fails with the referenced reason"; Designer: "explains a referenced collection" |
| reports missing collections safely | unknown collection reports not found | Designer: "reports an unknown collection as missing" |
| treats an inherited collection name as missing on the collection editor | prototype keys are not collections | Codec `catalog.test.ts`: "never resolves an inherited property as a collection" |

## `src/pages/records-page.test.tsx` → Workspace + Codec + Wire + DOM

| Test | Behaviour pinned | Replacement |
|---|---|---|
| treats an inherited collection name as missing on the records page | prototype keys are not collections | Codec `catalog.test.ts`: "never resolves an inherited property as a collection" |
| treats an inherited collection name as missing on the record editor | as above | Codec `catalog.test.ts`: same |
| keeps edit actions hidden until the requested record has loaded | no edit affordance before load | Workspace: "offers no editor actions until the record has loaded" |
| lists first and next cursor pages and forwards applied filters and sort | cursor paging carries filter and sort | Workspace: "carries the applied filter and sort into the next cursor page" |
| shows a safe alert when the server rejects the filter expression and clears it after success | rejected filter alert clears on success | Workspace: "reports a rejected filter and clears it after a good page" |
| lists integers outside the JavaScript safe range without rounding them | lossless list rendering | Wire: "reads and writes bodies losslessly"; DOM `records-page.test.tsx`: "renders a value outside the safe integer range" |
| creates all eight JSON representations without rounding decimal strings | per-type encoding | Codec `record-codec.test.ts`: "encodes every field type exactly" |
| reuses a create key after ambiguous failures and rotates it when the input changes | create key lifecycle through the editor | Workspace: "retries an ambiguous create with the same attempt" |
| omits untouched fields so database defaults can apply | unset fields are omitted | Codec `record-codec.test.ts`: "omits unset fields" |
| distinguishes a nullable boolean from a concrete false boolean | tri-state draft | Codec `record-codec.test.ts`: "keeps null and false apart" |
| submits optional and required empty text as concrete strings instead of null | empty text is a string | Codec `record-codec.test.ts`: "submits empty text as a string" |
| accepts nil and mixed-case canonical UUID values and normalizes case | uuid normalisation | Codec `record-codec.test.ts`: "normalises uuid case" |
| rejects integers outside the PostgreSQL integer range before sending them | integer range check | Codec `record-codec.test.ts`: "rejects out-of-range integers" |
| validates JSONB locally, sends partial updates, and shows safe 422 field errors | local JSON validation, PATCH is partial, field errors | Codec `record-codec.test.ts` + Workspace: "sends only dirty fields in a patch" and "maps rejected field codes to safe messages" |
| keeps a stale draft intact and requires an explicit reload after a 412 | 412 preserves the draft; reload is explicit | Workspace: "keeps a stale draft and reloads only on request" |
| names the text byte limit when the server rejects a value as too long | limit follows the field | Codec `record-codec.test.ts`: "names the byte limit that applies to the field" |
| explains a delete the server refused because other records still reference it | restricted delete message | Workspace: "explains a delete blocked by a referencing record" |
| confirms deletion and handles a record deleted by another administrator | confirm then 404 | Workspace: "reports a record another administrator already deleted" |
| does not delete until the administrator confirms | confirmation gate | DOM `record-editor-page.test.tsx`: "does not delete until the administrator confirms" |

## `src/pages/record-relations.test.tsx` → Workspace + Codec + DOM

| Test | Behaviour pinned | Replacement |
|---|---|---|
| only searches targets on explicit action, escapes text literals, and selects a single result | picker searches on demand and escapes literals | Workspace: "searches relation targets only on request and escapes the literal" |
| appends multi selections in result order without duplicates or exceeding maxSelect | ordered-unique multi selection | Codec `record-codec.test.ts`: "appends ordered unique selections up to maxSelect" |
| falls back to an exact canonical UUID lookup when the target has no searchable text field | uuid fallback lookup | Codec `record-codec.test.ts`: "falls back to a canonical UUID lookup" |
| creates canonical single and ordered multiple relation values while omitting untouched fields | relation create encoding | Codec `record-codec.test.ts`: "encodes single and ordered multiple relations" |
| leaves unchanged relation values out of PATCH requests | relations respect the dirty set | Workspace: "sends only dirty fields in a patch" |
| clears a single relation with null and a multiple relation with an empty ordered array | clearing semantics differ by cardinality | Codec `record-codec.test.ts`: "clears a single relation with null and a multiple relation with an empty array" |
| rejects invalid UUIDs, duplicates, and values outside relation bounds before making a request | local relation validation | Codec `record-codec.test.ts`: "rejects invalid, duplicate and out-of-bounds relation values" |
| maps backend relation failures to safe field-specific messages | server relation codes become field messages | Workspace: "maps rejected field codes to safe messages" |
| renders multiple relations in order and only offers sorting for an indexed single relation | list rendering and sortability rules | DOM `records-page.test.tsx`: "renders ordered relations and offers sorting only where the server supports it" |
| looks up source and target collections without traversing inherited catalog properties | catalog lookups are own-property only | Codec `catalog.test.ts`: "never resolves an inherited property as a collection" |

## Browser workflows

Playwright grows from one real-backend workflow to six. The onboarding project
keeps its own empty backend; every other spec runs in the authenticated project
against a separate backend origin, and each creates uniquely named collections
derived from the worker index so retries and parallel workers never collide.

| Spec | Workflow |
|---|---|
| `onboarding.spec.ts` | first-run setup and a reload that keeps the session |
| `session.spec.ts` | sign out and sign back in |
| `collections.spec.ts` | create, edit and delete a collection, including a direct history jump between two collections |
| `records.spec.ts` | scalar and relation record create, edit, filter and sort |
| `stale-record.spec.ts` | two tabs editing one record; the second recovers from the stale precondition |
| `system.spec.ts` | the System page renders diagnostics and redacts secrets |
