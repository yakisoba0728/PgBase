package schema

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
		return result, ErrConflict
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
	statement := "CREATE TEMP VIEW " + pgx.Identifier{viewName}.Sanitize() + " AS " + sql
	if _, err := tx.Exec(ctx, statement, pgx.QueryExecModeExec); err != nil {
		return result, ErrConflict
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
	if err := tx.QueryRow(ctx, check).Scan(&invalidID); err != nil || invalidID {
		return result, fmt.Errorf("%w: invalid id err=%v invalid=%v", ErrConflict, err, invalidID)
	}
	return SQLViewInspection{Fields: fields, Dependencies: dependencies, HasCreatedAt: hasCreatedAt, HasUpdatedAt: hasUpdatedAt}, nil
}

func inspectSQLViewDependencies(ctx context.Context, tx pgx.Tx, viewOID uint32, catalog Catalog) ([]uuid.UUID, error) {
	allowed := make(map[string]Collection, len(catalog.Collections))
	for _, c := range catalog.Collections {
		allowed[c.TableName] = c
	}
	rows, err := tx.Query(ctx, `select distinct n.nspname,c.relname,coalesce(a.attname,'') from pg_rewrite r join pg_depend d on d.classid='pg_rewrite'::regclass and d.objid=r.oid join pg_class c on d.refclassid='pg_class'::regclass and d.refobjid=c.oid join pg_namespace n on n.oid=c.relnamespace left join pg_attribute a on a.attrelid=c.oid and a.attnum=d.refobjsubid where r.ev_class=$1 and c.oid<>$1`, viewOID)
	if err != nil {
		return nil, err
	}
	deps := map[uuid.UUID]bool{}
	for rows.Next() {
		var namespace, name, column string
		if rows.Scan(&namespace, &name, &column) != nil {
			rows.Close()
			return nil, ErrConflict
		}
		collection, ok := allowed[name]
		if namespace != "app" || !ok {
			rows.Close()
			return nil, ErrConflict
		}
		for _, field := range collection.Fields {
			if column != "" && field.ColumnName == column && field.Type == TypePassword {
				rows.Close()
				return nil, ErrConflict
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
	err = tx.QueryRow(ctx, `with query_tree as (
		select ev_action::text body from pg_rewrite where ev_class=$1
	), function_ids as (
		select distinct (match)[1]::oid oid from query_tree,
		lateral regexp_matches(body, ':(?:funcid|aggfnoid|winfnoid|opfuncid) ([0-9]+)', 'g') match
	)
	select
		exists(select 1 from pg_rewrite r join pg_depend d on d.classid='pg_rewrite'::regclass and d.objid=r.oid join pg_proc p on d.refclassid='pg_proc'::regclass and d.refobjid=p.oid join pg_namespace n on n.oid=p.pronamespace where r.ev_class=$1 and (n.nspname<>'pg_catalog' or p.provolatile='v'))
		or exists(select 1 from pg_rewrite r join pg_depend d on d.classid='pg_rewrite'::regclass and d.objid=r.oid join pg_operator o on d.refclassid='pg_operator'::regclass and d.refobjid=o.oid join pg_namespace n on n.oid=o.oprnamespace where r.ev_class=$1 and n.nspname<>'pg_catalog')
		or exists(select 1 from pg_rewrite r join pg_depend d on d.classid='pg_rewrite'::regclass and d.objid=r.oid join pg_type t on d.refclassid='pg_type'::regclass and d.refobjid=t.oid join pg_namespace n on n.oid=t.typnamespace where r.ev_class=$1 and n.nspname<>'pg_catalog')
		or exists(select 1 from function_ids f left join pg_proc p on p.oid=f.oid left join pg_namespace n on n.oid=p.pronamespace where p.oid is null or f.oid=0 or n.nspname<>'pg_catalog' or p.provolatile='v')`, viewOID).Scan(&forbidden)
	if err != nil {
		return nil, err
	}
	if forbidden {
		return nil, ErrConflict
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
		if rows.Scan(&name, &typ) != nil {
			return nil, false, false, ErrConflict
		}
		if name == "id" {
			if typ != "uuid" || sawID {
				return nil, false, false, ErrConflict
			}
			sawID = true
			continue
		}
		if name == "created_at" || name == "updated_at" {
			if typ != "timestamptz" {
				return nil, false, false, ErrConflict
			}
			hasCreatedAt = hasCreatedAt || name == "created_at"
			hasUpdatedAt = hasUpdatedAt || name == "updated_at"
			continue
		}
		kind, ok := types[typ]
		if !ok || validateName("", name) != nil {
			return nil, false, false, ErrConflict
		}
		fields = append(fields, PlannedField{Name: name, ColumnName: name, Type: kind, Ordinal: position})
		position++
	}
	if err := rows.Err(); err != nil {
		return nil, false, false, err
	}
	if !sawID || len(fields) > 128 {
		return nil, false, false, ErrConflict
	}
	return fields, hasCreatedAt, hasUpdatedAt, nil
}
