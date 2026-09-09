package doctor

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

// Run inspects migration history, catalog metadata, physical collection
// tables, and record membership consistency. It does not alter database state.
func Run(ctx context.Context, pool *database.Pool, migrations *migrate.Runner) (Report, error) {
	report := Report{Findings: []Finding{}}
	if pool == nil {
		report.Findings = append(report.Findings, finding("database_unreachable", "PostgreSQL is unreachable"))
		return report, fmt.Errorf("doctor pool is nil")
	}
	if err := pool.Ping(ctx); err != nil {
		report.Findings = append(report.Findings, finding("database_unreachable", "PostgreSQL is unreachable"))
		return report, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	if migrations == nil {
		report.Findings = append(report.Findings, finding("migration_status_unavailable", "System migration status is unavailable"))
		return report, nil
	}
	status, err := migrations.Status(ctx)
	if err != nil {
		return report, fmt.Errorf("read system migration status: %w", err)
	}
	if migrationFinding, ok := migrationHistoryFinding(status); ok {
		report.Findings = append(report.Findings, migrationFinding)
	}
	if !status.Dirty && status.Current != status.Latest && !status.Compatible {
		report.Findings = append(report.Findings, finding("migration_not_current", "System migrations are not current"))
	}
	if status.Compatible {
		err := schema.PreflightCatalog(ctx, pool, nil)
		if errors.Is(err, schema.ErrCatalogLimitExceeded) {
			report.Findings = append(report.Findings, finding(
				"catalog_limit_exceeded",
				"Schema catalog exceeds 16 MiB; remove unused fields or collections during maintenance, then rerun pgbase doctor",
			))
			return report, nil
		}
		if errors.Is(err, schema.ErrCatalogInvalid) {
			appendUniqueFinding(&report, catalogInvalidCode, catalogInvalidMessage)
			err = nil
		}
		if err != nil {
			return report, fmt.Errorf("preflight schema catalog for doctor: %w", err)
		}
	}
	if err := pool.InTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	}, func(tx pgx.Tx) error {
		return inspectSnapshot(ctx, tx, status, &report)
	}); err != nil {
		return report, err
	}
	report.OK = len(report.Findings) == 0
	return report, nil
}

// inspectSnapshot holds every diagnostic that must observe one PostgreSQL
// snapshot. Its catalog query count is the bound doctor promises to operators,
// so it is exercised directly by the pgx-traced query-count test.
func inspectSnapshot(ctx context.Context, tx pgx.Tx, status migrate.Status, report *Report) error {
	if err := pinDeparseStyles(ctx, tx); err != nil {
		return err
	}
	schemaVersion, err := checkSchemaState(ctx, tx, report)
	if err != nil {
		return err
	}
	if err := checkPhysicalSchema(ctx, tx, schemaVersion, report); err != nil {
		return err
	}
	return checkRecordMembership(ctx, tx, status, report)
}

func migrationHistoryFinding(status migrate.Status) (Finding, bool) {
	if !status.Dirty {
		return Finding{}, false
	}
	if status.Issue == nil {
		return finding("migration_history_invalid", "System migration history is invalid"), true
	}
	switch status.Issue.Kind {
	case migrate.HistoryIssueChecksumMismatch:
		return finding("migration_checksum_mismatch", "System migration history does not match this binary"), true
	case migrate.HistoryIssueUnknownVersion:
		return finding("migration_unknown_version", "System migration history contains a version unknown to this binary"), true
	case migrate.HistoryIssueGap:
		return finding("migration_history_gap", "System migration history is not a contiguous prefix"), true
	case migrate.HistoryIssueDuplicateVersion:
		return finding("migration_history_duplicate", "System migration history contains a duplicate version"), true
	default:
		return finding("migration_history_invalid", "System migration history is invalid"), true
	}
}

func finding(code, message string) Finding {
	return Finding{Severity: "error", Code: code, Message: message}
}

// pinDeparseStyles fixes the literal styles PostgreSQL uses when it renders a
// stored expression back to text. Every drift check that compares pg_get_expr
// output against a pinned expectation depends on them: an operator or managed
// provider default such as `IntervalStyle=iso_8601` deparses the same CHECK as
// "P0Y0M0DT0H2M0S" instead of "00:02:00" and would report a healthy database
// as broken. The connection pool pins only application_name, timezone and
// statement_timeout, so this transaction cannot inherit a trustworthy value.
func pinDeparseStyles(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, "set local intervalstyle = 'postgres'"); err != nil {
		return fmt.Errorf("pin doctor interval style: %w", err)
	}
	if _, err := tx.Exec(ctx, "set local datestyle = 'ISO, MDY'"); err != nil {
		return fmt.Errorf("pin doctor date style: %w", err)
	}
	return nil
}

// checkSchemaState returns the published catalog version so the physical
// comparison below compiles the manifest for the same snapshot it just
// inspected, without reading the version a second time.
func checkSchemaState(ctx context.Context, db database.DBTX, report *Report) (int64, error) {
	var stateVersion int64
	if err := db.QueryRow(ctx, "select version from pgbase_meta.schema_state where singleton").Scan(&stateVersion); err != nil {
		return 0, fmt.Errorf("read schema state: %w", err)
	}
	var migrationVersion int64
	var userVersionTable bool
	if err := db.QueryRow(ctx, `select to_regclass('pgbase_meta.user_migration_schema_versions') is not null`).Scan(&userVersionTable); err != nil {
		return 0, fmt.Errorf("inspect user migration schema history: %w", err)
	}
	query := "select coalesce(max(version), 0) from pgbase_meta.schema_migrations"
	if userVersionTable {
		query = `select greatest(
			coalesce((select max(version) from pgbase_meta.schema_migrations),0),
			coalesce((select max(version) from pgbase_meta.user_migration_schema_versions),0)
		)`
	}
	if err := db.QueryRow(ctx, query).Scan(&migrationVersion); err != nil {
		return 0, fmt.Errorf("read schema migration history: %w", err)
	}
	if stateVersion != migrationVersion {
		report.Findings = append(report.Findings, finding("schema_state_stale", "Schema state does not match schema migration history"))
	}
	return stateVersion, nil
}

const (
	catalogInvalidCode    = "catalog_invalid"
	catalogInvalidMessage = "Schema catalog metadata is invalid"
)

// checkPhysicalSchema validates the catalog-to-relation-metadata seam and then
// compares the schema planner's own compiled contract for that catalog with one
// bulk observation of PostgreSQL. Doctor deliberately does not restate what a
// collection table, index, constraint, trigger or function should look like:
// the planner that creates those objects publishes the manifest, so drift
// diagnosis cannot fall behind schema creation.
func checkPhysicalSchema(ctx context.Context, db database.DBTX, schemaVersion int64, report *Report) error {
	catalog, err := readCatalogRows(ctx, db)
	if err != nil {
		return err
	}
	bindings, relationCatalogPresent, err := readRelationBindings(ctx, db)
	if err != nil {
		return err
	}
	relations := classifyRelations(catalog, bindings, report)
	if !relationCatalogPresent {
		return nil
	}

	manifest, compiled, err := expectedPhysicalSchema(ctx, db, schemaVersion, report)
	if err != nil || !compiled {
		return err
	}
	snapshot, err := readPhysicalSnapshot(ctx, db, manifest.ManagedNamespaces())
	if err != nil {
		return err
	}
	comparePhysicalSchema(manifest, snapshot, relations, report)
	return checkCompanionIndexes(ctx, db, schemaVersion, manifest, report)
}

// expectedPhysicalSchema compiles the manifest inside doctor's own snapshot. A
// catalog this binary cannot compile is a finding rather than an error: the
// operator needs the rest of the report, and PreflightCatalog has usually
// already reported the same condition. Only a PostgreSQL or cancellation
// failure stops the run, because those mean the snapshot itself is unusable.
func expectedPhysicalSchema(
	ctx context.Context,
	db database.DBTX,
	schemaVersion int64,
	report *Report,
) (schema.ExpectedPhysicalSchema, bool, error) {
	catalog, err := schema.NewStore(db).ReadCatalog(ctx, schemaVersion)
	if err == nil {
		var manifest schema.ExpectedPhysicalSchema
		manifest, err = schema.ExpectedPhysicalSchemaForCatalog(catalog)
		if err == nil {
			return manifest, true, nil
		}
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) || ctx.Err() != nil {
		return schema.ExpectedPhysicalSchema{}, false, fmt.Errorf("compile expected physical schema: %w", err)
	}
	appendUniqueFinding(report, catalogInvalidCode, catalogInvalidMessage)
	return schema.ExpectedPhysicalSchema{}, false, nil
}

// readCatalogRows keeps the raw collection/field join that the relation
// metadata checks need. schema.Store.ReadCatalog normalizes the same tables
// into a validated Catalog and therefore cannot show which invariant a broken
// catalog violated.
func readCatalogRows(ctx context.Context, db database.DBTX) ([]catalogRow, error) {
	rows, err := db.Query(ctx, `select collections.id, collections.name, collections.table_name,
		fields.id, fields.name, fields.column_name, fields.field_type,
		fields.required, fields.is_unique, fields.is_indexed, fields.has_default, fields.default_json::text
		from pgbase_meta.collections as collections
		left join pgbase_meta.fields as fields on fields.collection_id = collections.id
		order by collections.name, fields.ordinal`)
	if err != nil {
		return nil, fmt.Errorf("read schema catalog: %w", err)
	}
	defer rows.Close()
	catalog := make([]catalogRow, 0)
	for rows.Next() {
		var row catalogRow
		if err := rows.Scan(
			&row.collectionID, &row.collection, &row.table,
			&row.fieldID, &row.field, &row.column, &row.fieldType,
			&row.required, &row.unique, &row.indexed, &row.hasDefault, &row.defaultJSON,
		); err != nil {
			return nil, fmt.Errorf("scan schema catalog: %w", err)
		}
		catalog = append(catalog, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema catalog: %w", err)
	}
	return catalog, nil
}
