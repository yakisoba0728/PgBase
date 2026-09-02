package doctor

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/webisnoteasy/PgBase/internal/database"
)

var membershipTableNames = []string{
	"record_membership_capability",
	"collection_creation_counters",
	"record_creation_ordinals",
	"record_membership_backfills",
	"runtime_writer_leases",
}

type expectedMembershipColumn struct {
	name, typeName string
	notNull        bool
	defaultSQL     string
}

var expectedMembershipColumns = map[string][]expectedMembershipColumn{
	"record_membership_capability": {
		{name: "singleton", typeName: "boolean", notNull: true, defaultSQL: "true"},
		{name: "state", typeName: "text", notNull: true},
		{name: "generation", typeName: "bigint", notNull: true},
		{name: "minimum_writer_version", typeName: "smallint", notNull: true},
		{name: "cursor_issue_version", typeName: "smallint", notNull: true},
		{name: "cutover_at", typeName: "timestamp with time zone"},
		{name: "cutover_collection_count", typeName: "bigint"},
		{name: "cutover_record_count", typeName: "bigint"},
		{name: "ready_at", typeName: "timestamp with time zone"},
		{name: "ready_collection_count", typeName: "bigint"},
		{name: "v5_enabled_at", typeName: "timestamp with time zone"},
		{name: "v4_decode_until", typeName: "timestamp with time zone"},
		{name: "v4_retired_at", typeName: "timestamp with time zone"},
		{name: "updated_at", typeName: "timestamp with time zone", notNull: true},
	},
	"collection_creation_counters": {
		{name: "collection_id", typeName: "uuid", notNull: true},
		{name: "generation", typeName: "bigint", notNull: true},
		{name: "last_ordinal", typeName: "bigint", notNull: true},
		{name: "updated_at", typeName: "timestamp with time zone", notNull: true},
	},
	"record_creation_ordinals": {
		{name: "collection_id", typeName: "uuid", notNull: true},
		{name: "record_id", typeName: "uuid", notNull: true},
		{name: "ordinal", typeName: "bigint", notNull: true},
	},
	"record_membership_backfills": {
		{name: "collection_id", typeName: "uuid", notNull: true},
		{name: "generation", typeName: "bigint", notNull: true},
		{name: "state", typeName: "text", notNull: true},
		{name: "cutover_count", typeName: "bigint"},
		{name: "cutover_last_created_at", typeName: "timestamp with time zone"},
		{name: "cutover_last_record_id", typeName: "uuid"},
		{name: "checkpoint_created_at", typeName: "timestamp with time zone"},
		{name: "checkpoint_record_id", typeName: "uuid"},
		{name: "last_assigned_ordinal", typeName: "bigint", notNull: true, defaultSQL: "0"},
		{name: "validated_base_count", typeName: "bigint"},
		{name: "validated_sidecar_count", typeName: "bigint"},
		{name: "updated_at", typeName: "timestamp with time zone", notNull: true},
		{name: "completed_at", typeName: "timestamp with time zone"},
	},
	"runtime_writer_leases": {
		{name: "instance_id", typeName: "uuid", notNull: true},
		{name: "writer_version", typeName: "smallint", notNull: true},
		{name: "started_at", typeName: "timestamp with time zone", notNull: true},
		{name: "heartbeat_at", typeName: "timestamp with time zone", notNull: true},
		{name: "lease_expires_at", typeName: "timestamp with time zone", notNull: true},
	},
}

type expectedMembershipKey struct {
	table, name, kind string
	columns           []string
}

var expectedMembershipKeys = []expectedMembershipKey{
	{table: "record_membership_capability", name: "record_membership_capability_pkey", kind: "p", columns: []string{"singleton"}},
	{table: "collection_creation_counters", name: "collection_creation_counters_pkey", kind: "p", columns: []string{"collection_id"}},
	{table: "collection_creation_counters", name: "collection_creation_counters_generation_unique", kind: "u", columns: []string{"collection_id", "generation"}},
	{table: "record_creation_ordinals", name: "record_creation_ordinals_pkey", kind: "p", columns: []string{"collection_id", "record_id"}},
	{table: "record_creation_ordinals", name: "record_creation_ordinals_collection_ordinal_unique", kind: "u", columns: []string{"collection_id", "ordinal"}},
	{table: "record_membership_backfills", name: "record_membership_backfills_pkey", kind: "p", columns: []string{"collection_id"}},
	{table: "runtime_writer_leases", name: "runtime_writer_leases_pkey", kind: "p", columns: []string{"instance_id"}},
}

type expectedMembershipForeignKey struct {
	table, name       string
	columns           []string
	target            string
	targetColumns     []string
	deferrable        bool
	initiallyDeferred bool
}

var expectedMembershipForeignKeys = []expectedMembershipForeignKey{
	{
		table: "collection_creation_counters", name: "collection_creation_counters_collection_id_fkey",
		columns: []string{"collection_id"}, target: "collections", targetColumns: []string{"id"},
	},
	{
		table: "record_creation_ordinals", name: "record_creation_ordinals_counter_fk",
		columns: []string{"collection_id"}, target: "collection_creation_counters", targetColumns: []string{"collection_id"},
	},
	{
		table: "record_membership_backfills", name: "record_membership_backfills_counter_fk",
		columns: []string{"collection_id", "generation"}, target: "collection_creation_counters",
		targetColumns: []string{"collection_id", "generation"}, deferrable: true, initiallyDeferred: true,
	},
}

// The hashes pin PostgreSQL's deparse of every v7 CHECK expression.
// This prevents a same-named but weakened replacement from passing doctor.
var expectedMembershipCheckHashes = map[string]string{
	"collection_creation_counters\x00collection_creation_counters_generation_check":               "00726b650afae1ba30ebbfaa9ed74ddbcd9764c3374345862b1f9ade7d0131c8",
	"collection_creation_counters\x00collection_creation_counters_last_ordinal_check":             "9b7a6e275b0afe8d647308ae060c9ac3d213a835680bd3a1a76880433f535c16",
	"record_creation_ordinals\x00record_creation_ordinals_ordinal_check":                          "45428176598048c694905e07e39ea286597e1678464d34435c5463982182d650",
	"record_membership_backfills\x00record_membership_backfills_checkpoint_bound_check":           "a5bde79b335bad0a73c703c4c2c91bd4b8c7e0b283a60021f66c1fd89ae49bfa",
	"record_membership_backfills\x00record_membership_backfills_checkpoint_check":                 "eb4f923550fb0455b285dec0b86bf8bde10c8c29e287781f315f251b73aab4a0",
	"record_membership_backfills\x00record_membership_backfills_cutover_bound_check":              "b7de46fcf40785f7741a3d9d782eb0d2259de92ee7f71d34c2dfc2693082612c",
	"record_membership_backfills\x00record_membership_backfills_cutover_count_check":              "ba8dcefeb4cf2a2d1d3eefaa5edc80b576f17a3d615c7f89adb2d29be27c0ee5",
	"record_membership_backfills\x00record_membership_backfills_generation_check":                 "00726b650afae1ba30ebbfaa9ed74ddbcd9764c3374345862b1f9ade7d0131c8",
	"record_membership_backfills\x00record_membership_backfills_last_assigned_ordinal_check":      "781b04f79b8309f7a431e27c272509785b0fca1d41194247e9a75e7123c36f27",
	"record_membership_backfills\x00record_membership_backfills_state_check":                      "eef426e238964ae562f705846a03bfa88310eb94f4c97e0d44b9c2a620c6c542",
	"record_membership_backfills\x00record_membership_backfills_state_machine_check":              "fd6cace9584791c4dcea4e524466d0ab6f3d27378bbe7f9243f041e769cdf9bd",
	"record_membership_backfills\x00record_membership_backfills_validated_base_count_check":       "ef8b63d2247a56bff30b59ad11754f4d4adc9baee1df087e95fe1ceb2960f548",
	"record_membership_backfills\x00record_membership_backfills_validated_sidecar_count_check":    "6ebf191550b8019b445d1816f15a837027ad32c3326159b89c51a19d5ad24b6d",
	"record_membership_backfills\x00record_membership_backfills_validation_counts_check":          "70b6613c8084c1518930b5574320faeac2f28815aa4328135e1abe3b42b6258e",
	"record_membership_capability\x00record_membership_capability_cursor_issue_version_check":     "fc7f34643112f57d584742231f1918cd05b27e4cb98c04ee2c49df37656f70cb",
	"record_membership_capability\x00record_membership_capability_cutover_collection_count_check": "1b64db404ff56d5c499a05c770a421da432f1d684c0b27f312d11f2b257a41f4",
	"record_membership_capability\x00record_membership_capability_cutover_record_count_check":     "3854a93ed1c63ef352ea6e91182b7b6e6ece2623826d06f9645168ceb7b1d07d",
	"record_membership_capability\x00record_membership_capability_generation_check":               "00726b650afae1ba30ebbfaa9ed74ddbcd9764c3374345862b1f9ade7d0131c8",
	"record_membership_capability\x00record_membership_capability_minimum_writer_version_check":   "7e9a6b187f3c379e209ada47f6b048b32856641a4433fdd182e56334978e24d1",
	"record_membership_capability\x00record_membership_capability_ready_collection_count_check":   "6a50d349d1b7c2449a6e7c36146f26f484654e27eaf8488e1671cba1130ce57f",
	"record_membership_capability\x00record_membership_capability_rollout_state_check":            "99eabe490ff077df52b1a9ac907301d9e4561351c395d7f0374c30c67c861cf6",
	"record_membership_capability\x00record_membership_capability_singleton_check":                "e234ee35f82c5dd3f2f20ef0835225e326805e56b5fe83ee5e56c0f899f4901d",
	"record_membership_capability\x00record_membership_capability_state_check":                    "4dfbd8bf3bbc1db7dd88edcfd1c1df19812cb1d2598239896526f57f8d2264de",
	"record_membership_capability\x00record_membership_capability_timeline_check":                 "9927c9734e2d7a9cadcd688858c2b68ed6bae55a6a40a5011e8bbbbf90ee8b0d",
	"runtime_writer_leases\x00runtime_writer_leases_timeline_check":                               "fd840aaabfcab0b8b788b13d88bd75668943981e6a9bf20f45beab844b2071b7",
	"runtime_writer_leases\x00runtime_writer_leases_writer_version_check":                         "b8bf7eeabdbdb3c791614d68e9aa7ca35b5a0488d775061146fbdea89691d1a8",
}

type expectedMembershipIndex struct {
	table, name, predicate string
	columns                []string
	unique, primary        bool
}

var expectedMembershipIndexes = []expectedMembershipIndex{
	{table: "record_membership_capability", name: "record_membership_capability_pkey", columns: []string{"singleton"}, unique: true, primary: true},
	{table: "collection_creation_counters", name: "collection_creation_counters_pkey", columns: []string{"collection_id"}, unique: true, primary: true},
	{table: "collection_creation_counters", name: "collection_creation_counters_generation_unique", columns: []string{"collection_id", "generation"}, unique: true},
	{table: "record_creation_ordinals", name: "record_creation_ordinals_pkey", columns: []string{"collection_id", "record_id"}, unique: true, primary: true},
	{table: "record_creation_ordinals", name: "record_creation_ordinals_collection_ordinal_unique", columns: []string{"collection_id", "ordinal"}, unique: true},
	{table: "record_membership_backfills", name: "record_membership_backfills_pkey", columns: []string{"collection_id"}, unique: true, primary: true},
	{table: "record_membership_backfills", name: "record_membership_backfills_work_idx", columns: []string{"generation", "collection_id"}, predicate: "(state = 'backfilling'::text)"},
	{table: "runtime_writer_leases", name: "runtime_writer_leases_pkey", columns: []string{"instance_id"}, unique: true, primary: true},
	{table: "runtime_writer_leases", name: "runtime_writer_leases_expiry_idx", columns: []string{"lease_expires_at", "writer_version"}},
}

type membershipPhysicalCatalog struct {
	tablesByName map[string]*physicalTable
	tablesByOID  map[int64]*physicalTable
	constraints  map[string]physicalConstraint
	indexes      map[string]physicalIndex
	predicates   map[string]string
}

func recordMembershipCatalogValid(ctx context.Context, db database.DBTX) (bool, error) {
	catalog, err := loadMembershipPhysicalCatalog(ctx, db)
	if err != nil {
		return false, err
	}
	for tableName, columns := range expectedMembershipColumns {
		table := catalog.tablesByName[tableName]
		if !membershipColumnsMatch(table, columns) {
			return false, nil
		}
	}
	if !membershipConstraintsMatch(catalog) || !membershipIndexesMatch(catalog) {
		return false, nil
	}
	return true, nil
}

func loadMembershipPhysicalCatalog(ctx context.Context, db database.DBTX) (membershipPhysicalCatalog, error) {
	catalog := membershipPhysicalCatalog{
		tablesByName: make(map[string]*physicalTable),
		tablesByOID:  make(map[int64]*physicalTable),
		constraints:  make(map[string]physicalConstraint),
		indexes:      make(map[string]physicalIndex),
		predicates:   make(map[string]string),
	}
	tableNames := append([]string{"collections"}, membershipTableNames...)
	if err := loadMembershipTables(ctx, db, &catalog, tableNames); err != nil {
		return membershipPhysicalCatalog{}, err
	}
	if err := loadMembershipConstraints(ctx, db, &catalog); err != nil {
		return membershipPhysicalCatalog{}, err
	}
	if err := loadMembershipIndexes(ctx, db, &catalog); err != nil {
		return membershipPhysicalCatalog{}, err
	}
	return catalog, nil
}

func loadMembershipTables(ctx context.Context, db database.DBTX, catalog *membershipPhysicalCatalog, tableNames []string) error {
	rows, err := db.Query(ctx, `select tables.oid::bigint, tables.relname,
		tables.relkind::text, tables.relpersistence::text,
		columns.attnum::integer, columns.attname,
		format_type(columns.atttypid, columns.atttypmod), columns.attnotnull,
		pg_get_expr(defaults.adbin, defaults.adrelid)
		from pg_class as tables
		join pg_namespace as schemas on schemas.oid = tables.relnamespace
		left join pg_attribute as columns
			on columns.attrelid = tables.oid and columns.attnum > 0 and not columns.attisdropped
		left join pg_attrdef as defaults
			on defaults.adrelid = tables.oid and defaults.adnum = columns.attnum
		where schemas.nspname = 'pgbase_meta' and tables.relname = any($1::text[])
		order by tables.oid, columns.attnum nulls first`, tableNames)
	if err != nil {
		return fmt.Errorf("read record membership tables: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var oid int64
		var name, relKind, persistence string
		var attribute *int
		var columnName, typeName *string
		var notNull *bool
		var defaultSQL *string
		if err := rows.Scan(&oid, &name, &relKind, &persistence, &attribute, &columnName, &typeName, &notNull, &defaultSQL); err != nil {
			return fmt.Errorf("scan record membership table: %w", err)
		}
		table := catalog.tablesByOID[oid]
		if table == nil {
			table = &physicalTable{
				oid: oid, schema: "pgbase_meta", name: name, relKind: relKind,
				persistence: persistence, columns: make(map[string]physicalColumn),
			}
			catalog.tablesByOID[oid] = table
			catalog.tablesByName[name] = table
		}
		if attribute != nil && columnName != nil && typeName != nil && notNull != nil {
			table.columns[*columnName] = physicalColumn{
				typeName: *typeName, notNull: *notNull, defaultSQL: defaultSQL, attribute: int16(*attribute),
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate record membership tables: %w", err)
	}
	return nil
}

func loadMembershipConstraints(ctx context.Context, db database.DBTX, catalog *membershipPhysicalCatalog) error {
	rows, err := db.Query(ctx, `select constraints.conrelid::bigint, constraints.conname,
		constraints.contype::text, coalesce(constraints.conkey::smallint[], '{}'::smallint[]),
		constraints.confrelid::bigint, coalesce(constraints.confkey::smallint[], '{}'::smallint[]),
		constraints.confdeltype::text, constraints.confupdtype::text,
		constraints.confmatchtype::text, constraints.condeferrable,
		constraints.condeferred, constraints.convalidated,
		pg_get_expr(constraints.conbin, constraints.conrelid)
		from pg_constraint as constraints
		join pg_class as tables on tables.oid = constraints.conrelid
		join pg_namespace as schemas on schemas.oid = tables.relnamespace
		where schemas.nspname = 'pgbase_meta' and tables.relname = any($1::text[])
		  and constraints.contype in ('p', 'u', 'f', 'c')
		order by constraints.conrelid, constraints.conname`, membershipTableNames)
	if err != nil {
		return fmt.Errorf("read record membership constraints: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var constraint physicalConstraint
		if err := rows.Scan(
			&constraint.tableOID, &constraint.name, &constraint.constraintType,
			&constraint.columns, &constraint.targetTableOID, &constraint.targetColumns,
			&constraint.deleteAction, &constraint.updateAction, &constraint.matchType,
			&constraint.deferrable, &constraint.deferred, &constraint.validated,
			&constraint.checkExpression,
		); err != nil {
			return fmt.Errorf("scan record membership constraint: %w", err)
		}
		catalog.constraints[ownedObjectKey(constraint.tableOID, constraint.name)] = constraint
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate record membership constraints: %w", err)
	}
	return nil
}

func loadMembershipIndexes(ctx context.Context, db database.DBTX, catalog *membershipPhysicalCatalog) error {
	rows, err := db.Query(ctx, `select indexes.indrelid::bigint, index_tables.relname,
		access_methods.amname, indexes.indkey::smallint[], indexes.indoption::smallint[],
		indexes.indisunique, indexes.indisprimary,
		indexes.indisvalid, indexes.indisready, indexes.indislive,
		indexes.indnkeyatts::integer, indexes.indnatts::integer,
		pg_get_expr(indexes.indpred, indexes.indrelid), indexes.indexprs is not null
		from pg_index as indexes
		join pg_class as tables on tables.oid = indexes.indrelid
		join pg_namespace as schemas on schemas.oid = tables.relnamespace
		join pg_class as index_tables on index_tables.oid = indexes.indexrelid
		join pg_am as access_methods on access_methods.oid = index_tables.relam
		where schemas.nspname = 'pgbase_meta' and tables.relname = any($1::text[])
		order by indexes.indrelid, index_tables.relname`, membershipTableNames)
	if err != nil {
		return fmt.Errorf("read record membership indexes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var index physicalIndex
		var predicate *string
		if err := rows.Scan(
			&index.tableOID, &index.name, &index.method, &index.columns, &index.options,
			&index.unique, &index.primary, &index.valid, &index.ready, &index.live,
			&index.keyCount, &index.attributeCount, &predicate, &index.hasExpression,
		); err != nil {
			return fmt.Errorf("scan record membership index: %w", err)
		}
		index.hasPredicate = predicate != nil
		catalog.indexes[ownedObjectKey(index.tableOID, index.name)] = index
		if predicate != nil {
			catalog.predicates[ownedObjectKey(index.tableOID, index.name)] = *predicate
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate record membership indexes: %w", err)
	}
	return nil
}

func membershipColumnsMatch(table *physicalTable, expected []expectedMembershipColumn) bool {
	if table == nil || table.relKind != "r" || table.persistence != "p" || len(table.columns) != len(expected) {
		return false
	}
	for _, want := range expected {
		got, ok := table.columns[want.name]
		if !ok || got.typeName != want.typeName || got.notNull != want.notNull {
			return false
		}
		if want.defaultSQL == "" {
			if got.defaultSQL != nil {
				return false
			}
		} else if got.defaultSQL == nil || compactSQL(*got.defaultSQL) != want.defaultSQL {
			return false
		}
	}
	return true
}

func membershipConstraintsMatch(catalog membershipPhysicalCatalog) bool {
	wantCount := len(expectedMembershipKeys) + len(expectedMembershipForeignKeys) + len(expectedMembershipCheckHashes)
	if len(catalog.constraints) != wantCount {
		return false
	}
	for _, want := range expectedMembershipKeys {
		table := catalog.tablesByName[want.table]
		if table == nil {
			return false
		}
		constraint, ok := catalog.constraints[ownedObjectKey(table.oid, want.name)]
		if !ok || !keyConstraintMatches(constraint, table, want.kind, want.columns) {
			return false
		}
	}
	for _, want := range expectedMembershipForeignKeys {
		table := catalog.tablesByName[want.table]
		target := catalog.tablesByName[want.target]
		if table == nil || target == nil {
			return false
		}
		constraint, ok := catalog.constraints[ownedObjectKey(table.oid, want.name)]
		if !ok || !foreignKeyMatches(
			constraint, table, want.columns, target, want.targetColumns,
			"c", want.deferrable, want.initiallyDeferred,
		) {
			return false
		}
	}
	for key, expectedHash := range expectedMembershipCheckHashes {
		separator := -1
		for index := range key {
			if key[index] == 0 {
				separator = index
				break
			}
		}
		if separator < 0 {
			return false
		}
		table := catalog.tablesByName[key[:separator]]
		if table == nil {
			return false
		}
		constraint, ok := catalog.constraints[ownedObjectKey(table.oid, key[separator+1:])]
		if !ok || constraint.tableOID != table.oid || constraint.constraintType != "c" ||
			constraint.deferrable || constraint.deferred || !constraint.validated || constraint.checkExpression == nil {
			return false
		}
		if membershipCheckHash(*constraint.checkExpression) != expectedHash {
			return false
		}
	}
	return true
}

func membershipCheckHash(expression string) string {
	digest := sha256.Sum256([]byte(expression))
	return fmt.Sprintf("%x", digest)
}

func membershipIndexesMatch(catalog membershipPhysicalCatalog) bool {
	if len(catalog.indexes) != len(expectedMembershipIndexes) {
		return false
	}
	for _, want := range expectedMembershipIndexes {
		table := catalog.tablesByName[want.table]
		if table == nil {
			return false
		}
		key := ownedObjectKey(table.oid, want.name)
		index, ok := catalog.indexes[key]
		if !ok || index.tableOID != table.oid || index.method != "btree" ||
			index.unique != want.unique || index.primary != want.primary ||
			!index.valid || !index.ready || !index.live || index.hasExpression ||
			index.keyCount != len(want.columns) || index.attributeCount != len(want.columns) ||
			!indexOptionsAreDefault(index.options, len(want.columns)) ||
			!columnNumbersMatch(table, index.columns, want.columns) {
			return false
		}
		predicate, hasPredicate := catalog.predicates[key]
		if (want.predicate != "") != hasPredicate || hasPredicate && predicate != want.predicate {
			return false
		}
	}
	return true
}
