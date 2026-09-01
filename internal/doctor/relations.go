package doctor

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

// catalogRow is deliberately value-light: findings must never include catalog
// UUIDs or deterministic physical names. Those values are used only as lookup
// keys while the read-only doctor snapshot is open.
type catalogRow struct {
	collectionID             uuid.UUID
	collection, table        string
	fieldID                  *uuid.UUID
	field, column, fieldType *string
	defaultJSON              *string
	required, unique         *bool
	indexed, hasDefault      *bool
}

type relationBinding struct {
	fieldID, sourceCollectionID, targetCollectionID uuid.UUID
	cardinality                                     schema.RelationCardinality
	minimum, maximum                                int
	onDelete                                        schema.RelationDeleteAction
	valueCodecVersion                               int
	storageKind                                     schema.RelationStorageKind
	storageName                                     string
}

type relationCheck struct {
	sourceTable  string
	targetTable  string
	column       string
	required     bool
	unique       bool
	binding      relationBinding
	physicalName schema.RelationPhysicalNames
}

type relationChecks map[uuid.UUID]relationCheck

func readRelationBindings(ctx context.Context, db database.DBTX) ([]relationBinding, error) {
	var relationCatalogPresent bool
	if err := db.QueryRow(ctx, `select to_regclass('pgbase_meta.field_relations') is not null`).Scan(&relationCatalogPresent); err != nil {
		return nil, fmt.Errorf("check relation catalog: %w", err)
	}
	if !relationCatalogPresent {
		return []relationBinding{}, nil
	}
	rows, err := db.Query(ctx, `select field_id, source_collection_id, target_collection_id,
		cardinality, min_select, max_select, on_delete, value_codec_version,
		storage_kind, storage_name
		from pgbase_meta.field_relations
		order by field_id`)
	if err != nil {
		return nil, fmt.Errorf("read relation catalog: %w", err)
	}
	defer rows.Close()

	bindings := make([]relationBinding, 0)
	for rows.Next() {
		var binding relationBinding
		if err := rows.Scan(
			&binding.fieldID,
			&binding.sourceCollectionID,
			&binding.targetCollectionID,
			&binding.cardinality,
			&binding.minimum,
			&binding.maximum,
			&binding.onDelete,
			&binding.valueCodecVersion,
			&binding.storageKind,
			&binding.storageName,
		); err != nil {
			return nil, fmt.Errorf("scan relation catalog: %w", err)
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate relation catalog: %w", err)
	}
	return bindings, nil
}

// classifyRelations validates the catalog-to-binding seam before any physical
// inspection. At most one finding per relation invariant is emitted, keeping a
// badly corrupted large catalog from creating an unbounded diagnostic payload.
func classifyRelations(catalog []catalogRow, bindings []relationBinding, report *Report) relationChecks {
	// Stored text defaults are not a relation invariant, but they are read
	// from the same catalog snapshot and must be reported even when every
	// physical object below is healthy, so the pass runs here first.
	checkOversizedTextDefaults(catalog, report)

	collections := make(map[uuid.UUID]catalogRow, len(catalog))
	fields := make(map[uuid.UUID]catalogRow)
	for _, row := range catalog {
		collections[row.collectionID] = row
		if row.fieldID != nil {
			fields[*row.fieldID] = row
		}
	}

	byField := make(map[uuid.UUID][]relationBinding, len(bindings))
	for _, binding := range bindings {
		byField[binding.fieldID] = append(byField[binding.fieldID], binding)
		if _, ok := fields[binding.fieldID]; !ok {
			appendRelationFinding(report, "relation_metadata_orphaned", "Relation metadata is not bound to a catalog field")
		}
	}

	checks := make(relationChecks)
	for fieldID, row := range fields {
		fieldType := ""
		if row.fieldType != nil {
			fieldType = *row.fieldType
		}
		fieldBindings := byField[fieldID]
		if fieldType != string(schema.TypeRelation) {
			if len(fieldBindings) != 0 {
				appendRelationFinding(report, "relation_metadata_unexpected", "A scalar field has relation metadata")
			}
			continue
		}
		if len(fieldBindings) == 0 {
			appendRelationFinding(report, "relation_metadata_missing", "A relation field is missing its relation metadata")
			continue
		}
		if len(fieldBindings) != 1 {
			appendRelationFinding(report, "relation_metadata_mismatch", "Relation metadata is internally inconsistent")
			continue
		}

		binding := fieldBindings[0]
		target, targetExists := collections[binding.targetCollectionID]
		spec := schema.RelationSpec{
			TargetCollectionID: binding.targetCollectionID,
			Cardinality:        binding.cardinality,
			MinSelect:          binding.minimum,
			MaxSelect:          binding.maximum,
			OnDelete:           binding.onDelete,
			ValueCodecVersion:  binding.valueCodecVersion,
			Storage: schema.RelationStorage{
				Kind: binding.storageKind,
				Name: binding.storageName,
			},
		}

		if row.field == nil || row.column == nil || row.required == nil || row.unique == nil ||
			row.indexed == nil || row.hasDefault == nil {
			appendRelationFinding(report, "relation_metadata_mismatch", "Relation metadata is internally inconsistent")
			continue
		}
		names := schema.NewRelationPhysicalNames(row.table, *row.field)
		storageMatches := false
		switch binding.cardinality {
		case schema.RelationSingle:
			storageMatches = binding.storageKind == schema.RelationStorageInlineFKV1 && binding.storageName == *row.column
		case schema.RelationMultiple:
			storageMatches = binding.storageKind == schema.RelationStorageEdgeTableV1 && binding.storageName == names.MultiTable
		}
		metadataMatches := binding.sourceCollectionID == row.collectionID && targetExists &&
			schema.ValidateRelationSpec("field", spec) == nil && storageMatches &&
			*row.required == (binding.minimum > 0) && *row.indexed && !*row.hasDefault && row.defaultJSON == nil &&
			(binding.cardinality != schema.RelationMultiple || !*row.unique)
		if !metadataMatches {
			appendRelationFinding(report, "relation_metadata_mismatch", "Relation metadata is internally inconsistent")
		}

		// A recognizable cardinality and a live target are enough to inspect the
		// deterministic physical binding even when another metadata member drifted.
		if !targetExists || (binding.cardinality != schema.RelationSingle && binding.cardinality != schema.RelationMultiple) {
			continue
		}
		checks[fieldID] = relationCheck{
			sourceTable: row.table, targetTable: target.table,
			column:   *row.column,
			required: *row.required, unique: *row.unique,
			binding: binding, physicalName: names,
		}
	}
	return checks
}

func fieldUsesCollectionColumn(row catalogRow, relations relationChecks) bool {
	if row.fieldType == nil || *row.fieldType != string(schema.TypeRelation) {
		return true
	}
	if row.fieldID == nil {
		return false
	}
	relation, ok := relations[*row.fieldID]
	return ok && relation.binding.cardinality == schema.RelationSingle
}

func appendRelationFinding(report *Report, code, message string) {
	for _, existing := range report.Findings {
		if existing.Code == code {
			return
		}
	}
	report.Findings = append(report.Findings, finding(code, message))
}

type physicalColumn struct {
	typeName   string
	notNull    bool
	defaultSQL *string
	attribute  int16
}

type physicalTable struct {
	oid         int64
	schema      string
	name        string
	relKind     string
	persistence string
	columns     map[string]physicalColumn
}

type physicalConstraint struct {
	tableOID             int64
	name, constraintType string
	columns              []int16
	targetTableOID       int64
	targetColumns        []int16
	deleteAction         string
	updateAction         string
	matchType            string
	deferrable, deferred bool
	validated            bool
	checkExpression      *string
}

type physicalIndex struct {
	tableOID                    int64
	name, method                string
	columns, options            []int16
	unique, primary             bool
	valid, ready, live          bool
	keyCount, attributeCount    int
	hasPredicate, hasExpression bool
}

type physicalFunction struct {
	oid                        int64
	name, language, volatility string
	argumentCount              int
	returnsTrigger             bool
	securityDefiner, leakproof bool
	kind, source               string
}

type physicalTrigger struct {
	tableOID                          int64
	name                              string
	functionOID                       int64
	typeMask                          int
	enabled                           string
	constraint, deferrable, deferred  bool
	argumentCount                     int
	hasWhen, hasOldTable, hasNewTable bool
}

type relationPhysicalCatalog struct {
	tablesByName map[string]*physicalTable
	tablesByOID  map[int64]*physicalTable
	constraints  map[string]physicalConstraint
	indexes      map[string]physicalIndex
	functions    map[string]physicalFunction
	triggers     map[string]physicalTrigger
}

func relationObjectKey(namespace, name string) string { return namespace + "\x00" + name }

func ownedObjectKey(owner int64, name string) string {
	return strconv.FormatInt(owner, 10) + "\x00" + name
}

func functionKey(name string, argumentCount int) string {
	return name + "\x00" + strconv.Itoa(argumentCount)
}

func loadRelationPhysicalCatalog(ctx context.Context, db database.DBTX) (relationPhysicalCatalog, error) {
	catalog := relationPhysicalCatalog{
		tablesByName: make(map[string]*physicalTable),
		tablesByOID:  make(map[int64]*physicalTable),
		constraints:  make(map[string]physicalConstraint),
		indexes:      make(map[string]physicalIndex),
		functions:    make(map[string]physicalFunction),
		triggers:     make(map[string]physicalTrigger),
	}
	if err := loadRelationTables(ctx, db, &catalog); err != nil {
		return relationPhysicalCatalog{}, err
	}
	if err := loadRelationConstraints(ctx, db, &catalog); err != nil {
		return relationPhysicalCatalog{}, err
	}
	if err := loadRelationIndexes(ctx, db, &catalog); err != nil {
		return relationPhysicalCatalog{}, err
	}
	if err := loadRelationFunctions(ctx, db, &catalog); err != nil {
		return relationPhysicalCatalog{}, err
	}
	if err := loadRelationTriggers(ctx, db, &catalog); err != nil {
		return relationPhysicalCatalog{}, err
	}
	return catalog, nil
}

func loadRelationTables(ctx context.Context, db database.DBTX, catalog *relationPhysicalCatalog) error {
	rows, err := db.Query(ctx, `select tables.oid::bigint, schemas.nspname, tables.relname,
		tables.relkind::text, tables.relpersistence::text,
		columns.attnum::integer, columns.attname,
		format_type(columns.atttypid, columns.atttypmod), columns.attnotnull,
		pg_get_expr(defaults.adbin, defaults.adrelid)
		from pg_class as tables
		join pg_namespace as schemas on schemas.oid = tables.relnamespace
		left join pg_attribute as columns
			on columns.attrelid = tables.oid
			and columns.attnum > 0
			and not columns.attisdropped
		left join pg_attrdef as defaults
			on defaults.adrelid = tables.oid and defaults.adnum = columns.attnum
		where schemas.nspname in ('app', 'pgbase_relation')
			and tables.relkind in ('r', 'p')
		order by tables.oid, columns.attnum nulls first`)
	if err != nil {
		return fmt.Errorf("read relation physical tables: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var oid int64
		var namespace, name, relKind, persistence string
		var attribute *int
		var columnName, typeName *string
		var notNull *bool
		var defaultSQL *string
		if err := rows.Scan(&oid, &namespace, &name, &relKind, &persistence, &attribute, &columnName, &typeName, &notNull, &defaultSQL); err != nil {
			return fmt.Errorf("scan relation physical tables: %w", err)
		}
		table := catalog.tablesByOID[oid]
		if table == nil {
			table = &physicalTable{
				oid: oid, schema: namespace, name: name, relKind: relKind,
				persistence: persistence, columns: make(map[string]physicalColumn),
			}
			catalog.tablesByOID[oid] = table
			catalog.tablesByName[relationObjectKey(namespace, name)] = table
		}
		if attribute != nil && columnName != nil && typeName != nil && notNull != nil {
			table.columns[*columnName] = physicalColumn{
				typeName: *typeName, notNull: *notNull,
				defaultSQL: defaultSQL, attribute: int16(*attribute),
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate relation physical tables: %w", err)
	}
	return nil
}

func loadRelationConstraints(ctx context.Context, db database.DBTX, catalog *relationPhysicalCatalog) error {
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
		where schemas.nspname in ('app', 'pgbase_relation')
		order by constraints.conrelid, constraints.conname`)
	if err != nil {
		return fmt.Errorf("read relation physical constraints: %w", err)
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
			return fmt.Errorf("scan relation physical constraints: %w", err)
		}
		catalog.constraints[ownedObjectKey(constraint.tableOID, constraint.name)] = constraint
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate relation physical constraints: %w", err)
	}
	return nil
}

func loadRelationIndexes(ctx context.Context, db database.DBTX, catalog *relationPhysicalCatalog) error {
	rows, err := db.Query(ctx, `select indexes.indrelid::bigint, index_tables.relname,
		access_methods.amname, indexes.indkey::smallint[], indexes.indoption::smallint[],
		indexes.indisunique, indexes.indisprimary,
		indexes.indisvalid, indexes.indisready, indexes.indislive,
		indexes.indnkeyatts::integer, indexes.indnatts::integer,
		indexes.indpred is not null, indexes.indexprs is not null
		from pg_index as indexes
		join pg_class as tables on tables.oid = indexes.indrelid
		join pg_namespace as schemas on schemas.oid = tables.relnamespace
		join pg_class as index_tables on index_tables.oid = indexes.indexrelid
		join pg_am as access_methods on access_methods.oid = index_tables.relam
		where schemas.nspname in ('app', 'pgbase_relation')
		order by indexes.indrelid, index_tables.relname`)
	if err != nil {
		return fmt.Errorf("read relation physical indexes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var index physicalIndex
		if err := rows.Scan(
			&index.tableOID, &index.name, &index.method, &index.columns, &index.options,
			&index.unique, &index.primary, &index.valid, &index.ready, &index.live,
			&index.keyCount, &index.attributeCount, &index.hasPredicate, &index.hasExpression,
		); err != nil {
			return fmt.Errorf("scan relation physical indexes: %w", err)
		}
		catalog.indexes[ownedObjectKey(index.tableOID, index.name)] = index
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate relation physical indexes: %w", err)
	}
	return nil
}

func loadRelationFunctions(ctx context.Context, db database.DBTX, catalog *relationPhysicalCatalog) error {
	rows, err := db.Query(ctx, `select functions.oid::bigint, functions.proname,
		functions.pronargs::integer, functions.prorettype = 'trigger'::regtype,
		languages.lanname, functions.prosecdef, functions.proleakproof,
		functions.provolatile::text, functions.prokind::text, functions.prosrc
		from pg_proc as functions
		join pg_namespace as schemas on schemas.oid = functions.pronamespace
		join pg_language as languages on languages.oid = functions.prolang
		where schemas.nspname = 'pgbase_relation'
		order by functions.proname, functions.pronargs`)
	if err != nil {
		return fmt.Errorf("read relation physical functions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var function physicalFunction
		if err := rows.Scan(
			&function.oid, &function.name, &function.argumentCount, &function.returnsTrigger,
			&function.language, &function.securityDefiner, &function.leakproof,
			&function.volatility, &function.kind, &function.source,
		); err != nil {
			return fmt.Errorf("scan relation physical functions: %w", err)
		}
		catalog.functions[functionKey(function.name, function.argumentCount)] = function
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate relation physical functions: %w", err)
	}
	return nil
}

func loadRelationTriggers(ctx context.Context, db database.DBTX, catalog *relationPhysicalCatalog) error {
	rows, err := db.Query(ctx, `select triggers.tgrelid::bigint, triggers.tgname,
		triggers.tgfoid::bigint, triggers.tgtype::integer, triggers.tgenabled::text,
		triggers.tgconstraint <> 0, triggers.tgdeferrable, triggers.tginitdeferred,
		triggers.tgnargs::integer, triggers.tgqual is not null,
		triggers.tgoldtable is not null, triggers.tgnewtable is not null
		from pg_trigger as triggers
		join pg_class as tables on tables.oid = triggers.tgrelid
		join pg_namespace as schemas on schemas.oid = tables.relnamespace
		where schemas.nspname in ('app', 'pgbase_relation')
			and not triggers.tgisinternal
		order by triggers.tgrelid, triggers.tgname`)
	if err != nil {
		return fmt.Errorf("read relation physical triggers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var trigger physicalTrigger
		if err := rows.Scan(
			&trigger.tableOID, &trigger.name, &trigger.functionOID,
			&trigger.typeMask, &trigger.enabled, &trigger.constraint,
			&trigger.deferrable, &trigger.deferred, &trigger.argumentCount,
			&trigger.hasWhen, &trigger.hasOldTable, &trigger.hasNewTable,
		); err != nil {
			return fmt.Errorf("scan relation physical triggers: %w", err)
		}
		catalog.triggers[ownedObjectKey(trigger.tableOID, trigger.name)] = trigger
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate relation physical triggers: %w", err)
	}
	return nil
}

func checkRelationPhysicalSchema(ctx context.Context, db database.DBTX, relations relationChecks, report *Report) error {
	physical, err := loadRelationPhysicalCatalog(ctx, db)
	if err != nil {
		return err
	}
	expectedTables := make(map[string]struct{})
	expectedFunctions := make(map[string]struct{})
	for _, relation := range relations {
		switch relation.binding.cardinality {
		case schema.RelationSingle:
			checkSingleRelationPhysical(physical, relation, report)
		case schema.RelationMultiple:
			expectedTables[relation.physicalName.MultiTable] = struct{}{}
			expectedFunctions[functionKey(relation.physicalName.PositionFunction, 0)] = struct{}{}
			expectedFunctions[functionKey(relation.physicalName.CardinalityFunction, 0)] = struct{}{}
			checkMultiRelationPhysical(physical, relation, report)
		}
	}

	for _, table := range physical.tablesByOID {
		if table.schema != "pgbase_relation" {
			continue
		}
		if _, ok := expectedTables[table.name]; !ok {
			appendRelationFinding(report, "relation_table_orphaned", "A physical relation table is missing from relation metadata")
		}
	}
	for key := range physical.functions {
		if _, ok := expectedFunctions[key]; !ok {
			appendRelationFinding(report, "relation_function_orphaned", "A physical relation function is missing from relation metadata")
		}
	}
	return nil
}

func checkSingleRelationPhysical(physical relationPhysicalCatalog, relation relationCheck, report *Report) {
	source := physical.tablesByName[relationObjectKey("app", relation.sourceTable)]
	if source == nil {
		return // The collection-level check already reports the missing table.
	}
	column, ok := source.columns[relation.column]
	if !ok {
		appendRelationFinding(report, "relation_single_column_missing", "A single relation column is missing")
		return
	}
	if column.typeName != "uuid" || column.notNull != relation.required || column.defaultSQL != nil {
		appendRelationFinding(report, "relation_single_column_mismatch", "A single relation column has unexpected properties")
	}

	target := physical.tablesByName[relationObjectKey("app", relation.targetTable)]
	foreignKey, ok := physical.constraints[ownedObjectKey(source.oid, relation.physicalName.SingleForeignKey)]
	if !ok || !foreignKeyMatches(foreignKey, source, []string{relation.column}, target, []string{"id"}, "a", true, true) {
		appendRelationFinding(report, "relation_single_foreign_key_mismatch", "A single relation foreign key is missing or inconsistent")
	}
	index, ok := physical.indexes[ownedObjectKey(source.oid, relation.physicalName.SingleReverseIndex)]
	if !ok || !indexMatches(index, source, []string{relation.column}, relation.unique) {
		appendRelationFinding(report, "relation_single_index_mismatch", "A single relation reverse index is missing or inconsistent")
	}
}

func checkMultiRelationPhysical(physical relationPhysicalCatalog, relation relationCheck, report *Report) {
	edge := physical.tablesByName[relationObjectKey("pgbase_relation", relation.physicalName.MultiTable)]
	if edge == nil || edge.relKind != "r" || edge.persistence != "p" {
		appendRelationFinding(report, "relation_multi_table_missing", "A multiple relation table is missing")
		return
	}
	if !columnsMatch(edge, []expectedPhysicalColumn{
		{name: "source_id", typeName: "uuid", notNull: true},
		{name: "target_id", typeName: "uuid", notNull: true},
		{name: "position", typeName: "integer", notNull: true},
	}) {
		appendRelationFinding(report, "relation_multi_columns_mismatch", "A multiple relation table has unexpected columns")
	}

	primaryKey, ok := physical.constraints[ownedObjectKey(edge.oid, relation.physicalName.MultiPrimaryKey)]
	if !ok || !keyConstraintMatches(primaryKey, edge, "p", []string{"source_id", "target_id"}) {
		appendRelationFinding(report, "relation_multi_primary_key_mismatch", "A multiple relation primary key is missing or inconsistent")
	}
	positionUnique, ok := physical.constraints[ownedObjectKey(edge.oid, relation.physicalName.MultiPositionUnique)]
	if !ok || !keyConstraintMatches(positionUnique, edge, "u", []string{"source_id", "position"}) {
		appendRelationFinding(report, "relation_multi_position_unique_mismatch", "A multiple relation position constraint is missing or inconsistent")
	}
	positionCheck, ok := physical.constraints[ownedObjectKey(edge.oid, relation.physicalName.MultiPositionCheck)]
	if !ok || !positionCheckMatches(positionCheck, edge) {
		appendRelationFinding(report, "relation_multi_position_check_mismatch", "A multiple relation position check is missing or inconsistent")
	}

	source := physical.tablesByName[relationObjectKey("app", relation.sourceTable)]
	target := physical.tablesByName[relationObjectKey("app", relation.targetTable)]
	sourceForeignKey, ok := physical.constraints[ownedObjectKey(edge.oid, relation.physicalName.MultiSourceForeignKey)]
	if !ok || !foreignKeyMatches(sourceForeignKey, edge, []string{"source_id"}, source, []string{"id"}, "c", false, false) {
		appendRelationFinding(report, "relation_multi_source_foreign_key_mismatch", "A multiple relation source foreign key is missing or inconsistent")
	}
	targetForeignKey, ok := physical.constraints[ownedObjectKey(edge.oid, relation.physicalName.MultiTargetForeignKey)]
	if !ok || !foreignKeyMatches(targetForeignKey, edge, []string{"target_id"}, target, []string{"id"}, "a", true, true) {
		appendRelationFinding(report, "relation_multi_target_foreign_key_mismatch", "A multiple relation target foreign key is missing or inconsistent")
	}
	reverseIndex, ok := physical.indexes[ownedObjectKey(edge.oid, relation.physicalName.MultiReverseIndex)]
	if !ok || !indexMatches(reverseIndex, edge, []string{"target_id", "source_id"}, false) {
		appendRelationFinding(report, "relation_multi_reverse_index_mismatch", "A multiple relation reverse index is missing or inconsistent")
	}

	positionFunction, positionFunctionOK := physical.functions[functionKey(relation.physicalName.PositionFunction, 0)]
	if !positionFunctionOK || !functionPropertiesMatch(positionFunction) || !positionFunctionBodyMatches(positionFunction.source, relation) {
		appendRelationFinding(report, "relation_multi_position_function_mismatch", "A multiple relation position function is missing or inconsistent")
	}
	cardinalityFunction, cardinalityFunctionOK := physical.functions[functionKey(relation.physicalName.CardinalityFunction, 0)]
	if !cardinalityFunctionOK || !functionPropertiesMatch(cardinalityFunction) || !cardinalityFunctionBodyMatches(cardinalityFunction.source, relation) {
		appendRelationFinding(report, "relation_multi_cardinality_function_mismatch", "A multiple relation cardinality function is missing or inconsistent")
	}

	positionTrigger, ok := physical.triggers[ownedObjectKey(edge.oid, relation.physicalName.PositionTrigger)]
	if !ok || !triggerMatches(positionTrigger, positionFunction, positionFunctionOK, triggerAfterRowInsertUpdateDelete) {
		appendRelationFinding(report, "relation_multi_position_trigger_mismatch", "A multiple relation position trigger is missing or inconsistent")
	}
	if source != nil {
		sourceTrigger, sourceTriggerOK := physical.triggers[ownedObjectKey(source.oid, relation.physicalName.SourceCardinalityTrigger)]
		if !sourceTriggerOK || !triggerMatches(sourceTrigger, cardinalityFunction, cardinalityFunctionOK, triggerAfterRowInsert) {
			appendRelationFinding(report, "relation_multi_source_cardinality_trigger_mismatch", "A multiple relation source cardinality trigger is missing or inconsistent")
		}
	} else {
		appendRelationFinding(report, "relation_multi_source_cardinality_trigger_mismatch", "A multiple relation source cardinality trigger is missing or inconsistent")
	}
	edgeTrigger, ok := physical.triggers[ownedObjectKey(edge.oid, relation.physicalName.EdgeCardinalityTrigger)]
	if !ok || !triggerMatches(edgeTrigger, cardinalityFunction, cardinalityFunctionOK, triggerAfterRowInsertUpdateDelete) {
		appendRelationFinding(report, "relation_multi_edge_cardinality_trigger_mismatch", "A multiple relation edge cardinality trigger is missing or inconsistent")
	}
}

type expectedPhysicalColumn struct {
	name, typeName string
	notNull        bool
}

func columnsMatch(table *physicalTable, expected []expectedPhysicalColumn) bool {
	if table == nil || len(table.columns) != len(expected) {
		return false
	}
	for _, want := range expected {
		got, ok := table.columns[want.name]
		if !ok || got.typeName != want.typeName || got.notNull != want.notNull || got.defaultSQL != nil {
			return false
		}
	}
	return true
}

func keyConstraintMatches(constraint physicalConstraint, table *physicalTable, kind string, columns []string) bool {
	return table != nil && constraint.tableOID == table.oid && constraint.constraintType == kind && constraint.validated &&
		!constraint.deferrable && !constraint.deferred &&
		columnNumbersMatch(table, constraint.columns, columns)
}

func positionCheckMatches(constraint physicalConstraint, table *physicalTable) bool {
	if !keyConstraintMatches(constraint, table, "c", []string{"position"}) || constraint.checkExpression == nil {
		return false
	}
	expression := compactSQL(*constraint.checkExpression)
	return expression == "position>=0"
}

func foreignKeyMatches(
	constraint physicalConstraint,
	source *physicalTable,
	sourceColumns []string,
	target *physicalTable,
	targetColumns []string,
	deleteAction string,
	deferrable, deferred bool,
) bool {
	return source != nil && target != nil && constraint.tableOID == source.oid &&
		constraint.constraintType == "f" && constraint.targetTableOID == target.oid &&
		constraint.deleteAction == deleteAction && constraint.updateAction == "a" && constraint.matchType == "s" &&
		constraint.deferrable == deferrable && constraint.deferred == deferred && constraint.validated &&
		columnNumbersMatch(source, constraint.columns, sourceColumns) &&
		columnNumbersMatch(target, constraint.targetColumns, targetColumns)
}

func indexMatches(index physicalIndex, table *physicalTable, columns []string, unique bool) bool {
	return table != nil && index.tableOID == table.oid && index.unique == unique && !index.primary &&
		index.method == "btree" && indexOptionsAreDefault(index.options, len(columns)) &&
		index.valid && index.ready && index.live && !index.hasPredicate && !index.hasExpression &&
		index.keyCount == len(columns) && index.attributeCount == len(columns) &&
		columnNumbersMatch(table, index.columns, columns)
}

func indexOptionsAreDefault(options []int16, count int) bool {
	if len(options) != count {
		return false
	}
	for _, option := range options {
		if option != 0 {
			return false
		}
	}
	return true
}

func columnNumbersMatch(table *physicalTable, numbers []int16, names []string) bool {
	if table == nil || len(numbers) != len(names) {
		return false
	}
	for index, name := range names {
		column, ok := table.columns[name]
		if !ok || column.attribute != numbers[index] {
			return false
		}
	}
	return true
}

func functionPropertiesMatch(function physicalFunction) bool {
	return function.argumentCount == 0 && function.returnsTrigger && function.language == "plpgsql" &&
		!function.securityDefiner && !function.leakproof && function.volatility == "v" && function.kind == "f"
}

func positionFunctionBodyMatches(source string, relation relationCheck) bool {
	compact := compactSQL(source)
	edge := "pgbase_relation." + relation.physicalName.MultiTable
	return containsEvery(compact,
		"iftg_op='insert'then", "affected_sources:=array[new.source_id]",
		"elsiftg_op='delete'then", "affected_sources:=array[old.source_id]",
		"affected_sources:=array[old.source_id,new.source_id]",
		"from"+edge, "count(*)", "min(position)", "max(position)",
		"minimum_position<>0", "maximum_position::bigint<>relation_count-1",
		"errcode='23514'", "constraint='"+relation.physicalName.MultiPositionCheck+"'",
	)
}

func cardinalityFunctionBodyMatches(source string, relation relationCheck) bool {
	compact := compactSQL(source)
	edge := "pgbase_relation." + relation.physicalName.MultiTable
	sourceTable := "app." + relation.sourceTable
	bounds := "ifrelation_count<" + strconv.Itoa(relation.binding.minimum) +
		"orrelation_count>" + strconv.Itoa(relation.binding.maximum) + "then"
	return strings.Count(compact, compactSQL(bounds)) == 2 && containsEvery(compact,
		"iftg_table_schema='app'andtg_table_name='"+relation.sourceTable+"'then",
		"iftg_op='insert'then", "affected_sources:=array[new.source_id]",
		"elsiftg_op='delete'then", "affected_sources:=array[old.source_id]",
		"affected_sources:=array[old.source_id,new.source_id]",
		"from"+edge, "ifexists(select1from"+sourceTable+"whereid=affected_source)",
		"where source_id=affected_source", bounds,
		"errcode='23514'", "constraint='"+relation.physicalName.CardinalityFunction+"'",
	)
}

func compactSQL(value string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsSpace(character) || character == '"' || character == '(' || character == ')' {
			return -1
		}
		return unicode.ToLower(character)
	}, value)
}

func containsEvery(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, compactSQL(fragment)) {
			return false
		}
	}
	return true
}

const (
	triggerAfterRowInsert             = 1 | 4
	triggerAfterRowInsertUpdateDelete = 1 | 4 | 8 | 16
)

func triggerMatches(trigger physicalTrigger, function physicalFunction, functionExists bool, typeMask int) bool {
	return functionExists && trigger.functionOID == function.oid && trigger.typeMask == typeMask &&
		trigger.enabled == "O" && trigger.constraint && trigger.deferrable && trigger.deferred &&
		trigger.argumentCount == 0 && !trigger.hasWhen && !trigger.hasOldTable && !trigger.hasNewTable
}
