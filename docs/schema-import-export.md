# Schema import and export

PgBase exports a portable `pgbase.schema/v1` JSON document from the administrator API. The document contains logical collection and field names, field constraints and presentation metadata, relation targets by collection name, compound indexes, declarative or SQL view definitions, public-user access, and typed record rules. It omits database UUIDs, physical identifiers, timestamps, and policy revisions.

View entries include a portable `dependsOn` name list. Import topologically orders those entries and rejects dependency cycles, so an export remains importable even when alphabetical order puts a dependent SQL view before its source view.

`GET /api/admin/schema/export` reads the catalog, access gates, and rules in one repeatable-read snapshot. `POST /api/admin/schema/import/preview` validates a document and returns its checksum, base schema version, policy checksum, and ordered DDL preview. Preview executes tentative DDL under a savepoint and rolls it back, which validates dependencies between newly imported collections and views without changing the database.

Import merges by default: when `deleteMissing` is omitted or `false`, omitted collections remain unchanged. Existing fields and indexes omitted from an imported collection remain unchanged even when `deleteMissing` is `true`. Existing fields match by name; a conflicting type is rejected, and portable import does not infer renames. This preserves record data and prevents a partial document from silently deleting columns.

Setting `deleteMissing: true` plans deletion only of catalog collections absent from the document's `collections` array. Preview orders those deletions by dependency and rejects the import if references prevent a valid deletion order. Omitting a system collection, or omitting any existing auth realm from `authCollections`, also rejects the import; these protected objects are not deleted. Approved collection deletions and other imported changes use the same guarded transaction.

Existing declarative views can receive the same named projection, filter, and field-presentation updates supported by the collection plan/apply API. The import planner infers the updated fields from the named source and preserves the view collection identity and matching field identities. When the source, projection, and filter are unchanged, presentation-only changes update catalog metadata without replacing the PostgreSQL view, so dependent views remain valid. Definition changes still validate the replacement against the current dependency graph and PostgreSQL before approval; a dependent view that prevents replacement rejects preview without changing the database. Existing SQL views can receive a new SQL definition through import.

Apply requires the exact preview `baseVersion`, `checksum`, document, `PgBase-Operation-ID`, and `Idempotency-Key`. PgBase re-locks and reloads the catalog and policy state, repeats inspection, then applies every DDL and metadata write in one transaction. The durable import operation row makes a retry with the same identity return the committed result after a lost response. Any validation or DDL failure rolls the whole import back.

The TypeScript administrator SDK offers object and raw JSON variants. The object API is limited to values JavaScript can represent precisely. `exportSchemaJSON()` followed by `previewSchemaImportJSON()` returns an `ApprovedSchemaImportPlan` that privately snapshots the exact JSON and reuses those bytes in `applySchemaImport()`, preserving integers outside JavaScript's safe range. Keep that approved plan object intact through apply; spreading or reconstructing it falls back to the object API contract.

`GET /api/admin/schema/import/operations/{id}` recovers the owner-scoped durable status after reconnecting. The administrator page retains the pending operation ID for the browser session and checks it again after reload.
An operation reports `running` only while its exact per-operation advisory lock is held. A claim left behind before execution reports `unknown`; retrying the original body and identity safely resumes it.
Status responses can omit `version` and `applied` until those values are known; the TypeScript `SchemaImportResult` models both fields as optional.

Unknown JSON members are rejected by the HTTP decoder. Unsupported field types or options produce validation errors instead of being omitted from the import.

The administrator UI schema transfer page can download an export, preview pasted JSON, review the affected collections and statement counts, and apply the approved preview.
