package doctor

import (
	"context"
	"fmt"

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
	rows, err := pool.Query(ctx, `select collections.name, collections.table_name, fields.column_name, fields.field_type,
		fields.required, fields.is_unique, fields.is_indexed
		from pgbase_meta.collections as collections
		left join pgbase_meta.fields as fields on fields.collection_id = collections.id
		order by collections.name, fields.ordinal`)
	if err != nil {
		return fmt.Errorf("read schema catalog: %w", err)
	}
	defer rows.Close()
	seenTables := make(map[string]bool)
	for rows.Next() {
		var collection, table string
		var column, fieldType *string
		var required, unique, indexed *bool
		if err := rows.Scan(&collection, &table, &column, &fieldType, &required, &unique, &indexed); err != nil {
			return fmt.Errorf("scan schema catalog: %w", err)
		}
		if !seenTables[table] {
			seenTables[table] = true
			present, err := tableExists(ctx, pool, table)
			if err != nil {
				return err
			}
			if !present {
				report.Findings = append(report.Findings, finding("collection_table_missing", "A catalog collection table is missing"))
				continue
			}
			if err := checkBaseColumns(ctx, pool, table, report); err != nil {
				return err
			}
		}
		if column == nil || fieldType == nil || !seenTables[table] {
			continue
		}
		if err := checkField(ctx, pool, table, *column, *fieldType, *required, *unique, *indexed, report); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate schema catalog: %w", err)
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
		if err := checkColumnType(ctx, pool, table, column, typeName, report); err != nil {
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
	if err := checkColumnType(ctx, pool, table, column, expectedDataType(fieldType), report); err != nil {
		return err
	}
	var nullable string
	err := pool.QueryRow(ctx, `select is_nullable from information_schema.columns where table_schema = 'app' and table_name = $1 and column_name = $2`, table, column).Scan(&nullable)
	if err != nil {
		return fmt.Errorf("read collection column nullability: %w", err)
	}
	if required && nullable != "NO" {
		report.Findings = append(report.Findings, finding("collection_column_nullability_mismatch", "A required catalog field allows NULL values"))
	}
	if unique || indexed {
		var exists bool
		err := pool.QueryRow(ctx, `select exists(
			select 1 from pg_indexes
			where schemaname = 'app' and tablename = $1 and indexdef like '%' || quote_ident($2) || '%'
			and ($3 = false or indexdef like 'CREATE UNIQUE INDEX%'))`, table, column, unique).Scan(&exists)
		if err != nil {
			return fmt.Errorf("check collection index: %w", err)
		}
		if !exists {
			report.Findings = append(report.Findings, finding("collection_index_missing", "A catalog field index is missing"))
		}
	}
	return nil
}

func checkColumnType(ctx context.Context, pool *database.Pool, table, column, want string, report *Report) error {
	var got string
	err := pool.QueryRow(ctx, `select data_type from information_schema.columns where table_schema = 'app' and table_name = $1 and column_name = $2`, table, column).Scan(&got)
	if err != nil {
		report.Findings = append(report.Findings, finding("collection_column_missing", "A catalog collection column is missing"))
		return nil
	}
	if got != want {
		report.Findings = append(report.Findings, finding("collection_column_type_mismatch", "A catalog collection column has an unexpected type"))
	}
	return nil
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
