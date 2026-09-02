package doctor

import (
	"context"
	"fmt"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

// physicalSnapshot is one bulk observation of every PostgreSQL object living in
// the namespaces the schema planner manages. It reuses the planner's own spec
// types so drift diagnosis compares like with like instead of restating what a
// collection table, index, constraint, trigger or function is supposed to look
// like.
//
// It is loaded with a fixed number of statements regardless of how many
// collections, fields or relations the catalog holds. That bound is the
// promise an operator relies on when running doctor against a large project,
// and it is enforced by the pgx-traced query-count test.
type physicalSnapshot struct {
	namespaces  map[string]struct{}
	tables      map[schema.PhysicalObjectRef]schema.TableSpec
	columns     map[schema.PhysicalObjectRef]schema.ColumnSpec
	indexes     map[schema.PhysicalObjectRef]schema.IndexSpec
	constraints map[schema.PhysicalObjectRef]schema.ConstraintSpec
	triggers    map[schema.PhysicalObjectRef]schema.TriggerSpec
	functions   map[schema.PhysicalObjectRef]schema.FunctionSpec
}

func tableRef(namespace, name string) schema.PhysicalObjectRef {
	return schema.PhysicalObjectRef{Kind: schema.PhysicalObjectTable, Namespace: namespace, Name: name}
}

func columnRef(namespace, table, name string) schema.PhysicalObjectRef {
	return schema.PhysicalObjectRef{Kind: schema.PhysicalObjectColumn, Namespace: namespace, Table: table, Name: name}
}

func indexRef(namespace, table, name string) schema.PhysicalObjectRef {
	return schema.PhysicalObjectRef{Kind: schema.PhysicalObjectIndex, Namespace: namespace, Table: table, Name: name}
}

func constraintRef(namespace, table, name string) schema.PhysicalObjectRef {
	return schema.PhysicalObjectRef{Kind: schema.PhysicalObjectConstraint, Namespace: namespace, Table: table, Name: name}
}

func triggerRef(namespace, table, name string) schema.PhysicalObjectRef {
	return schema.PhysicalObjectRef{Kind: schema.PhysicalObjectTrigger, Namespace: namespace, Table: table, Name: name}
}

func functionRef(namespace, name string) schema.PhysicalObjectRef {
	return schema.PhysicalObjectRef{Kind: schema.PhysicalObjectFunction, Namespace: namespace, Name: name}
}

func readPhysicalSnapshot(ctx context.Context, db database.DBTX, namespaces []string) (physicalSnapshot, error) {
	snapshot := physicalSnapshot{
		namespaces:  make(map[string]struct{}, len(namespaces)),
		tables:      make(map[schema.PhysicalObjectRef]schema.TableSpec),
		columns:     make(map[schema.PhysicalObjectRef]schema.ColumnSpec),
		indexes:     make(map[schema.PhysicalObjectRef]schema.IndexSpec),
		constraints: make(map[schema.PhysicalObjectRef]schema.ConstraintSpec),
		triggers:    make(map[schema.PhysicalObjectRef]schema.TriggerSpec),
		functions:   make(map[schema.PhysicalObjectRef]schema.FunctionSpec),
	}
	for _, load := range []func(context.Context, database.DBTX, []string, *physicalSnapshot) error{
		loadPhysicalNamespaces,
		loadPhysicalTables,
		loadPhysicalColumns,
		loadPhysicalIndexes,
		loadPhysicalConstraints,
		loadPhysicalTriggers,
		loadPhysicalFunctions,
	} {
		if err := load(ctx, db, namespaces, &snapshot); err != nil {
			return physicalSnapshot{}, err
		}
	}
	return snapshot, nil
}

func loadPhysicalNamespaces(ctx context.Context, db database.DBTX, namespaces []string, snapshot *physicalSnapshot) error {
	rows, err := db.Query(ctx, `select nspname from pg_namespace where nspname = any($1::text[])`, namespaces)
	if err != nil {
		return fmt.Errorf("read managed namespaces: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("scan managed namespace: %w", err)
		}
		snapshot.namespaces[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate managed namespaces: %w", err)
	}
	return nil
}

func loadPhysicalTables(ctx context.Context, db database.DBTX, namespaces []string, snapshot *physicalSnapshot) error {
	rows, err := db.Query(ctx, `select namespaces.nspname, tables.relname,
		tables.relkind::text, tables.relpersistence::text
		from pg_class as tables
		join pg_namespace as namespaces on namespaces.oid = tables.relnamespace
		where namespaces.nspname = any($1::text[]) and tables.relkind in ('r', 'p')`, namespaces)
	if err != nil {
		return fmt.Errorf("read physical tables: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var spec schema.TableSpec
		if err := rows.Scan(&spec.Namespace, &spec.Name, &spec.Kind, &spec.Persistence); err != nil {
			return fmt.Errorf("scan physical table: %w", err)
		}
		snapshot.tables[tableRef(spec.Namespace, spec.Name)] = spec
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate physical tables: %w", err)
	}
	return nil
}

func loadPhysicalColumns(ctx context.Context, db database.DBTX, namespaces []string, snapshot *physicalSnapshot) error {
	rows, err := db.Query(ctx, `select namespaces.nspname, tables.relname, columns.attname,
		format_type(columns.atttypid, columns.atttypmod), columns.attnotnull,
		coalesce(pg_get_expr(defaults.adbin, defaults.adrelid), '')
		from pg_attribute as columns
		join pg_class as tables on tables.oid = columns.attrelid
		join pg_namespace as namespaces on namespaces.oid = tables.relnamespace
		left join pg_attrdef as defaults on defaults.adrelid = tables.oid and defaults.adnum = columns.attnum
		where namespaces.nspname = any($1::text[]) and tables.relkind in ('r', 'p')
			and columns.attnum > 0 and not columns.attisdropped`, namespaces)
	if err != nil {
		return fmt.Errorf("read physical columns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var spec schema.ColumnSpec
		if err := rows.Scan(&spec.Namespace, &spec.Table, &spec.Name, &spec.DataType, &spec.NotNull, &spec.DefaultSQL); err != nil {
			return fmt.Errorf("scan physical column: %w", err)
		}
		snapshot.columns[columnRef(spec.Namespace, spec.Table, spec.Name)] = spec
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate physical columns: %w", err)
	}
	return nil
}

func loadPhysicalIndexes(ctx context.Context, db database.DBTX, namespaces []string, snapshot *physicalSnapshot) error {
	rows, err := db.Query(ctx, `select namespaces.nspname, tables.relname, index_tables.relname,
		access_methods.amname,
		array(select attributes.attname
			from unnest(indexes.indkey::smallint[]) with ordinality as keys(attribute_number, ordinal)
			join pg_attribute as attributes
				on attributes.attrelid = indexes.indrelid and attributes.attnum = keys.attribute_number
			where keys.ordinal <= indexes.indnkeyatts order by keys.ordinal),
		indexes.indoption::smallint[],
		array(select case when operator_classes.opcdefault then '' else operator_classes.opcname end
			from unnest(indexes.indclass::oid[]) with ordinality as classes(operator_class, ordinal)
			join pg_opclass as operator_classes on operator_classes.oid = classes.operator_class
			where classes.ordinal <= indexes.indnkeyatts order by classes.ordinal),
		array(select attributes.attname
			from unnest(indexes.indkey::smallint[]) with ordinality as keys(attribute_number, ordinal)
			join pg_attribute as attributes
				on attributes.attrelid = indexes.indrelid and attributes.attnum = keys.attribute_number
			where keys.ordinal > indexes.indnkeyatts order by keys.ordinal),
		indexes.indnkeyatts::integer, indexes.indnatts::integer,
		indexes.indisunique, indexes.indnullsnotdistinct, indexes.indisprimary,
		coalesce(constraints.conname, ''),
		coalesce(pg_get_expr(indexes.indpred, indexes.indrelid), ''),
		coalesce(pg_get_expr(indexes.indexprs, indexes.indrelid), ''),
		indexes.indisvalid, indexes.indisready, indexes.indislive
		from pg_index as indexes
		join pg_class as tables on tables.oid = indexes.indrelid
		join pg_namespace as namespaces on namespaces.oid = tables.relnamespace
		join pg_class as index_tables on index_tables.oid = indexes.indexrelid
		join pg_am as access_methods on access_methods.oid = index_tables.relam
		left join pg_constraint as constraints
			on constraints.conindid = indexes.indexrelid and constraints.contype in ('p', 'u')
		where namespaces.nspname = any($1::text[])`, namespaces)
	if err != nil {
		return fmt.Errorf("read physical indexes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var spec schema.IndexSpec
		var columns, operatorClasses []string
		var options []int16
		var keyCount, attributeCount int
		if err := rows.Scan(
			&spec.Namespace, &spec.Table, &spec.Name, &spec.Method,
			&columns, &options, &operatorClasses, &spec.IncludedColumns,
			&keyCount, &attributeCount,
			&spec.Unique, &spec.NullsNotDistinct, &spec.Primary, &spec.ConstraintName,
			&spec.PredicateSQL, &spec.ExpressionSQL, &spec.Valid, &spec.Ready, &spec.Live,
		); err != nil {
			return fmt.Errorf("scan physical index: %w", err)
		}
		// A key whose attribute number is 0 is an expression key, so its name
		// is absent from the join above. Recording the declared key count keeps
		// such an index structurally distinct from a plain column index instead
		// of silently comparing as a shorter one.
		if len(columns) != keyCount || len(operatorClasses) != keyCount ||
			len(spec.IncludedColumns) != attributeCount-keyCount {
			spec.ExpressionSQL += "\x00truncated_index_keys"
		}
		spec.Keys = make([]schema.IndexKeySpec, len(columns))
		for index, column := range columns {
			key := schema.IndexKeySpec{Column: column}
			if index < len(options) {
				key.Descending = options[index]&1 != 0
				key.NullsFirst = options[index]&2 != 0
			}
			if index < len(operatorClasses) {
				key.OperatorClass = operatorClasses[index]
			}
			spec.Keys[index] = key
		}
		snapshot.indexes[indexRef(spec.Namespace, spec.Table, spec.Name)] = spec
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate physical indexes: %w", err)
	}
	return nil
}

// loadPhysicalConstraints skips PostgreSQL 18's contype 'n' rows: that release
// projects every NOT NULL as a catalog constraint, and the planner's contract
// records it on the column instead. Reading conenforced through to_jsonb keeps
// one statement working on 16, 17 and 18, where the column does not exist
// before 18 and a direct reference would fail to parse.
func loadPhysicalConstraints(ctx context.Context, db database.DBTX, namespaces []string, snapshot *physicalSnapshot) error {
	rows, err := db.Query(ctx, `select namespaces.nspname, tables.relname, constraints.conname,
		constraints.contype::text,
		array(select attributes.attname
			from unnest(coalesce(constraints.conkey, '{}'::smallint[])) with ordinality as keys(attribute_number, ordinal)
			join pg_attribute as attributes
				on attributes.attrelid = constraints.conrelid and attributes.attnum = keys.attribute_number
			order by keys.ordinal),
		coalesce(target_namespaces.nspname, ''), coalesce(target_tables.relname, ''),
		array(select attributes.attname
			from unnest(coalesce(constraints.confkey, '{}'::smallint[])) with ordinality as keys(attribute_number, ordinal)
			join pg_attribute as attributes
				on attributes.attrelid = constraints.confrelid and attributes.attnum = keys.attribute_number
			order by keys.ordinal),
		constraints.confmatchtype::text, constraints.confupdtype::text, constraints.confdeltype::text,
		coalesce(pg_get_expr(constraints.conbin, constraints.conrelid), ''),
		constraints.condeferrable, constraints.condeferred, constraints.convalidated,
		coalesce((to_jsonb(constraints) ->> 'conenforced')::boolean, true),
		constraints.connoinherit
		from pg_constraint as constraints
		join pg_class as tables on tables.oid = constraints.conrelid
		join pg_namespace as namespaces on namespaces.oid = tables.relnamespace
		left join pg_class as target_tables on target_tables.oid = constraints.confrelid
		left join pg_namespace as target_namespaces on target_namespaces.oid = target_tables.relnamespace
		where namespaces.nspname = any($1::text[]) and constraints.contype <> 'n'`, namespaces)
	if err != nil {
		return fmt.Errorf("read physical constraints: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var spec schema.ConstraintSpec
		if err := rows.Scan(
			&spec.Namespace, &spec.Table, &spec.Name, &spec.Kind, &spec.Columns,
			&spec.ReferenceNamespace, &spec.ReferenceTable, &spec.ReferenceColumns,
			&spec.Match, &spec.OnUpdate, &spec.OnDelete, &spec.CheckSQL,
			&spec.Deferrable, &spec.InitiallyDeferred, &spec.Validated, &spec.Enforced, &spec.NoInherit,
		); err != nil {
			return fmt.Errorf("scan physical constraint: %w", err)
		}
		snapshot.constraints[constraintRef(spec.Namespace, spec.Table, spec.Name)] = spec
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate physical constraints: %w", err)
	}
	return nil
}

func loadPhysicalTriggers(ctx context.Context, db database.DBTX, namespaces []string, snapshot *physicalSnapshot) error {
	rows, err := db.Query(ctx, `select namespaces.nspname, tables.relname, triggers.tgname,
		function_namespaces.nspname, functions.proname,
		array(select format_type(argument_type, null)
			from unnest(functions.proargtypes::oid[]) with ordinality as arguments(argument_type, ordinal)
			order by arguments.ordinal),
		triggers.tgtype::integer,
		array(select attributes.attname
			from unnest(triggers.tgattr::smallint[]) with ordinality as update_columns(attribute_number, ordinal)
			join pg_attribute as attributes
				on attributes.attrelid = triggers.tgrelid and attributes.attnum = update_columns.attribute_number
			order by update_columns.ordinal),
		triggers.tgenabled::text, triggers.tgconstraint <> 0,
		triggers.tgdeferrable, triggers.tginitdeferred, triggers.tgnargs::integer,
		coalesce(pg_get_expr(triggers.tgqual, triggers.tgrelid), ''),
		coalesce(triggers.tgoldtable::text, ''), coalesce(triggers.tgnewtable::text, '')
		from pg_trigger as triggers
		join pg_class as tables on tables.oid = triggers.tgrelid
		join pg_namespace as namespaces on namespaces.oid = tables.relnamespace
		join pg_proc as functions on functions.oid = triggers.tgfoid
		join pg_namespace as function_namespaces on function_namespaces.oid = functions.pronamespace
		where namespaces.nspname = any($1::text[]) and not triggers.tgisinternal`, namespaces)
	if err != nil {
		return fmt.Errorf("read physical triggers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var spec schema.TriggerSpec
		var argumentCount int
		if err := rows.Scan(
			&spec.Namespace, &spec.Table, &spec.Name,
			&spec.FunctionNamespace, &spec.FunctionName, &spec.FunctionArgumentTypes,
			&spec.TypeMask, &spec.UpdateColumns, &spec.Enabled, &spec.Constraint,
			&spec.Deferrable, &spec.InitiallyDeferred, &argumentCount,
			&spec.WhenSQL, &spec.OldTransitionTable, &spec.NewTransitionTable,
		); err != nil {
			return fmt.Errorf("scan physical trigger: %w", err)
		}
		spec.Arguments = make([]string, argumentCount)
		snapshot.triggers[triggerRef(spec.Namespace, spec.Table, spec.Name)] = spec
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate physical triggers: %w", err)
	}
	return nil
}

func loadPhysicalFunctions(ctx context.Context, db database.DBTX, namespaces []string, snapshot *physicalSnapshot) error {
	rows, err := db.Query(ctx, `select namespaces.nspname, functions.proname,
		array(select format_type(argument_type, null)
			from unnest(functions.proargtypes::oid[]) with ordinality as arguments(argument_type, ordinal)
			order by arguments.ordinal),
		functions.prorettype = 'trigger'::regtype, languages.lanname,
		functions.provolatile::text, functions.prokind::text,
		functions.prosecdef, functions.proleakproof, functions.proisstrict,
		functions.proparallel::text, functions.prosrc
		from pg_proc as functions
		join pg_namespace as namespaces on namespaces.oid = functions.pronamespace
		join pg_language as languages on languages.oid = functions.prolang
		where namespaces.nspname = any($1::text[])`, namespaces)
	if err != nil {
		return fmt.Errorf("read physical functions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var spec schema.FunctionSpec
		if err := rows.Scan(
			&spec.Namespace, &spec.Name, &spec.ArgumentTypes, &spec.ReturnsTrigger, &spec.Language,
			&spec.Volatility, &spec.Kind, &spec.SecurityDefiner, &spec.Leakproof, &spec.Strict,
			&spec.Parallel, &spec.BodySQL,
		); err != nil {
			return fmt.Errorf("scan physical function: %w", err)
		}
		snapshot.functions[functionRef(spec.Namespace, spec.Name)] = spec
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate physical functions: %w", err)
	}
	return nil
}
