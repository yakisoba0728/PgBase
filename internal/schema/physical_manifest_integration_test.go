//go:build integration

package schema

import (
	"context"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/webisnoteasy/PgBase/internal/database"
)

func TestExpectedPhysicalSchemaMatchesPlannerCatalogObjects(t *testing.T) {
	pool, service, administratorID := newSchemaServiceTest(t)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "users"}, administratorID)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "tags"}, administratorID)
	author := relationPlannerField("author", "users", RelationSingle, false, 0, RelationRestrict)
	author.Unique = true
	applyFreshPlan(t, service, CreateCollectionInput{
		Name: "posts",
		Fields: []FieldInput{
			{Name: "title", Type: TypeText, Required: true, Indexed: true},
			author,
			relationPlannerField("tags", "tags", RelationMultiple, true, 5, RelationRestrict),
		},
	}, administratorID)

	ctx := context.Background()
	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}
	manifest, err := ExpectedPhysicalSchemaForCatalog(catalog)
	if err != nil {
		t.Fatalf("compile physical manifest: %v", err)
	}
	required := manifest.Required()

	var serverVersion int
	if err := pool.QueryRow(ctx, `select current_setting('server_version_num')::integer`).Scan(&serverVersion); err != nil {
		t.Fatalf("read PostgreSQL version: %v", err)
	}
	assertPhysicalObjectCounts(t, pool, required)
	for _, table := range required.Tables {
		assertPostgresTableSpec(t, pool, table)
	}
	for _, column := range required.Columns {
		assertPostgresColumnSpec(t, pool, column)
	}
	for _, index := range required.Indexes {
		assertPostgresIndexSpec(t, pool, index)
	}
	for _, constraint := range required.Constraints {
		assertPostgresConstraintSpec(t, pool, constraint, serverVersion)
	}
	for _, trigger := range required.Triggers {
		assertPostgresTriggerSpec(t, pool, trigger)
	}
	for _, function := range required.Functions {
		assertPostgresFunctionSpec(t, pool, function)
	}
}

func assertPhysicalObjectCounts(t *testing.T, db database.DBTX, required RequiredPhysicalObjects) {
	t.Helper()
	ctx := context.Background()
	checks := []struct {
		name  string
		query string
		want  int
	}{
		{
			name: "namespaces",
			query: `select count(*) from pg_namespace
				where nspname in ('app', 'pgbase_relation')`,
			want: len(required.Namespaces),
		},
		{
			name: "tables",
			query: `select count(*) from pg_class as tables
				join pg_namespace as namespaces on namespaces.oid = tables.relnamespace
				where namespaces.nspname in ('app', 'pgbase_relation') and tables.relkind in ('r', 'p')`,
			want: len(required.Tables),
		},
		{
			name: "columns",
			query: `select count(*) from pg_attribute as columns
				join pg_class as tables on tables.oid = columns.attrelid
				join pg_namespace as namespaces on namespaces.oid = tables.relnamespace
				where namespaces.nspname in ('app', 'pgbase_relation') and tables.relkind in ('r', 'p')
					and columns.attnum > 0 and not columns.attisdropped`,
			want: len(required.Columns),
		},
		{
			name: "indexes",
			query: `select count(*) from pg_index as indexes
				join pg_class as tables on tables.oid = indexes.indrelid
				join pg_namespace as namespaces on namespaces.oid = tables.relnamespace
				where namespaces.nspname in ('app', 'pgbase_relation')`,
			want: len(required.Indexes),
		},
		{
			name: "standalone constraints",
			query: `select count(*) from pg_constraint as constraints
				join pg_class as tables on tables.oid = constraints.conrelid
				join pg_namespace as namespaces on namespaces.oid = tables.relnamespace
				where namespaces.nspname in ('app', 'pgbase_relation') and constraints.contype <> 'n'`,
			want: len(required.Constraints),
		},
		{
			name: "triggers",
			query: `select count(*) from pg_trigger as triggers
				join pg_class as tables on tables.oid = triggers.tgrelid
				join pg_namespace as namespaces on namespaces.oid = tables.relnamespace
				where namespaces.nspname in ('app', 'pgbase_relation') and not triggers.tgisinternal`,
			want: len(required.Triggers),
		},
		{
			name: "functions",
			query: `select count(*) from pg_proc as functions
				join pg_namespace as namespaces on namespaces.oid = functions.pronamespace
				where namespaces.nspname = 'pgbase_relation'`,
			want: len(required.Functions),
		},
	}
	for _, check := range checks {
		var got int
		if err := db.QueryRow(ctx, check.query).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if got != check.want {
			t.Errorf("PostgreSQL %s count = %d, manifest count = %d", check.name, got, check.want)
		}
	}
}

func assertPostgresTableSpec(t *testing.T, db database.DBTX, want TableSpec) {
	t.Helper()
	var kind, persistence string
	err := db.QueryRow(context.Background(), `select tables.relkind::text, tables.relpersistence::text
		from pg_class as tables
		join pg_namespace as namespaces on namespaces.oid = tables.relnamespace
		where namespaces.nspname = $1 and tables.relname = $2`, want.Namespace, want.Name).Scan(&kind, &persistence)
	if err != nil {
		t.Fatalf("read table %s.%s: %v", want.Namespace, want.Name, err)
	}
	if kind != want.Kind || persistence != want.Persistence {
		t.Errorf("table %s.%s = kind %q persistence %q, want %q %q", want.Namespace, want.Name, kind, persistence, want.Kind, want.Persistence)
	}
}

func assertPostgresColumnSpec(t *testing.T, db database.DBTX, want ColumnSpec) {
	t.Helper()
	var dataType string
	var notNull bool
	var defaultSQL string
	err := db.QueryRow(context.Background(), `select format_type(columns.atttypid, columns.atttypmod),
		columns.attnotnull, coalesce(pg_get_expr(defaults.adbin, defaults.adrelid), '')
		from pg_attribute as columns
		join pg_class as tables on tables.oid = columns.attrelid
		join pg_namespace as namespaces on namespaces.oid = tables.relnamespace
		left join pg_attrdef as defaults on defaults.adrelid = tables.oid and defaults.adnum = columns.attnum
		where namespaces.nspname = $1 and tables.relname = $2 and columns.attname = $3
			and columns.attnum > 0 and not columns.attisdropped`, want.Namespace, want.Table, want.Name).
		Scan(&dataType, &notNull, &defaultSQL)
	if err != nil {
		t.Fatalf("read column %s.%s.%s: %v", want.Namespace, want.Table, want.Name, err)
	}
	if dataType != want.DataType || notNull != want.NotNull || compactManifestSQL(defaultSQL) != compactManifestSQL(want.DefaultSQL) {
		t.Errorf("column %s.%s.%s = type %q not-null %t default %q, want %q %t %q",
			want.Namespace, want.Table, want.Name, dataType, notNull, defaultSQL, want.DataType, want.NotNull, want.DefaultSQL)
	}
}

func assertPostgresIndexSpec(t *testing.T, db database.DBTX, want IndexSpec) {
	t.Helper()
	names := append([]string{want.Name}, want.CompatibleNames...)
	var name, method, constraintName, predicateSQL, expressionSQL string
	var columns, includedColumns, operatorClasses []string
	var options []int16
	var defaultOperatorClasses []bool
	var unique, nullsNotDistinct, primary, valid, ready, live bool
	err := db.QueryRow(context.Background(), `select index_tables.relname, access_methods.amname,
		array(select attributes.attname
			from unnest(indexes.indkey::smallint[]) with ordinality as keys(attribute_number, ordinal)
			join pg_attribute as attributes
				on attributes.attrelid = indexes.indrelid and attributes.attnum = keys.attribute_number
			where keys.ordinal <= indexes.indnkeyatts order by keys.ordinal),
		indexes.indoption::smallint[],
		array(select operator_classes.opcname
			from unnest(indexes.indclass::oid[]) with ordinality as classes(operator_class, ordinal)
			join pg_opclass as operator_classes on operator_classes.oid = classes.operator_class
			where classes.ordinal <= indexes.indnkeyatts order by classes.ordinal),
		array(select operator_classes.opcdefault
			from unnest(indexes.indclass::oid[]) with ordinality as classes(operator_class, ordinal)
			join pg_opclass as operator_classes on operator_classes.oid = classes.operator_class
			where classes.ordinal <= indexes.indnkeyatts order by classes.ordinal),
		array(select attributes.attname
			from unnest(indexes.indkey::smallint[]) with ordinality as keys(attribute_number, ordinal)
			join pg_attribute as attributes
				on attributes.attrelid = indexes.indrelid and attributes.attnum = keys.attribute_number
			where keys.ordinal > indexes.indnkeyatts order by keys.ordinal),
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
		where namespaces.nspname = $1 and tables.relname = $2 and index_tables.relname = any($3::text[])
		order by index_tables.relname = $4 desc limit 1`, want.Namespace, want.Table, names, want.Name).Scan(
		&name, &method, &columns, &options, &operatorClasses, &defaultOperatorClasses, &includedColumns,
		&unique, &nullsNotDistinct, &primary, &constraintName, &predicateSQL, &expressionSQL, &valid, &ready, &live,
	)
	if err != nil {
		t.Fatalf("read index %s.%s.%s: %v", want.Namespace, want.Table, want.Name, err)
	}
	if name != want.Name && !containsString(want.CompatibleNames, name) {
		t.Errorf("index name = %q, want %q or %v", name, want.Name, want.CompatibleNames)
	}
	wantColumns := make([]string, len(want.Keys))
	for index, key := range want.Keys {
		wantColumns[index] = key.Column
		option := int16(0)
		if key.Descending {
			option |= 1
		}
		if key.NullsFirst {
			option |= 2
		}
		if index >= len(options) || options[index] != option {
			t.Errorf("index %s key %d option = %v, want %d", want.Name, index, options, option)
		}
		if index >= len(operatorClasses) || index >= len(defaultOperatorClasses) ||
			(key.OperatorClass == "" && !defaultOperatorClasses[index]) ||
			(key.OperatorClass != "" && operatorClasses[index] != key.OperatorClass) {
			t.Errorf("index %s key %d operator class = %v default=%v, want %q", want.Name, index, operatorClasses, defaultOperatorClasses, key.OperatorClass)
		}
	}
	if !slices.Equal(columns, wantColumns) || !slices.Equal(includedColumns, want.IncludedColumns) ||
		method != want.Method || unique != want.Unique || nullsNotDistinct != want.NullsNotDistinct ||
		primary != want.Primary || constraintName != want.ConstraintName ||
		compactManifestSQL(predicateSQL) != compactManifestSQL(want.PredicateSQL) ||
		compactManifestSQL(expressionSQL) != compactManifestSQL(want.ExpressionSQL) ||
		valid != want.Valid || ready != want.Ready || live != want.Live {
		t.Errorf("index %s.%s.%s differs: columns=%v include=%v method=%q unique=%t nulls-not-distinct=%t primary=%t constraint=%q predicate=%q expression=%q valid=%t ready=%t live=%t",
			want.Namespace, want.Table, want.Name, columns, includedColumns, method, unique, nullsNotDistinct, primary,
			constraintName, predicateSQL, expressionSQL, valid, ready, live)
	}
}

func assertPostgresConstraintSpec(t *testing.T, db database.DBTX, want ConstraintSpec, serverVersion int) {
	t.Helper()
	enforcedExpression := "true"
	if serverVersion >= 180000 {
		enforcedExpression = "constraints.conenforced"
	}
	query := `select constraints.contype::text,
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
		constraints.condeferrable, constraints.condeferred, constraints.convalidated, ` + enforcedExpression + `,
		constraints.connoinherit, coalesce(pg_get_expr(constraints.conbin, constraints.conrelid), '')
		from pg_constraint as constraints
		join pg_class as tables on tables.oid = constraints.conrelid
		join pg_namespace as namespaces on namespaces.oid = tables.relnamespace
		left join pg_class as target_tables on target_tables.oid = constraints.confrelid
		left join pg_namespace as target_namespaces on target_namespaces.oid = target_tables.relnamespace
		where namespaces.nspname = $1 and tables.relname = $2 and constraints.conname = $3`
	var kind, referenceNamespace, referenceTable, match, onUpdate, onDelete, checkSQL string
	var columns, referenceColumns []string
	var deferrable, initiallyDeferred, validated, enforced, noInherit bool
	err := db.QueryRow(context.Background(), query, want.Namespace, want.Table, want.Name).Scan(
		&kind, &columns, &referenceNamespace, &referenceTable, &referenceColumns,
		&match, &onUpdate, &onDelete, &deferrable, &initiallyDeferred, &validated, &enforced, &noInherit, &checkSQL,
	)
	if err != nil {
		t.Fatalf("read constraint %s.%s.%s: %v", want.Namespace, want.Table, want.Name, err)
	}
	if kind != want.Kind || !slices.Equal(columns, want.Columns) ||
		referenceNamespace != want.ReferenceNamespace || referenceTable != want.ReferenceTable ||
		!slices.Equal(referenceColumns, want.ReferenceColumns) || match != want.Match ||
		onUpdate != want.OnUpdate || onDelete != want.OnDelete || deferrable != want.Deferrable ||
		initiallyDeferred != want.InitiallyDeferred || validated != want.Validated || enforced != want.Enforced ||
		noInherit != want.NoInherit || compactManifestSQL(checkSQL) != compactManifestSQL(want.CheckSQL) {
		t.Errorf("constraint %s.%s.%s differs: kind=%q columns=%v reference=%s.%s%v match=%q update=%q delete=%q deferrable=%t deferred=%t validated=%t enforced=%t no-inherit=%t check=%q",
			want.Namespace, want.Table, want.Name, kind, columns, referenceNamespace, referenceTable, referenceColumns,
			match, onUpdate, onDelete, deferrable, initiallyDeferred, validated, enforced, noInherit, checkSQL)
	}
}

func assertPostgresTriggerSpec(t *testing.T, db database.DBTX, want TriggerSpec) {
	t.Helper()
	var functionNamespace, functionName, enabled, whenSQL, oldTable, newTable string
	var functionArgumentCount, typeMask, argumentCount int
	var updateColumns []string
	var constraint, deferrable, initiallyDeferred bool
	err := db.QueryRow(context.Background(), `select function_namespaces.nspname, functions.proname,
		functions.pronargs::integer, triggers.tgtype::integer,
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
		where namespaces.nspname = $1 and tables.relname = $2 and triggers.tgname = $3 and not triggers.tgisinternal`,
		want.Namespace, want.Table, want.Name).Scan(
		&functionNamespace, &functionName, &functionArgumentCount, &typeMask, &updateColumns,
		&enabled, &constraint, &deferrable, &initiallyDeferred, &argumentCount, &whenSQL, &oldTable, &newTable,
	)
	if err != nil {
		t.Fatalf("read trigger %s.%s.%s: %v", want.Namespace, want.Table, want.Name, err)
	}
	if functionNamespace != want.FunctionNamespace || functionName != want.FunctionName ||
		functionArgumentCount != len(want.FunctionArgumentTypes) || typeMask != want.TypeMask ||
		!slices.Equal(updateColumns, want.UpdateColumns) || enabled != want.Enabled ||
		constraint != want.Constraint || deferrable != want.Deferrable || initiallyDeferred != want.InitiallyDeferred ||
		argumentCount != len(want.Arguments) || compactManifestSQL(whenSQL) != compactManifestSQL(want.WhenSQL) ||
		oldTable != want.OldTransitionTable || newTable != want.NewTransitionTable {
		t.Errorf("trigger %s.%s.%s differs: function=%s.%s/%d type=%d update-columns=%v enabled=%q constraint=%t deferrable=%t deferred=%t args=%d when=%q old=%q new=%q",
			want.Namespace, want.Table, want.Name, functionNamespace, functionName, functionArgumentCount, typeMask,
			updateColumns, enabled, constraint, deferrable, initiallyDeferred, argumentCount, whenSQL, oldTable, newTable)
	}
}

func assertPostgresFunctionSpec(t *testing.T, db database.DBTX, want FunctionSpec) {
	t.Helper()
	var argumentCount int
	var returnsTrigger, securityDefiner, leakproof, strict bool
	var language, volatility, kind, parallel, body string
	err := db.QueryRow(context.Background(), `select functions.pronargs::integer,
		functions.prorettype = 'trigger'::regtype, languages.lanname,
		functions.provolatile::text, functions.prokind::text,
		functions.prosecdef, functions.proleakproof, functions.proisstrict,
		functions.proparallel::text, functions.prosrc
		from pg_proc as functions
		join pg_namespace as namespaces on namespaces.oid = functions.pronamespace
		join pg_language as languages on languages.oid = functions.prolang
		where namespaces.nspname = $1 and functions.proname = $2 and functions.pronargs = $3`,
		want.Namespace, want.Name, len(want.ArgumentTypes)).Scan(
		&argumentCount, &returnsTrigger, &language, &volatility, &kind,
		&securityDefiner, &leakproof, &strict, &parallel, &body,
	)
	if err != nil {
		t.Fatalf("read function %s.%s: %v", want.Namespace, want.Name, err)
	}
	if argumentCount != len(want.ArgumentTypes) || returnsTrigger != want.ReturnsTrigger ||
		language != want.Language || volatility != want.Volatility || kind != want.Kind ||
		securityDefiner != want.SecurityDefiner || leakproof != want.Leakproof || strict != want.Strict ||
		parallel != want.Parallel || body != want.BodySQL {
		t.Errorf("function %s.%s differs: args=%d returns-trigger=%t language=%q volatility=%q kind=%q security-definer=%t leakproof=%t strict=%t parallel=%q body-equal=%t",
			want.Namespace, want.Name, argumentCount, returnsTrigger, language, volatility, kind,
			securityDefiner, leakproof, strict, parallel, body == want.BodySQL)
	}
}

func compactManifestSQL(value string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsSpace(character) || character == '"' || character == '(' || character == ')' {
			return -1
		}
		return unicode.ToLower(character)
	}, value)
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
