package doctor

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
)

// Run inspects migration history, catalog metadata, and physical collection
// tables. It does not alter database state.
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
	if status.Dirty {
		report.Findings = append(report.Findings, finding("migration_checksum_mismatch", "System migration checksums do not match this binary"))
	}
	if status.Current != status.Latest {
		report.Findings = append(report.Findings, finding("migration_not_current", "System migrations are not current"))
	}
	if err := checkSchemaState(ctx, pool, &report); err != nil {
		return report, err
	}
	if err := checkCollections(ctx, pool, &report); err != nil {
		return report, err
	}
	report.OK = len(report.Findings) == 0
	return report, nil
}

func finding(code, message string) Finding {
	return Finding{Severity: "error", Code: code, Message: message}
}

func checkSchemaState(ctx context.Context, pool *database.Pool, report *Report) error {
	var stateVersion int64
	if err := pool.QueryRow(ctx, "select version from pgbase_meta.schema_state where singleton").Scan(&stateVersion); err != nil {
		return fmt.Errorf("read schema state: %w", err)
	}
	var migrationVersion int64
	if err := pool.QueryRow(ctx, "select coalesce(max(version), 0) from pgbase_meta.schema_migrations").Scan(&migrationVersion); err != nil {
		return fmt.Errorf("read schema migration history: %w", err)
	}
	if stateVersion != migrationVersion {
		report.Findings = append(report.Findings, finding("schema_state_stale", "Schema state does not match schema migration history"))
	}
	return nil
}

func checkCollections(ctx context.Context, pool *database.Pool, report *Report) error {
	type catalogRow struct {
		collection, table         string
		column, fieldType         *string
		required, unique, indexed *bool
	}
	rows, err := pool.Query(ctx, `select collections.name, collections.table_name, fields.column_name, fields.field_type,
		fields.required, fields.is_unique, fields.is_indexed
		from pgbase_meta.collections as collections
		left join pgbase_meta.fields as fields on fields.collection_id = collections.id
		order by collections.name, fields.ordinal`)
	if err != nil {
		return fmt.Errorf("read schema catalog: %w", err)
	}
	catalog := make([]catalogRow, 0)
	for rows.Next() {
		var row catalogRow
		if err := rows.Scan(&row.collection, &row.table, &row.column, &row.fieldType, &row.required, &row.unique, &row.indexed); err != nil {
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

	tablePresent := make(map[string]bool)
	for _, row := range catalog {
		present, checked := tablePresent[row.table]
		if !checked {
			present, err = tableExists(ctx, pool, row.table)
			if err != nil {
				return err
			}
			tablePresent[row.table] = present
			if !present {
				report.Findings = append(report.Findings, finding("collection_table_missing", "A catalog collection table is missing"))
				continue
			}
			if err := checkBaseColumns(ctx, pool, row.table, report); err != nil {
				return err
			}
		}
		if !present || row.column == nil || row.fieldType == nil {
			continue
		}
		if err := checkField(ctx, pool, row.table, *row.column, *row.fieldType, *row.required, *row.unique, *row.indexed, report); err != nil {
			return err
		}
	}
	return nil
}

func tableExists(ctx context.Context, pool *database.Pool, table string) (bool, error) {
	var exists bool
	if err := pool.QueryRow(ctx, `select exists(select 1 from information_schema.tables where table_schema = 'app' and table_name = $1)`, table).Scan(&exists); err != nil {
		return false, fmt.Errorf("check collection table: %w", err)
	}
	return exists, nil
}

func checkBaseColumns(ctx context.Context, pool *database.Pool, table string, report *Report) error {
	for _, expected := range []struct{ column, typeName string }{
		{column: "id", typeName: "uuid"},
		{column: "created_at", typeName: "timestamp with time zone"},
		{column: "updated_at", typeName: "timestamp with time zone"},
	} {
		column, typeName := expected.column, expected.typeName
		if _, err := checkColumnType(ctx, pool, table, column, typeName, report); err != nil {
			return err
		}
	}
	var hasPrimaryKey bool
	if err := pool.QueryRow(ctx, `select exists(
		select 1
		from information_schema.table_constraints as constraints
		join information_schema.key_column_usage as columns
			on columns.constraint_name = constraints.constraint_name
			and columns.constraint_schema = constraints.constraint_schema
		where constraints.table_schema = 'app' and constraints.table_name = $1
			and constraints.constraint_type = 'PRIMARY KEY' and columns.column_name = 'id')`, table).Scan(&hasPrimaryKey); err != nil {
		return fmt.Errorf("check collection primary key: %w", err)
	}
	if !hasPrimaryKey {
		report.Findings = append(report.Findings, finding("collection_primary_key_missing", "A catalog collection primary key is missing"))
	}
	return nil
}

func checkField(ctx context.Context, pool *database.Pool, table, column, fieldType string, required, unique, indexed bool, report *Report) error {
	present, err := checkColumnType(ctx, pool, table, column, expectedDataType(fieldType), report)
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	var nullable string
	err = pool.QueryRow(ctx, `select is_nullable from information_schema.columns where table_schema = 'app' and table_name = $1 and column_name = $2`, table, column).Scan(&nullable)
	if err != nil {
		return fmt.Errorf("read collection column nullability: %w", err)
	}
	if required && nullable != "NO" {
		report.Findings = append(report.Findings, finding("collection_column_nullability_mismatch", "A required catalog field allows NULL values"))
	}
	if unique || indexed {
		var exists bool
		err := pool.QueryRow(ctx, `select exists(
			select 1
			from pg_index as indexes
			join pg_class as tables on tables.oid = indexes.indrelid
			join pg_namespace as schemas on schemas.oid = tables.relnamespace
			join pg_attribute as columns on columns.attrelid = tables.oid and columns.attname = $2
			where schemas.nspname = 'app' and tables.relname = $1
				and indexes.indisvalid and indexes.indisready and indexes.indislive
				and indexes.indpred is null and indexes.indexprs is null
				and indexes.indnkeyatts = 1 and indexes.indkey[0] = columns.attnum
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

func checkColumnType(ctx context.Context, pool *database.Pool, table, column, want string, report *Report) (bool, error) {
	var got string
	err := pool.QueryRow(ctx, `select data_type from information_schema.columns where table_schema = 'app' and table_name = $1 and column_name = $2`, table, column).Scan(&got)
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
