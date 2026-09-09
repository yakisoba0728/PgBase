# Advanced fields

PgBase supports four PocketBase-style advanced field types: `editor`, `geoPoint`, `autodate`, and `password`. They use the same schema preview/apply, portable schema document, record transaction, visibility, realtime, and batch boundaries as the existing field types.

## Text generation and domains

Text `options.autogeneratePattern` is a bounded generatable Go/RE2 expression.
Unbounded quantifiers (`*`, `+`, and `{m,}`) are rejected; use an explicit
upper bound such as `{0,6}`, `{1,6}`, or `{m,n}`.
When the field key is absent on create, PgBase uses cryptographic randomness to
produce a matching value. Any explicit value, including an empty string or
`null`, wins and is validated normally; updates never generate. Generated
values pass every configured text validator. Unique generated values use
candidate locks and bounded retries, and an idempotent create replay returns
the originally committed value. Text defaults and autogeneration cannot be
combined.

Email and URL text fields may set `options.allowedDomains` and
`options.deniedDomains` together. Schema input lowercases, trims and sorts both
lists. Entries match the exact host and its subdomains; deny wins before a
nonempty allow list is checked. Tightening either list preflights existing rows.

Text and editor filters support the terminal `:lower` modifier, for example
`title:lower = 'published'` and `owner.name:lower = 'ada'`. Typed record rules
accept the same modifier. It changes comparison only, not storage or sorting.

## Editor

An editor value is an HTML string. PgBase stores the text verbatim and does not sanitize or execute it. Applications must escape or sanitize HTML for their rendering context. The administrator rich editor treats `convertURLs` as an editing hint; the server never rewrites URLs.

`options.maxSize` counts UTF-8 bytes. Zero or omission means 5 MiB and the largest configurable value is 8 MiB. `required` rejects both `null` and the empty string. Optional editor fields use the PocketBase zero value `""`; their physical PostgreSQL column is non-null text with an empty-string default. Indexed or unique editor fields are allowed only when their effective maximum is at most 2,048 bytes, preserving PostgreSQL btree headroom.

The complete canonical record is limited to 8 MiB. This size uses compact JSON with HTML escaping disabled, so HTML characters are charged as their actual JSON bytes. HTTP request and response limits allow 48 MiB plus envelope space because a valid JSON wire encoder may escape one character into six bytes.

Editor fields support ordinary text comparisons, including `:lower`.

## Geo point

A geo point is exactly `{"lon": number, "lat": number}`. Both members are required, extra members and non-finite or nonnumeric values are rejected, longitude must be between -180 and 180, and latitude between -90 and 90. Omission uses `{lon:0,lat:0}`. Explicit `null` is rejected. An optional field accepts the zero point; a required field rejects it while accepting a point where either coordinate alone is nonzero.

Geo point columns are non-null `jsonb` with the zero-point default. They cannot be unique or have a custom default. An indexed field supports numeric filter and sort paths `field.lon` and `field.lat`, including paths through bounded single relations. Other JSON properties and deeper paths are rejected. Cursor identity binds the field and coordinate, so `lat` and `lon` cursors cannot be interchanged.

Typed record rules accept the same `field.lon` and `field.lat` paths. PocketBase permits general JSON-property traversal after a geoPoint field; PgBase deliberately limits this to the two defined coordinates.

## Autodate

An autodate field is a server-managed timestamp. At least one of `options.onCreate` and `options.onUpdate` must be true. Client values, `required`, `unique`, and custom defaults are rejected; indexing is supported. Every timestamp injected by one record transaction comes from the database statement clock. Create/update and atomic batch processing apply the same rule.

Changing the on-create/on-update settings affects later writes and does not rewrite existing records. Autodate values use the same RFC 3339 UTC response, filter, sort, and cursor contract as `timestamptz`.

## Password

A password field accepts plaintext only on an explicit write and stores an Argon2id hash. Responses, realtime payloads, audit artifacts, expansions, and batch results expose an empty string instead of plaintext or the hash. Password fields cannot be presentable, indexed, unique, filtered, sorted, or projected into declarative or SQL collection views.

`options.minLength`, `options.maxLength`, and the Go/RE2 `options.pattern` validate plaintext. Negative or reversed limits and invalid patterns fail schema preview. When `maxLength` is omitted, PgBase applies a 71-rune maximum. Required fields reject `null` and empty strings; optional fields use either to clear the password.

`App.VerifyRecordPassword(ctx, collection, recordID, field, plaintext)` lets trusted Go extensions verify a stored password without returning its hash. Extensions apply their application authorization before calling it; it creates no HTTP endpoint or user session. A missing record or cleared password does not match.

Argon2id is irreversible, so changing password length or pattern options cannot validate historical plaintext. The new policy applies to the next explicit password write; existing hashes remain valid and are never exposed for preflight inspection.

## Schema and visibility

All four logical types and their options round-trip through catalog responses and `pgbase.schema/v1` export/import. Editor and geoPoint fixed defaults may appear in exported documents and import only when they match their exact zero value. Unsupported options are rejected rather than discarded.

PocketBase mutation-key modifiers for numeric, select, relation and file fields
remain outside the PgBase contract. Exact bigint/numeric transport, ordered
relation writes with duplicate rejection, target authorization and attachment
lifecycle retain their existing explicit write interfaces. PgBase also does
not accept a text `:autogenerate` write key; absent-on-create is the sole
generation trigger.

`hidden` has the same meaning as on other fields: public users cannot read, filter, sort, or expand through the field, while administrators retain schema and record access. Password redaction applies to administrators as well and is stronger than hidden-field projection.

Declarative views may project editor and autodate fields. They reject password fields. SQL view inspection rejects a query only when PostgreSQL dependency metadata shows that the query references a password column; another unused password field in the source collection does not block the view. PostgreSQL result OIDs cannot distinguish editor from text or autodate from timestamptz, so newly inferred SQL-view output fields use the physical scalar type.

## Administrator editing

The rich editor supports text formatting, headings, lists, links, images, tables, code blocks, undo/redo and HTML source editing. Opening a stored document does not mark it changed or rewrite its source. Visual editing serializes the supported document model; source mode retains entered HTML. `convertURLs` removes the current origin from local links and images, keeping external origins intact. Record lists render a short text summary.

GeoPoint inputs provide numeric coordinates and an offline Natural Earth coastline map with keyboard selection and zoom. The map is approximate; numeric inputs allow precise coordinates. Coordinate sorting is available through separate longitude/latitude controls.

Autodates are read-only in record forms. Password inputs start as unchanged on edit, are omitted from record duplication and browser draft persistence regardless of field name, and are cleared after an accepted save without losing any newer edit made while saving.
