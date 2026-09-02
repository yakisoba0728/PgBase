package doctor

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
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
			report.Findings = append(report.Findings, finding(
				"catalog_invalid",
				"Schema catalog metadata is invalid",
			))
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
		if err := pinDeparseStyles(ctx, tx); err != nil {
			return err
		}
		if err := checkSchemaState(ctx, tx, &report); err != nil {
			return err
		}
		if err := checkCollections(ctx, tx, &report); err != nil {
			return err
		}
		if err := checkCompanionIndexes(ctx, tx, &report); err != nil {
			return err
		}
		return checkRecordMembership(ctx, tx, status, &report)
	}); err != nil {
		return report, err
	}
	report.OK = len(report.Findings) == 0
	return report, nil
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

func checkSchemaState(ctx context.Context, db database.DBTX, report *Report) error {
	var stateVersion int64
	if err := db.QueryRow(ctx, "select version from pgbase_meta.schema_state where singleton").Scan(&stateVersion); err != nil {
		return fmt.Errorf("read schema state: %w", err)
	}
	var migrationVersion int64
	if err := db.QueryRow(ctx, "select coalesce(max(version), 0) from pgbase_meta.schema_migrations").Scan(&migrationVersion); err != nil {
		return fmt.Errorf("read schema migration history: %w", err)
	}
	if stateVersion != migrationVersion {
		report.Findings = append(report.Findings, finding("schema_state_stale", "Schema state does not match schema migration history"))
	}
	return nil
}

func checkCollections(ctx context.Context, db database.DBTX, report *Report) error {
	rows, err := db.Query(ctx, `select collections.id, collections.name, collections.table_name,
		fields.id, fields.name, fields.column_name, fields.field_type,
		fields.required, fields.is_unique, fields.is_indexed, fields.has_default, fields.default_json::text
		from pgbase_meta.collections as collections
		left join pgbase_meta.fields as fields on fields.collection_id = collections.id
		order by collections.name, fields.ordinal`)
	if err != nil {
		return fmt.Errorf("read schema catalog: %w", err)
	}
	catalog := make([]catalogRow, 0)
	for rows.Next() {
		var row catalogRow
		if err := rows.Scan(
			&row.collectionID, &row.collection, &row.table,
			&row.fieldID, &row.field, &row.column, &row.fieldType,
			&row.required, &row.unique, &row.indexed, &row.hasDefault, &row.defaultJSON,
		); err != nil {
			rows.Close()
			return fmt.Errorf("scan schema catalog: %w", err)
		}
		catalog = append(catalog, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate schema catalog: %w", err)
	}
	rows.Close()
	bindings, err := readRelationBindings(ctx, db)
	if err != nil {
		return err
	}
	relations := classifyRelations(catalog, bindings, report)

	expectedColumns := make(map[string]map[string]struct{})
	for _, row := range catalog {
		columns, ok := expectedColumns[row.table]
		if !ok {
			columns = map[string]struct{}{"id": {}, "created_at": {}, "updated_at": {}}
			expectedColumns[row.table] = columns
		}
		if row.column != nil && fieldUsesCollectionColumn(row, relations) {
			columns[*row.column] = struct{}{}
		}
	}

	tablePresent := make(map[string]bool)
	for _, row := range catalog {
		present, checked := tablePresent[row.table]
		if !checked {
			present, err = tableExists(ctx, db, row.table)
			if err != nil {
				return err
			}
			tablePresent[row.table] = present
			if !present {
				report.Findings = append(report.Findings, finding("collection_table_missing", "A catalog collection table is missing"))
				continue
			}
			if err := checkBaseColumns(ctx, db, row.table, report); err != nil {
				return err
			}
		}
		if !present || row.fieldID == nil || row.column == nil || row.fieldType == nil {
			continue
		}
		if *row.fieldType == "relation" {
			continue
		}
		if err := checkField(ctx, db, row.table, *row.column, *row.fieldType, *row.required, *row.unique, *row.indexed, *row.hasDefault, row.defaultJSON, report); err != nil {
			return err
		}
	}
	if err := checkRelationPhysicalSchema(ctx, db, relations, report); err != nil {
		return err
	}
	return checkUnexpectedPhysicalSchema(ctx, db, expectedColumns, report)
}

func checkUnexpectedPhysicalSchema(ctx context.Context, db database.DBTX, expectedColumns map[string]map[string]struct{}, report *Report) error {
	rows, err := db.Query(ctx, `select tables.table_name, columns.column_name
		from information_schema.tables as tables
		left join information_schema.columns as columns
			on columns.table_schema = tables.table_schema and columns.table_name = tables.table_name
		where tables.table_schema = 'app' and tables.table_type = 'BASE TABLE'
		order by tables.table_name, columns.ordinal_position nulls first`)
	if err != nil {
		return fmt.Errorf("read physical collection schema: %w", err)
	}
	defer rows.Close()
	orphanedTables := make(map[string]struct{})
	for rows.Next() {
		var table string
		var column *string
		if err := rows.Scan(&table, &column); err != nil {
			return fmt.Errorf("scan physical collection schema: %w", err)
		}
		expected, cataloged := expectedColumns[table]
		if !cataloged {
			if _, reported := orphanedTables[table]; !reported {
				report.Findings = append(report.Findings, finding("collection_table_orphaned", "A physical collection table is missing from the catalog"))
				orphanedTables[table] = struct{}{}
			}
			continue
		}
		if column != nil {
			if _, known := expected[*column]; known {
				continue
			}
			report.Findings = append(report.Findings, finding("collection_column_orphaned", "A physical collection column is missing from the catalog"))
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate physical collection schema: %w", err)
	}
	return nil
}

func tableExists(ctx context.Context, db database.DBTX, table string) (bool, error) {
	var exists bool
	if err := db.QueryRow(ctx, `select exists(select 1 from information_schema.tables where table_schema = 'app' and table_name = $1)`, table).Scan(&exists); err != nil {
		return false, fmt.Errorf("check collection table: %w", err)
	}
	return exists, nil
}

func checkBaseColumns(ctx context.Context, db database.DBTX, table string, report *Report) error {
	for _, expected := range []struct{ column, typeName string }{
		{column: "id", typeName: "uuid"},
		{column: "created_at", typeName: "timestamp with time zone"},
		{column: "updated_at", typeName: "timestamp with time zone"},
	} {
		column, typeName := expected.column, expected.typeName
		present, err := checkColumnType(ctx, db, table, column, typeName, report)
		if err != nil {
			return err
		}
		if present && column != "id" {
			var nullable string
			if err := db.QueryRow(ctx, `select is_nullable from information_schema.columns
				where table_schema = 'app' and table_name = $1 and column_name = $2`, table, column).Scan(&nullable); err != nil {
				return fmt.Errorf("read collection base column nullability: %w", err)
			}
			if nullable != "NO" {
				report.Findings = append(report.Findings, finding("collection_column_nullability_mismatch", "A collection base column has unexpected nullability"))
			}
		}
	}
	var hasPrimaryKey bool
	if err := db.QueryRow(ctx, `select exists(
		select 1
		from pg_constraint as constraints
		join pg_class as tables on tables.oid = constraints.conrelid
		join pg_namespace as schemas on schemas.oid = tables.relnamespace
		join pg_attribute as columns
			on columns.attrelid = tables.oid
			and columns.attname = 'id'
			and not columns.attisdropped
		where schemas.nspname = 'app' and tables.relname = $1
			and constraints.contype = 'p'
			and constraints.conkey = array[columns.attnum]::smallint[])`, table).Scan(&hasPrimaryKey); err != nil {
		return fmt.Errorf("check collection primary key: %w", err)
	}
	if !hasPrimaryKey {
		report.Findings = append(report.Findings, finding("collection_primary_key_missing", "A catalog collection primary key is missing"))
	}
	// Every collection the planner creates carries a (created_at, id) index and
	// the list kernel treats created_at as indexed unconditionally, so losing
	// this index turns every list request into a full-table top-N sort without
	// any other check noticing.
	var hasPageIndex bool
	if err := db.QueryRow(ctx, `select exists(
		select 1
		from pg_index as indexes
		join pg_class as tables on tables.oid = indexes.indrelid
		join pg_namespace as schemas on schemas.oid = tables.relnamespace
		join pg_class as index_tables on index_tables.oid = indexes.indexrelid
		join pg_am as access_methods on access_methods.oid = index_tables.relam
		join pg_attribute as created
			on created.attrelid = tables.oid
			and created.attname = 'created_at'
			and not created.attisdropped
		join pg_attribute as identifier
			on identifier.attrelid = tables.oid
			and identifier.attname = 'id'
			and not identifier.attisdropped
		join pg_opclass as created_opclass on created_opclass.oid = indexes.indclass[0]
		join pg_opclass as identifier_opclass on identifier_opclass.oid = indexes.indclass[1]
		where schemas.nspname = 'app' and tables.relname = $1
			and access_methods.amname = 'btree'
			and indexes.indisvalid and indexes.indisready and indexes.indislive
			and indexes.indpred is null and indexes.indexprs is null
			and indexes.indnkeyatts = 2 and indexes.indnatts = 2
			and indexes.indkey[0] = created.attnum
			and indexes.indkey[1] = identifier.attnum
			and indexes.indoption[0] = 0 and indexes.indoption[1] = 0
			and created_opclass.opcdefault and created_opclass.opcmethod = index_tables.relam
			and created_opclass.opcintype = created.atttypid
			and identifier_opclass.opcdefault and identifier_opclass.opcmethod = index_tables.relam
			and identifier_opclass.opcintype = identifier.atttypid)`, table).Scan(&hasPageIndex); err != nil {
		return fmt.Errorf("check collection page index: %w", err)
	}
	if !hasPageIndex {
		report.Findings = append(report.Findings, finding("collection_page_index_missing", "A collection list pagination index on (created_at, id) is missing"))
	}
	return nil
}

func checkField(ctx context.Context, db database.DBTX, table, column, fieldType string, required, unique, indexed, hasDefault bool, defaultJSON *string, report *Report) error {
	present, err := checkColumnType(ctx, db, table, column, expectedDataType(fieldType), report)
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	var nullable string
	var columnDefault *string
	err = db.QueryRow(ctx, `select is_nullable, column_default from information_schema.columns where table_schema = 'app' and table_name = $1 and column_name = $2`, table, column).Scan(&nullable, &columnDefault)
	if err != nil {
		return fmt.Errorf("read collection column nullability: %w", err)
	}
	if (required && nullable != "NO") || (!required && nullable != "YES") {
		report.Findings = append(report.Findings, finding("collection_column_nullability_mismatch", "Catalog and physical column nullability do not match"))
	}
	defaultMatches := hasDefault == (columnDefault != nil)
	if defaultMatches && hasDefault {
		defaultMatches = defaultJSON != nil && physicalDefaultMatchesCatalog(fieldType, *defaultJSON, *columnDefault)
	}
	if !defaultMatches {
		report.Findings = append(report.Findings, finding("collection_column_default_mismatch", "A catalog field default does not match the physical column"))
	}
	if unique || indexed {
		var exists bool
		err := db.QueryRow(ctx, `select exists(
			select 1
			from pg_index as indexes
			join pg_class as tables on tables.oid = indexes.indrelid
			join pg_namespace as schemas on schemas.oid = tables.relnamespace
			join pg_class as index_tables on index_tables.oid = indexes.indexrelid
			join pg_am as access_methods on access_methods.oid = index_tables.relam
			join pg_attribute as columns on columns.attrelid = tables.oid and columns.attname = $2 and not columns.attisdropped
			join pg_opclass as operator_classes on operator_classes.oid = indexes.indclass[0]
			where schemas.nspname = 'app' and tables.relname = $1
				and access_methods.amname = 'btree'
				and indexes.indisvalid and indexes.indisready and indexes.indislive
				and indexes.indpred is null and indexes.indexprs is null
				and indexes.indnkeyatts = 1 and indexes.indnatts = 1
				and indexes.indkey[0] = columns.attnum and indexes.indoption[0] = 0
				and operator_classes.opcdefault and operator_classes.opcmethod = index_tables.relam
				and operator_classes.opcintype = columns.atttypid
				and not indexes.indisprimary
				and indexes.indisunique = $3)`, table, column, unique).Scan(&exists)
		if err != nil {
			return fmt.Errorf("check collection index: %w", err)
		}
		if !exists {
			report.Findings = append(report.Findings, finding("collection_index_missing", "A catalog field index is missing"))
		}
	}
	return nil
}

func checkColumnType(ctx context.Context, db database.DBTX, table, column, want string, report *Report) (bool, error) {
	var got string
	err := db.QueryRow(ctx, `select data_type from information_schema.columns where table_schema = 'app' and table_name = $1 and column_name = $2`, table, column).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		report.Findings = append(report.Findings, finding("collection_column_missing", "A catalog collection column is missing"))
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read collection column type: %w", err)
	}
	if got != want {
		report.Findings = append(report.Findings, finding("collection_column_type_mismatch", "A catalog collection column has an unexpected type"))
	}
	return true, nil
}

func expectedDataType(fieldType string) string {
	switch fieldType {
	case "timestamptz":
		return "timestamp with time zone"
	case "integer":
		return "integer"
	case "bigint":
		return "bigint"
	case "numeric":
		return "numeric"
	case "boolean":
		return "boolean"
	case "text":
		return "text"
	case "uuid":
		return "uuid"
	case "jsonb":
		return "jsonb"
	default:
		return ""
	}
}
