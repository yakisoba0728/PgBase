# Declarative view updates through schema import

Development baseline: `abc94ef` on `codex/remaining-development`; this record describes subsequent changes.

## Product contract

Schema import uses the existing declarative-view planner for an existing named builder view. An imported `view` may change its named source projection and static filter, and its exported fields may change `hidden`, `presentable`, and `help` presentation metadata. Source-field inference remains authoritative for names, types, defaults, and constraints.

The update preserves the view collection UUID, its inherited record-ID policy, stored source records, and field UUIDs for projected fields whose name and type still match. Preview and apply compile the same imported change. Invalid sources, projections, filters, field presentation shapes, and replacements blocked by dependent views are rejected by the existing planner or tentative PostgreSQL DDL. Apply still requires the preview base version and checksum, and all DDL, metadata, access, and rule writes remain in one transaction.

An unchanged source, projection, and filter with only `hidden`, `presentable`, or `help` changes produces an update plan with an empty statement list. The optimization also compares the existing and inferred physical field count, names, column names, and types, so a source change earlier in the same import cannot turn a physical shape change into a metadata-only update. The normal planner still validates and infers the fields, while apply writes only metadata. This lets presentation changes succeed when another view depends on the physical builder view. The empty statement list and adjusted plan checksum are sealed by preview and recomputed during apply; definition changes retain their normal `DROP VIEW`/`CREATE VIEW` statements and dependency rejection.

The schema document remains a name-based merge. Omitted base fields and indexes are preserved, and `deleteMissing` retains its collection-only deletion scope.

## Regression coverage

`TestSchemaDocumentImportUpdatesDeclarativeView` runs preview and apply against PostgreSQL, changes projection, filter, and presentation, and verifies identity, text-ID policy, matching field identity, stored data, and filtered results.

`TestSchemaDocumentImportUpdatesDeclarativeViewPresentationWithDependentView` keeps the definition unchanged, changes presentation in a zero-DDL plan, and verifies that a dependent SQL view remains queryable with stable collection and field identities.

`TestSchemaDocumentDeclarativeViewUpdateRejectsInvalidDependentAndStalePlans` verifies invalid projections, dependent-view protection, stale-plan rejection, and preservation of the original query result.

`TestSchemaDocumentDeclarativeViewUpdateRollsBackAfterLateValidationFailure` forces a failure after the replacement DDL and verifies that both physical view and catalog metadata roll back atomically.

Targeted PostgreSQL integration command:

```powershell
docker exec pgbase-remaining-runner go test -tags=integration ./internal/schema -run '^TestSchemaDocument(ImportUpdatesDeclarativeView|DeclarativeViewUpdateRejectsInvalidDependentAndStalePlans|DeclarativeViewUpdateRollsBackAfterLateValidationFailure)$' -count=1
```

Observed result: PASS (`internal/schema`, 0.606s test runtime). The shared PostgreSQL 17.9 runner was reused without restart.
