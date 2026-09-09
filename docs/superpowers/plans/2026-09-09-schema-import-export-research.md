# Schema import/export research

PocketBase's pinned `core/collection_import.go` imports collections inside one transaction, merges imported JSON onto existing collection models, preserves existing fields when `deleteMissing` is false, orders views after base collections, and runs collection validation after saving all candidates. Its HTTP form accepts arbitrary collection maps and an optional destructive `deleteMissing` flag.

PgBase's safety model is stricter: schema mutations use a catalog version, deterministic checksum, PostgreSQL advisory lock, repeatable-read transaction, metadata catalog projection, and a durable operation identity. Access gates and typed record rules live outside the catalog version. A faithful PgBase workflow therefore needs one portable typed document, a policy-state checksum, tentative dependency validation, and one atomic apply rather than an HTTP loop over collection creation.

The implemented v1 contract uses logical names and merge-only behavior. It stages newly imported base collections before relation updates, orders views last, and performs tentative DDL in a savepoint so SQL views over new collections can be inspected. Apply re-plans against the actual IDs allocated earlier in its transaction. Migration 31 adds a durable schema-import operation receipt without changing any historical migration.

Primary references inspected: `.local/upstream/pocketbase/core/collection_import.go`, `.local/upstream/pocketbase/apis/collection_import.go`, PgBase `internal/schema/service.go`, `operation_apply.go`, `planner.go`, `store.go`, `internal/recordaccess/rules.go`, and `internal/accessrule/rule.go`.
