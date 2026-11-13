package schema

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const maximumSQLViewBytes = 64 << 10

type SQLViewInspection struct {
	Fields       []PlannedField
	Dependencies []uuid.UUID
	HasCreatedAt bool
	HasUpdatedAt bool
}

func cloneSQLViewInspection(source *SQLViewInspection) *SQLViewInspection {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.Fields = append([]PlannedField(nil), source.Fields...)
	cloned.Dependencies = append([]uuid.UUID(nil), source.Dependencies...)
	return &cloned
}

// InspectSQLView validates and describes a read-only SQL view without leaving
// objects or an aborted state in the caller transaction.
func InspectSQLView(ctx context.Context, tx pgx.Tx, catalog Catalog, sql string) (result SQLViewInspection, resultErr error) {
	if tx == nil || len(sql) == 0 || len(sql) > maximumSQLViewBytes || strings.IndexByte(sql, 0) >= 0 {
		return result, viewSQLError("invalid")
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	viewName := "pgbase_inspect_" + suffix
	savepoint := "pgbase_sql_view_" + suffix
	if _, err := tx.Exec(ctx, "SAVEPOINT "+savepoint); err != nil {
		return result, err
	}
	defer func() {
		_, rollbackErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+savepoint)
		_, releaseErr := tx.Exec(ctx, "RELEASE SAVEPOINT "+savepoint)
		if resultErr == nil {
			resultErr = errors.Join(rollbackErr, releaseErr)
		}
	}()
	prefix := "CREATE TEMP VIEW " + pgx.Identifier{viewName}.Sanitize() + " AS "
	if _, err := tx.Exec(ctx, prefix+sql, pgx.QueryExecModeExec); err != nil {
		// Analysis reads no rows, so its message describes only the statement.
		var postgres *pgconn.PgError
		if !errors.As(err, &postgres) {
			return result, err
		}
		details := []ValidationDetail{{Field: "view.sql.message", Code: postgres.Message}, {Field: "view.sql.sqlState", Code: postgres.Code}}
		if position := int(postgres.Position) - len(prefix); position > 0 {
			details = append(details, ValidationDetail{Field: "view.sql.position", Code: strconv.Itoa(position)})
		}
		return result, viewSQLError("invalid_sql", details...)
	}
	var oid uint32
	if err := tx.QueryRow(ctx, `select c.oid from pg_class c join pg_namespace n on n.oid=c.relnamespace where n.oid=pg_my_temp_schema() and c.relname=$1 and c.relkind='v'`, viewName).Scan(&oid); err != nil {
		return result, err
	}
	dependencies, err := inspectSQLViewDependencies(ctx, tx, oid, catalog)
	if err != nil {
		return result, fmt.Errorf("inspect SQL view dependencies: %w", err)
	}
	fields, hasCreatedAt, hasUpdatedAt, err := inspectSQLViewFields(ctx, tx, oid)
	if err != nil {
		return result, fmt.Errorf("inspect SQL view fields: %w", err)
	}
	var invalidID bool
	check := "select exists(select 1 from " + pgx.Identifier{viewName}.Sanitize() + ` where id is null) or exists(select 1 from ` + pgx.Identifier{viewName}.Sanitize() + ` group by id having count(*)>1)`
	if err := tx.QueryRow(ctx, check).Scan(&invalidID); err != nil {
		return result, viewQueryError(err)
	} else if invalidID {
		return result, viewSQLError("invalid_id")
	}
	return SQLViewInspection{Fields: fields, Dependencies: dependencies, HasCreatedAt: hasCreatedAt, HasUpdatedAt: hasUpdatedAt}, nil
}

const sqlViewQueryTrees = `with recursive view_oids(oid) as (
 select $1::oid
 union
 select c.oid from view_oids v join pg_rewrite r on r.ev_class=v.oid,
 lateral regexp_matches(r.ev_action::text, ':relid ([0-9]+)', 'g') m
 join pg_class c on c.oid=(m)[1]::oid and c.relkind='v'
), query_tree as (select ev_action::text body from pg_rewrite where ev_class in (select oid from view_oids))`

func inspectSQLViewDependencies(ctx context.Context, tx pgx.Tx, viewOID uint32, catalog Catalog) ([]uuid.UUID, error) {
	allowed := make(map[string]Collection, len(catalog.Collections))
	for _, c := range catalog.Collections {
		allowed[c.TableName] = c
	}
	// Pinned catalogs have no pg_depend edge; whole-row reads include password columns.
	rows, err := tx.Query(ctx, sqlViewQueryTrees+`, permissions as (
 select (m)[1]::oid relid,string_to_array(trim((m)[2]),' ')::int[] cols
 from query_tree,lateral regexp_matches(body,
 '\{RTEPERMISSIONINFO :relid ([0-9]+)[^{}]*:selectedCols \(b([^)]*)\)', 'g') m
), refs as (
 select d.refobjid relid,d.refobjsubid attnum from pg_rewrite r join pg_depend d
 on d.classid='pg_rewrite'::regclass and d.objid=r.oid and d.refclassid='pg_class'::regclass where r.ev_class in (select oid from view_oids)
 union select relid,0 from permissions
 union select p.relid,a.attnum from permissions p join pg_attribute a on a.attrelid=p.relid
 where (7=any(p.cols) or a.attnum+7=any(p.cols)) and a.attnum>0 and not a.attisdropped
)
 select distinct n.nspname,c.relname,coalesce(a.attname,'') from refs d join pg_class c on c.oid=d.relid
 join pg_namespace n on n.oid=c.relnamespace left join pg_attribute a on a.attrelid=c.oid and a.attnum=d.attnum where c.oid<>$1`, viewOID)
	if err != nil {
		return nil, err
	}
	deps := map[uuid.UUID]bool{}
	for rows.Next() {
		var namespace, name, column string
		if err := rows.Scan(&namespace, &name, &column); err != nil {
			rows.Close()
			return nil, err
		}
		collection, ok := allowed[name]
		if namespace != "app" || !ok {
			rows.Close()
			return nil, viewSQLError("relation_not_collection", ValidationDetail{Field: "view.sql.relation", Code: namespace + "." + name})
		}
		for _, field := range collection.Fields {
			if column != "" && field.ColumnName == column && field.Type == TypePassword {
				rows.Close()
				return nil, viewSQLError("password_field", ValidationDetail{Field: "view.sql.column", Code: column})
			}
		}
		deps[collection.ID] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	var forbidden bool
	err = tx.QueryRow(ctx, sqlViewQueryTrees+`, function_ids as (
		select distinct (match)[1]::oid oid from query_tree,
		lateral regexp_matches(body, ':(?:funcid|aggfnoid|winfnoid|opfuncid) ([0-9]+)', 'g') match
	)
	select
		exists(select 1 from pg_rewrite r join pg_depend d on d.classid='pg_rewrite'::regclass and d.objid=r.oid join pg_proc p on d.refclassid='pg_proc'::regclass and d.refobjid=p.oid join pg_namespace n on n.oid=p.pronamespace where r.ev_class in (select oid from view_oids) and (n.nspname<>'pg_catalog' or p.provolatile='v'))
		or exists(select 1 from pg_rewrite r join pg_depend d on d.classid='pg_rewrite'::regclass and d.objid=r.oid join pg_operator o on d.refclassid='pg_operator'::regclass and d.refobjid=o.oid join pg_namespace n on n.oid=o.oprnamespace where r.ev_class in (select oid from view_oids) and n.nspname<>'pg_catalog')
		or exists(select 1 from pg_rewrite r join pg_depend d on d.classid='pg_rewrite'::regclass and d.objid=r.oid join pg_type t on d.refclassid='pg_type'::regclass and d.refobjid=t.oid join pg_namespace n on n.oid=t.typnamespace where r.ev_class in (select oid from view_oids) and n.nspname<>'pg_catalog')
		or exists(select 1 from function_ids f left join pg_proc p on p.oid=f.oid left join pg_namespace n on n.oid=p.pronamespace where p.oid is null or f.oid=0 or n.nspname<>'pg_catalog' or p.provolatile='v')`, viewOID).Scan(&forbidden)
	if err != nil {
		return nil, err
	}
	if forbidden {
		return nil, viewSQLError("forbidden_function")
	}
	result := make([]uuid.UUID, 0, len(deps))
	for id := range deps {
		result = append(result, id)
	}
	slices.SortFunc(result, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return result, nil
}

func inspectSQLViewFields(ctx context.Context, tx pgx.Tx, oid uint32) ([]PlannedField, bool, bool, error) {
	rows, err := tx.Query(ctx, `select a.attname,t.typname from pg_attribute a join pg_type t on t.oid=a.atttypid where a.attrelid=$1 and a.attnum>0 and not a.attisdropped order by a.attnum`, oid)
	if err != nil {
		return nil, false, false, err
	}
	defer rows.Close()
	types := map[string]FieldType{"bool": TypeBoolean, "int4": TypeInteger, "int8": TypeBigInt, "numeric": TypeNumeric, "text": TypeText, "varchar": TypeText, "timestamptz": TypeTimestampTZ, "uuid": TypeUUID, "jsonb": TypeJSONB}
	fields := []PlannedField{}
	position := 0
	sawID := false
	hasCreatedAt, hasUpdatedAt := false, false
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, false, false, err
		}
		column := ValidationDetail{Field: "view.sql.column", Code: name}
		if name == "id" {
			if typ != "uuid" || sawID {
				return nil, false, false, viewSQLError("invalid_id", column)
			}
			sawID = true
			continue
		}
		if name == "created_at" || name == "updated_at" {
			if typ != "timestamptz" {
				return nil, false, false, viewSQLError("invalid_timestamp", column)
			}
			hasCreatedAt = hasCreatedAt || name == "created_at"
			hasUpdatedAt = hasUpdatedAt || name == "updated_at"
			continue
		}
		kind, ok := types[typ]
		if !ok || validateName("", name) != nil {
			return nil, false, false, viewSQLError("unsupported_column", column)
		}
		fields = append(fields, PlannedField{Name: name, ColumnName: name, Type: kind, Ordinal: position})
		position++
	}
	if err := rows.Err(); err != nil {
		return nil, false, false, err
	}
	if !sawID {
		return nil, false, false, viewSQLError("id_required")
	}
	if len(fields) > 128 {
		return nil, false, false, viewSQLError("too_many_fields")
	}
	return fields, hasCreatedAt, hasUpdatedAt, nil
}

// viewSQLError stays a schema conflict for existing callers while telling the
// administrator which view.sql check failed.
func viewSQLError(reason string, details ...ValidationDetail) error {
	return errors.Join(ErrConflict, &ValidationError{Details: append([]ValidationDetail{{Field: "view.sql", Code: reason}}, details...)})
}

// viewQueryError reports only the SQLSTATE of a failure while rows are read:
// its message could quote stored values.
func viewQueryError(err error) error {
	var postgres *pgconn.PgError
	if !errors.As(err, &postgres) {
		return err
	}
	return viewSQLError("query_failed", ValidationDetail{Field: "view.sql.sqlState", Code: postgres.Code})
}

// Recheck persisted SQL views at startup so old unsafe definitions cannot survive an upgrade.
func validateCatalogSQLViews(ctx context.Context, tx pgx.Tx, catalog Catalog) error {
	for _, collection := range catalog.Collections {
		if collection.View == nil || collection.View.SQL == "" {
			continue
		}
		var oid uint32
		if err := tx.QueryRow(ctx, `select to_regclass($1)::oid`, pgx.Identifier{"app", collection.TableName}.Sanitize()).Scan(&oid); err != nil {
			return err
		}
		if _, err := inspectSQLViewDependencies(ctx, tx, oid, catalog); err != nil {
			return err
		}
	}
	return nil
}
