package sqlconsole

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func inspectManagedReadRelations(ctx context.Context, pool *database.Pool, catalog schema.Catalog) (map[RelationRef]managedReadSnapshot, error) {
	columnsByRelation := managedReadColumns(catalog)
	collections := make(map[RelationRef]schema.Collection, len(catalog.Collections))
	result := make(map[RelationRef]managedReadSnapshot, len(columnsByRelation))
	for _, collection := range catalog.Collections {
		reference := RelationRef{Schema: "app", Name: collection.TableName}
		collections[reference] = collection
		var oid uint32
		var kind, persistence string
		var rowSecurity, forceRowSecurity, inherited bool
		err := pool.QueryRow(ctx, `select c.oid,c.relkind::text,c.relpersistence::text,c.relrowsecurity,c.relforcerowsecurity,
			exists(select 1 from pg_catalog.pg_inherits where inhrelid=c.oid or inhparent=c.oid)
			from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
			where n.nspname=$1 and c.relname=$2`, reference.Schema, reference.Name).Scan(
			&oid, &kind, &persistence, &rowSecurity, &forceRowSecurity, &inherited,
		)
		if err != nil {
			return nil, fmt.Errorf("%w: inspect %s.%s: %v", ErrUnsafeReadRelation, reference.Schema, reference.Name, err)
		}
		expectedKind := "r"
		if collection.IsView() {
			expectedKind = "v"
		}
		if kind != expectedKind || persistence != "p" || rowSecurity || forceRowSecurity || inherited {
			return nil, fmt.Errorf("%w: %s.%s has unexpected relation shape", ErrUnsafeReadRelation, reference.Schema, reference.Name)
		}
		for column := range columnsByRelation[reference] {
			var typeSchema, typeName string
			if err := pool.QueryRow(ctx, `select tn.nspname,t.typname from pg_catalog.pg_attribute a
				join pg_catalog.pg_type t on t.oid=a.atttypid join pg_catalog.pg_namespace tn on tn.oid=t.typnamespace
				where a.attrelid=$1 and a.attname=$2 and a.attnum>0 and not a.attisdropped`, oid, column).Scan(&typeSchema, &typeName); err != nil {
				return nil, fmt.Errorf("%w: inspect %s.%s.%s: %v", ErrUnsafeReadRelation, reference.Schema, reference.Name, column, err)
			}
			if typeSchema != "pg_catalog" || !safeType(typeName) {
				return nil, fmt.Errorf("%w: %s.%s.%s has an unsupported type", ErrUnsafeReadRelation, reference.Schema, reference.Name, column)
			}
		}
		result[reference] = managedReadSnapshot{
			Reference: reference, OID: oid, Columns: columnsByRelation[reference], View: collection.IsView(),
		}
	}
	for reference, collection := range collections {
		if collection.IsView() {
			if err := validateManagedViewDependencies(ctx, pool, result[reference].OID, result); err != nil {
				return nil, fmt.Errorf("%w: %s.%s: %v", ErrUnsafeReadRelation, reference.Schema, reference.Name, err)
			}
		}
	}
	return result, nil
}

func validateManagedViewDependencies(ctx context.Context, pool *database.Pool, rootOID uint32, allowed map[RelationRef]managedReadSnapshot) error {
	queue := []uint32{rootOID}
	visited := make(map[uint32]struct{})
	for len(queue) != 0 {
		viewOID := queue[0]
		queue = queue[1:]
		if _, seen := visited[viewOID]; seen {
			continue
		}
		visited[viewOID] = struct{}{}
		rows, err := pool.Query(ctx, `select distinct c.oid,n.nspname,c.relname,coalesce(a.attname,'')
			from pg_catalog.pg_rewrite r join pg_catalog.pg_depend d
			on d.classid='pg_catalog.pg_rewrite'::pg_catalog.regclass and d.objid=r.oid
			join pg_catalog.pg_class c on d.refclassid='pg_catalog.pg_class'::pg_catalog.regclass and d.refobjid=c.oid
			join pg_catalog.pg_namespace n on n.oid=c.relnamespace
			left join pg_catalog.pg_attribute a on a.attrelid=c.oid and a.attnum=d.refobjsubid
			where r.ev_class=$1 and c.oid<>$1`, viewOID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var dependencyOID uint32
			var schemaName, relationName, columnName string
			if err := rows.Scan(&dependencyOID, &schemaName, &relationName, &columnName); err != nil {
				rows.Close()
				return err
			}
			dependency, ok := allowed[RelationRef{Schema: schemaName, Name: relationName}]
			if !ok || dependency.OID != dependencyOID {
				rows.Close()
				return fmt.Errorf("relation %s.%s is outside the managed read scope", schemaName, relationName)
			}
			if columnName != "" {
				if _, ok := dependency.Columns[columnName]; !ok {
					rows.Close()
					return fmt.Errorf("column %s.%s.%s is outside the managed read scope", schemaName, relationName, columnName)
				}
			}
			if dependency.View {
				queue = append(queue, dependencyOID)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if err := validateViewExpressionDependencies(ctx, pool, viewOID); err != nil {
			return err
		}
	}
	return nil
}

func validateViewExpressionDependencies(ctx context.Context, pool *database.Pool, viewOID uint32) error {
	// Resolved OID checks alone miss CoerceViaIO and SQLValueFunction nodes.
	// Re-apply the same fail-closed grammar to PostgreSQL's deparsed view so
	// intermediate casts and every expression form are checked transitively.
	var definition string
	var sqlValueFunction bool
	if err := pool.QueryRow(ctx, `select pg_catalog.pg_get_viewdef($1::oid,true),
		exists(select 1 from pg_catalog.pg_rewrite where ev_class=$1 and ev_action::text like '%{SQLVALUEFUNCTION %')`, viewOID).Scan(&definition, &sqlValueFunction); err != nil {
		return err
	}
	if sqlValueFunction {
		return errors.New("SQL value functions are outside the exact builtin allowlist")
	}
	if _, err := Analyze(Request{ExecutionID: uuid.New(), Mode: ModeRead, Statements: []Statement{{SQL: definition}}}); err != nil {
		return fmt.Errorf("view expression is outside the SQL console grammar: %w", err)
	}
	rows, err := pool.Query(ctx, `select distinct (match)[1],(match)[2]::oid
		from pg_catalog.pg_rewrite r,
		lateral pg_catalog.regexp_matches(r.ev_action::text, ':(funcid|aggfnoid|winfnoid|opfuncid|opno) ([0-9]+)', 'g') match
		where r.ev_class=$1`, viewOID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var oid uint32
		if err := rows.Scan(&kind, &oid); err != nil {
			return err
		}
		if oid == 0 {
			return errors.New("unresolved expression dependency")
		}
		if kind == "opno" {
			var namespace, name string
			if err := pool.QueryRow(ctx, `select n.nspname,o.oprname from pg_catalog.pg_operator o
				join pg_catalog.pg_namespace n on n.oid=o.oprnamespace where o.oid=$1`, oid).Scan(&namespace, &name); err != nil {
				return err
			}
			if namespace != "pg_catalog" {
				return fmt.Errorf("operator %s.%s is outside pg_catalog", namespace, name)
			}
			if _, ok := allowedOperators[strings.ToUpper(name)]; !ok {
				return fmt.Errorf("operator %s is not allowed", name)
			}
			continue
		}
		var namespace, name, volatility string
		if err := pool.QueryRow(ctx, `select n.nspname,p.proname,p.provolatile::text from pg_catalog.pg_proc p
			join pg_catalog.pg_namespace n on n.oid=p.pronamespace where p.oid=$1`, oid).Scan(&namespace, &name, &volatility); err != nil {
			return err
		}
		if namespace != "pg_catalog" || volatility == "v" {
			return fmt.Errorf("function %s.%s is outside the immutable/stable builtin scope", namespace, name)
		}
		if kind == "opfuncid" {
			continue
		}
		if _, ok := allowedFunctions[strings.ToLower(name)]; ok {
			continue
		}
		var safeCast bool
		if err := pool.QueryRow(ctx, `select exists(select 1 from pg_catalog.pg_cast c
			join pg_catalog.pg_type source on source.oid=c.castsource
			join pg_catalog.pg_namespace source_n on source_n.oid=source.typnamespace
			join pg_catalog.pg_type target on target.oid=c.casttarget
			join pg_catalog.pg_namespace target_n on target_n.oid=target.typnamespace
			where c.castfunc=$1 and source_n.nspname='pg_catalog' and target_n.nspname='pg_catalog'
			and source.typname=any($2) and target.typname=any($2))`, oid, safeTypeNames()).Scan(&safeCast); err != nil {
			return err
		}
		if !safeCast {
			return fmt.Errorf("function %s is not in the exact builtin allowlist", name)
		}
	}
	return rows.Err()
}

func safeTypeNames() []string {
	result := make([]string, 0, len(allowedCasts)+1)
	for name := range allowedCasts {
		result = append(result, name)
	}
	result = append(result, "bpchar")
	sort.Strings(result)
	return result
}

func validateNoUnexpectedRelationPrivileges(ctx context.Context, pool *database.Pool, label string, allowed map[RelationRef]struct{}) error {
	rows, err := pool.Query(ctx, `select n.nspname,c.relname
		from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
		where c.relkind in ('r','p','v','m','f')
		and n.nspname not in ('pg_catalog','information_schema','pgbase_meta','pgbase_relation')
		and n.nspname !~ '^pg_(toast|temp_)'
		and (has_table_privilege(current_user,c.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
		or has_any_column_privilege(current_user,c.oid,'SELECT,INSERT,UPDATE,REFERENCES'))`)
	if err != nil {
		return fmt.Errorf("%w: inspect %s relation grants: %v", ErrUnsafeDatabaseRole, label, err)
	}
	for rows.Next() {
		var reference RelationRef
		if err := rows.Scan(&reference.Schema, &reference.Name); err != nil {
			rows.Close()
			return err
		}
		if _, ok := allowed[reference]; !ok {
			rows.Close()
			return fmt.Errorf("%w: %s role has an unexpected grant on %s.%s", ErrUnsafeDatabaseRole, label, reference.Schema, reference.Name)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	var sequences int
	if err := pool.QueryRow(ctx, `select count(*) from pg_catalog.pg_class c
		join pg_catalog.pg_namespace n on n.oid=c.relnamespace
		where c.relkind='S' and n.nspname not in ('pg_catalog','information_schema','pgbase_meta','pgbase_relation')
		and n.nspname !~ '^pg_(toast|temp_)'
		and has_sequence_privilege(current_user,c.oid,'USAGE,SELECT,UPDATE')`).Scan(&sequences); err != nil {
		return fmt.Errorf("%w: inspect %s sequence grants: %v", ErrUnsafeDatabaseRole, label, err)
	}
	if sequences != 0 {
		return fmt.Errorf("%w: %s role has %d user sequence grants", ErrUnsafeDatabaseRole, label, sequences)
	}
	return nil
}
