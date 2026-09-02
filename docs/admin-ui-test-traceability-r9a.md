# Admin UI test traceability — R9a

Plan section 10 forbids deleting a test before a table connects its observable
contract to a replacement. This is that table for the R9a admin UI deepening.
Every row names a test as it existed at `618c4ea`, the behaviour it pinned, and
the test that pins that behaviour now. Replacement titles are quoted exactly as
they appear in the tree, so this table can be diffed against
`grep -rn "^\s*it(\|^\s*it\.each(" ui/admin/src`.

## Replacement surfaces

| Surface | File | What it may assert |
|---|---|---|
| Route | `ui/admin/src/routing/admin-location.test.tsx` | pure route parse/format/identity/canonicalisation, both history adapters, the late-result guard and the identity-guarded state container |
| Value | `ui/admin/src/backend/ports.test.ts` | the opaque values — catalog lookup, plan identity, record precondition, create attempt |
| Conformance | `ui/admin/src/backend/conformance.test.ts` | semantic input/output, cancellation and typed errors, run against the HTTP adapter *and* the scripted-memory adapter |
| Wire | `ui/admin/src/backend/http-backend.test.ts` | HTTP-adapter only: path, method, status, headers, CSRF, idempotency, precondition, duplicate JSON keys, lossless bodies |
| Designer | `ui/admin/src/designer/collection-designer.test.tsx` | `CollectionDesigner` interface: draft, preview staleness, destructive confirmation, delete plan, per-resource isolation |
| Draft | `ui/admin/src/designer/collection-draft.test.ts` | the pure collection default and relation input contracts |
| Workspace | `ui/admin/src/workspace/record-workspace.test.tsx` | `RecordList` and `RecordEditor` |
| Codec | `ui/admin/src/workspace/record-codec.test.ts` | pure record value contracts: tri-state draft, per-type encoding, relation values |
| Picker | `ui/admin/src/relation/relation-picker.test.tsx` | the `RelationPicker` interface: search, cursor pagination, label hydration, keyboard activation, selection |
| Relation query | `ui/admin/src/relation/relation-query.test.ts` | the pure target lookup contracts: search plan, candidate decoding, draft selection, hydration filter |
| DOM | `ui/admin/src/app.test.tsx`, `ui/admin/src/auth/session.test.tsx`, `ui/admin/src/pages/*.test.tsx` | thin smoke: the page renders the module state and forwards actions, driven by the scripted backend and a memory history |
| E2E | `ui/admin/e2e/onboarding/*.spec.ts`, `ui/admin/e2e/authenticated/*.spec.ts` | real backend browser workflow |

A row marked **deleted with the capability** removes a test because plan
section 5 removes the unused production capability it pinned; the row says
which capability went and where the surviving behaviour is asserted.

## `src/api/client.test.ts` (deleted) → Wire, Conformance

| Test at 618c4ea | Behaviour pinned | Replacement |
|---|---|---|
| round-trips JSON numbers that are unsafe for JavaScript Number | responses parse through `lossless-json`, not `JSON.parse` | Wire "reads and writes bodies losslessly" |
| retains response status and headers without changing the existing data-only API | `requestWithResponse` exposed status and headers to callers | **deleted with the capability**: `APIClient.requestWithResponse` is gone and every header the UI needs is consumed inside the adapter. The one header a caller depended on is pinned by Wire "reads a strong ETag into the record version" |
| retains error response headers for failed preconditions | `APIClientError.headers` | **deleted with the capability**: no caller read error headers. The 412 contract survives as Conformance "reports a record another writer changed as stale" |
| rejects a 2xx whose body cannot be parsed instead of resolving without data | an unreadable 2xx is a failure | Wire "treats an unreadable success body as a failure rather than an empty result" |
| still resolves an empty successful body as undefined | 204 / empty body is a success | Wire "accepts an empty successful body" |
| reports the transport status when a failure body is unreadable | an unreadable error body still fails with its status | Wire "reports a failure whose body cannot be read" |
| reads every response through text() so the shipped parser is never bypassed | `response.json()` is never called | Wire "never calls response.json() so the shipped parser is not bypassed" |
| notifies the session owner for every protected-request 401 | a protected 401 ends the session | Wire "announces the session expiry for a protected 401 and stays silent for the authentication flow"; Conformance "announces the session expiry for a protected request the backend rejects" |
| does not notify the session owner for public authentication-flow 401 responses | status/session/setup/login 401 is silent | Wire "announces the session expiry for a protected 401 and stays silent for the authentication flow" |

## `src/api/records.test.ts` (deleted) → Wire, Value

| Test at 618c4ea | Behaviour pinned | Replacement |
|---|---|---|
| keeps a create key for identical retries and rotates it after input changes or success | `CreateAttempt` key lifecycle | Value "reuses one idempotency key per unchanged create body and rotates it otherwise" |
| returns GET ETags while preserving lossless record numbers | a GET yields a version and lossless numbers | Wire "reads a strong ETag into the record version" |
| sends idempotency and precondition headers on safe mutations | `Idempotency-Key`, `If-Match`, `X-PgBase-CSRF`, exact body | Wire "sends idempotency, precondition and CSRF headers with the exact request body" |
| preserves canonical single, null, and ordered multiple relation values across API boundaries | relation values survive the round trip byte for byte | Wire "keeps relation values byte-identical across a round trip" |
| fails closed for an absent or non-strong ETag: %s (4 cases) | weak, absent and list-form ETags are rejected | Wire "fails closed for an absent or non-strong ETag: %s" (same 4 cases) |

## `src/auth/session.test.tsx` (rewritten) → DOM, Conformance

| Test at 618c4ea | Behaviour pinned | Replacement |
|---|---|---|
| clears the authenticated shell when the server session expires | the expiry deadline returns the shell to sign-in | DOM `session.test.tsx` "clears the authenticated shell when the server session expires" — same behaviour, now on a fake clock the test advances instead of a 250 ms real deadline |
| clears authenticated state when a protected request returns 401 | a protected 401 signs the administrator out | DOM `app.test.tsx` "signs the administrator out when a protected request reports the session expired"; Conformance "announces the session expiry for a protected request the backend rejects" |

New rows the old file did not cover, kept because the port now distinguishes
them: DOM `session.test.tsx` "explains a %s resume before asking for
credentials" (2 cases) and "sends an unset instance to first-run setup".

## `src/app.test.tsx` (rewritten) → Route, Designer, Workspace, DOM

| Test at 618c4ea | Behaviour pinned | Replacement |
|---|---|---|
| resets records state when popstate changes the collection resource | changing collection resets definition, page, filter, sort and cursor | Workspace "resets definition, page, filter, sort and cursor when the collection changes"; DOM `app.test.tsx` "swaps the record list between collections as history moves" |
| ignores a completed record mutation after popstate selects another record | a late record mutation does not steer the new resource | Workspace "drops a save result that arrives after the editor moved to another record"; Route "goes false once the outlet is reused for another identity or unmounted" |
| ignores a completed collection mutation after popstate selects another collection | a late delete result does not steer the new collection | Designer "drops a delete result that arrives after the designer moved to another collection" |
| does not let a completed create redirect an edit route after a new-to-edit transition | a late create does not redirect | Designer "drops a create result that arrives after the route moved to an edit page" |
| resets record drafts across new and edit resources and ignores the old create result | drafts do not leak between new and edit; a late create is ignored | Workspace "never carries a new-record draft into an existing record" and "drops a save result that arrives after the editor moved to another record" |

## `src/pages/collections-page.test.tsx` (rewritten) → DOM

| Test at 618c4ea | Behaviour pinned | Replacement |
|---|---|---|
| shows relation field counts in the collection catalog | field and relation counts render | DOM `collections-page.test.tsx` "lists collections in name order with their field and relation counts" |
| ends the loading state when the collections request fails at the network boundary | a transport failure ends the loading state | DOM `collections-page.test.tsx` "ends the loading state when the catalog cannot be read" |

## `src/pages/system-page.test.tsx` (rewritten) → DOM, Route, E2E

| Test at 618c4ea | Behaviour pinned | Replacement |
|---|---|---|
| navigates to the read-only redacted system status | System renders only the allowed fields and leaks no secret | DOM `system-page.test.tsx` "renders the operational status the backend reports and nothing else"; DOM `app.test.tsx` "moves between the collection list and the read-only system page"; E2E `system.spec.ts` "reports operational diagnostics without leaking a secret" |
| falls back safely for malformed encoded administrator paths | an undecodable path segment falls back to the collection list | Route "falls back to the collection list for the undecodable path %s" (3 cases); DOM `app.test.tsx` "falls back to the collection list for an undecodable administrator path" |

## `src/pages/login-page.test.tsx` (rewritten) → DOM, E2E

| Test at 618c4ea | Behaviour pinned | Replacement |
|---|---|---|
| shows validation errors and does not submit incomplete credentials | client-side required checks, focus, aria | DOM `login-page.test.tsx` "shows validation errors and does not submit incomplete credentials" |
| shows safe credential errors, clears the password, and disables the pending form | safe message, password cleared, pending disabled | DOM `login-page.test.tsx` "shows a safe credential error, clears the password and disables the pending form" |
| moves to the authenticated shell after a successful login | login reaches the shell | DOM `login-page.test.tsx` "moves to the authenticated shell after a successful login"; E2E `session.spec.ts` "signs out and back in against a real backend" |

## `src/pages/setup-page.test.tsx` (rewritten) → Wire, DOM, Conformance, E2E

| Test at 618c4ea | Behaviour pinned | Replacement |
|---|---|---|
| sends credential-included JSON requests and decodes field errors without retrying a POST | POST shape, credentials, content type, no retry | Wire "sends one credential-included JSON POST per setup attempt" |
| adds the CSRF header on logout and passes abort signals to session fetches | logout CSRF headers, abort signal forwarding | Wire "sends both CSRF headers on logout using the token the session issued" and "forwards the abort signal to the session probe" |
| reaches first-run setup through the unauthenticated backend status contract | a 404 `setup_required` reaches the setup screen | Conformance "resumes an unset instance as setup required"; DOM `setup-page.test.tsx` "reaches first-run setup and conceals the token until it is revealed" |
| conceals the setup token unless the administrator explicitly reveals it | the token field is a password until revealed | DOM `setup-page.test.tsx` "reaches first-run setup and conceals the token until it is revealed" |
| validates required values and matching passwords before calling the server | client-side validation and focus order | DOM `setup-page.test.tsx` "validates required values and matching passwords before calling the server" |
| allowlists server field errors, clears password fields, and focuses the first invalid field | only allowlisted messages surface; password fields clear | DOM `setup-page.test.tsx` "disables the pending form and allowlists the server's own failure message" |
| explains the password byte bounds before sending a request the server would reject | the byte-bound message | DOM `setup-page.test.tsx` "explains the password byte bounds before sending a request the server would reject" |
| stays on setup and shows a safe field error when the setup token is rejected | a rejected token keeps the screen and clears the password | DOM `setup-page.test.tsx` "stays on setup and explains a %s failure safely" (3 cases); Conformance "rejects a bad setup token and bad credentials with the invalid-credentials reason" |
| moves to the authenticated shell after a successful setup | setup reaches the shell | DOM `setup-page.test.tsx` "moves to the authenticated shell after a successful setup"; E2E `onboarding.spec.ts` |
| disables repeated logout and shows a safe failure without dropping the session | one logout at a time; a safe failure keeps the session | DOM `app.test.tsx` "refuses a second sign-out and keeps the session after a failed one" |

## `src/pages/collection-defaults.test.ts` (moved) → Draft

Moved verbatim to `src/designer/collection-draft.test.ts`; it now imports
`buildInput` from the designer module instead of through the create page's
re-export, which plan section 12 forbids. All four titles are unchanged:
"emits the exact server representation for every non-trivial default",
"rejects boolean and integer defaults that the backend will reject",
"emits numeric bounded relation members without draft-only state",
"rejects out-of-range, inconsistent, and required set-null relation drafts".

## `src/pages/collection-create-page.test.tsx` (deleted) → Designer, DOM, Conformance, Wire

| Test at 618c4ea | Behaviour pinned | Replacement |
|---|---|---|
| does not silently select self when the collection name is entered after choosing relation type | a relation is only self if it was chosen that way | Designer "keeps a relation target the administrator chose when the collection name changes" |
| keeps a chosen relation target when the typed collection name passes through it | a typed name passing an existing target does not retarget | Designer "keeps a relation target the administrator chose when the collection name changes" |
| retargets a relation chosen as self when the collection name changes | an explicit self target follows the name | Designer "retargets a self relation when the collection name changes" |
| builds a self relation from the live catalog and previews normalized relation SQL | the self target is offered from the catalog and the preview renders | DOM `collection-designer-page.test.tsx` "previews a self relation built from the live catalog" |
| keeps multiple relation bounds, required state, and uniqueness consistent | the field-editor coupling rules | Designer "keeps relation bounds, required and unique consistent" |
| previews normalized server output and applies the exact preview input and checksum | apply sends exactly what was previewed | Conformance "applies the approved plan under its own operation, name, checksum and base version"; Wire "posts the approved checksum and base version with the CSRF header"; Value "applies only under the operation, name, checksum and base version it was issued for" |
| invalidates changed previews and requires a fresh preview after a conflict | editing invalidates the preview; a conflict clears it | Designer "invalidates the preview as soon as the draft changes" and "names a %s %s conflict and clears the stale preview" (create rows) |

## `src/pages/collection-edit-page.test.tsx` (deleted) → Designer, Draft, Conformance, Value, DOM

| Test at 618c4ea | Behaviour pinned | Replacement |
|---|---|---|
| resolves persisted relation target IDs back to names and round-trips relation settings | id→name resolution and a lossless round trip | Designer "loads a persisted relation as a named target and round-trips it" |
| explains staged required relation migration and displays the backend validation code | the staged-migration note and the server's code | Designer "explains a staged required relation migration only for a field the collection does not have yet"; the note itself renders in DOM `collection-designer-page.test.tsx` |
| requires explicit confirmation before applying a change that deletes field data | the destructive confirmation gate | Designer "requires confirmation before a change that drops field data"; DOM `collection-designer-page.test.tsx` "gates a destructive change behind the confirmation the designer reports" |
| previews and applies a schema update with the update operation | the update operation reaches the port | Conformance "applies the approved plan under its own operation, name, checksum and base version" |
| round-trips now timestamp defaults without treating them as strings | `{kind:"now"}` survives | Draft "emits the exact server representation for every non-trivial default" |
| round-trips jsonb defaults, including string scalars, without changing their type | jsonb defaults keep their type | Draft "emits the exact server representation for every non-trivial default" |
| distinguishes a data-conflict 409 from a catalog-change 409 when applying | the 409 disambiguation by catalog version | Conformance "reports an apply that failed as %s"; Wire "re-reads the catalog only to separate a same-version conflict from a moved catalog"; Designer "names a %s %s conflict and clears the stale preview" (4 cases) |
| explains a 422 data-constraint rejection with the existing-data message | the data-blocked message and detail codes | Designer "explains a change existing data blocks with the offending detail codes" |
| requires confirming the deletion plan before dropping the collection | preview the deletion, then confirm | Designer "requires the deletion plan before dropping a collection"; DOM `collection-designer-page.test.tsx` "shows the deletion plan before dropping a collection and lets it be cancelled" |
| shows the stable collection-referenced code when a relation blocks deletion | the referenced message and its code | Conformance "reports a deletion the backend refuses because the collection is referenced"; Wire "never re-reads the catalog for a refused deletion"; Designer "explains a collection another collection still references" |
| reports missing collections safely | an unknown collection reports not found | Designer "reports an unknown collection as missing"; DOM `collection-designer-page.test.tsx` "reports a collection the catalog does not have" |
| distinguishes a %s catalog failure from a missing collection (2 cases) | a server failure and a transport failure both read as unavailable, not missing | Designer "tells a %s catalog failure apart from a missing collection" (same 2 cases) |
| treats an inherited collection name as missing on the collection editor | prototype keys are not collections | Value "never resolves an inherited property as a collection" |

## `src/pages/records-page.test.tsx` (rewritten) → Workspace, Codec, Wire, Value, DOM

| Test at 618c4ea | Behaviour pinned | Replacement |
|---|---|---|
| treats an inherited collection name as missing on the records page | prototype keys are not collections | Value "never resolves an inherited property as a collection"; Workspace "reports a collection the catalog does not have as a failed list" |
| treats an inherited collection name as missing on the record editor | as above, for the editor | Value "never resolves an inherited property as a collection"; Workspace "reports a record the catalog cannot place" |
| keeps edit actions hidden until the requested record has loaded | no editor affordance before the record has loaded | DOM `record-edit-page.test.tsx` "keeps edit actions hidden until the requested record has loaded" |
| lists first and next cursor pages and forwards applied filters and sort | cursor paging carries filter and sort | Workspace "carries the applied filter and sort into every page including the next cursor" and "cycles a sortable column through ascending, descending and unsorted"; Wire "asks for one bounded page and carries cursor, filter and sort in the query" |
| shows a safe alert when the server rejects the filter expression and clears it after success | a rejected filter alerts and clears on the next good page | Workspace "reports a rejected filter and clears the report after a good page"; Conformance "reports a filter the backend rejects as a rejected record request" |
| lists integers outside the JavaScript safe range without rounding them | lossless list rendering | DOM `records-page.test.tsx` "renders a value outside the JavaScript safe integer range without rounding it"; Wire "reads and writes bodies losslessly" |
| creates all eight JSON representations without rounding decimal strings | per-type encoding of every field type | Codec "encodes every field type exactly, without rounding a decimal string" |
| reuses a create key after ambiguous failures and rotates it when the input changes | the create key lifecycle through the editor | Workspace "retries an ambiguous create with the same idempotency key and rotates it when the draft changes" |
| omits untouched fields so database defaults can apply | an unset field is omitted | Codec "omits an unset field so the database default applies" |
| distinguishes a nullable boolean from a concrete false boolean | the tri-state draft | Codec "keeps a NULL boolean and a concrete false apart" |
| submits optional and required empty text as concrete strings instead of null | empty text is a string, not null | Codec "submits empty text as a concrete string rather than null" |
| accepts nil and mixed-case canonical UUID values and normalizes case | uuid normalisation | Codec "accepts nil and mixed-case canonical UUIDs and normalises the case" |
| rejects integers outside the PostgreSQL integer range before sending them | the integer range check | Codec "rejects the integer %s before sending it" (3 cases) |
| validates JSONB locally, sends partial updates, and shows safe 422 field errors | local JSON validation, a partial PATCH, per-field messages | Codec "reports invalid JSON against the jsonb field that holds it" and "sends only the fields an edit actually changed"; Workspace "sends only the changed fields in a patch and maps rejected codes to safe messages" |
| keeps a stale draft intact and requires an explicit reload after a 412 | a 412 preserves the draft; the reload is explicit | Workspace "keeps a stale draft and reloads the record only when asked" |
| names the text byte limit when the server rejects a value as too long | the limit follows the field | Codec "names the byte limit that applies to the field" |
| explains a delete the server refused because other records still reference it | the restricted-delete message | Workspace "explains a delete another record still references" |
| confirms deletion and handles a record deleted by another administrator | confirm, then a 404 | Workspace "reports a record another administrator already deleted" |
| does not delete until the administrator confirms | the confirmation gate | Workspace "does not delete until the administrator confirms"; DOM `record-edit-page.test.tsx` "does not delete until the administrator confirms" |

## `src/pages/record-relations.test.tsx` (deleted) → Workspace, Codec, Value, DOM

| Test at 618c4ea | Behaviour pinned | Replacement |
|---|---|---|
| only searches targets on explicit action, escapes text literals, and selects a single result | the picker searches on demand and escapes the literal | Picker "searches only when asked and sends one escaped, indexed lookup"; Relation query "looks up the first indexed text field and escapes the literal" |
| appends multi selections in result order without duplicates or exceeding maxSelect | ordered-unique multi selection | Codec "appends ordered unique selections up to maxSelect"; Picker "reports a duplicate selection instead of changing the draft" |
| falls back to an exact canonical UUID lookup when the target has no searchable text field | the uuid fallback lookup | Relation query "falls back to a canonical UUID lookup when the target has no searchable text field"; Picker "refuses a query the target cannot be looked up by" |
| creates canonical single and ordered multiple relation values while omitting untouched fields | relation create encoding | Codec "encodes a single relation and an ordered multiple relation" and "omits an unset field so the database default applies" |
| leaves unchanged relation values out of PATCH requests | relations respect the dirty set | Codec "sends only the fields an edit actually changed"; Workspace "sends only the changed fields in a patch and maps rejected codes to safe messages" |
| clears a single relation with null and a multiple relation with an empty ordered array | clearing semantics differ by cardinality | Codec "clears a single relation with null and a multiple relation with an empty ordered array" and "starts a multiple relation as a value because it has no NULL" |
| rejects invalid UUIDs, duplicates, and values outside relation bounds before making a request | local relation validation | Codec "rejects invalid, duplicate and out-of-bounds relation values before a request" and "requires a required relation the administrator left unset or null" |
| maps backend relation failures to safe field-specific messages | server relation codes become field messages | Codec "maps relation codes to the bound the administrator has to satisfy"; Workspace "sends only the changed fields in a patch and maps rejected codes to safe messages" |
| renders multiple relations in order and only offers sorting for an indexed single relation | list rendering and the sortability rules | DOM `records-page.test.tsx` "renders ordered relations and offers sorting only where the server can order"; Workspace "offers sorting only for columns the server can order on" |
| looks up source and target collections without traversing inherited catalog properties | catalog lookups are own-property only | Value "never resolves an inherited property as a collection" |

## Browser workflows

Playwright grows from one real-backend workflow to six. The onboarding project
keeps its own administrator-free backend; every other spec runs in the
authenticated project against a separate backend origin, and each names its
collections after the worker index and retry, so retries and parallel workers
never address the same rows.

| Spec | Workflow |
|---|---|
| `e2e/onboarding/onboarding.spec.ts` | first-run setup and a reload that keeps the session |
| `e2e/authenticated/session.spec.ts` | sign out, a rejected sign-in, and sign back in |
| `e2e/authenticated/collections.spec.ts` | create, edit and delete collections with a direct history jump between two of them |
| `e2e/authenticated/records.spec.ts` | scalar and relation record create, edit, filter and sort |
| `e2e/authenticated/stale-record.spec.ts` | two tabs on one record; the second recovers from the stale precondition |
| `e2e/authenticated/system.spec.ts` | the System page renders diagnostics and redacts secrets |

## F2 — searchable relation picker

F2 moved the picker out of `record-workspace.ts`, `record-codec.ts` and
`record-edit-page.tsx` into `src/relation/`. Every row above still names the
same assertion; only the surface changed. Two rows had to be reworded because
the behaviour they pin is now reached differently, so they are recorded here.

| Assertion before F2 | Behaviour pinned | Assertion now |
|---|---|---|
| Workspace "reports an empty result and a truncated result differently" expected `Showing the first 50 matching target records.` | a truncated page is distinguishable from an empty one | Picker, same test title, expects `Showing 1 target record. Load more to see the rest.` and `hasMore` — the page is no longer the last one the picker can reach |
| DOM `record-edit-page.test.tsx` clicked `button` named `Select release — <id>` | choosing a result puts its id in the draft | the same test clicks `option` named `release — <id>`; results are an ARIA listbox the textbox drives, so a result is an option rather than a button |
