package doctor

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"slices"
	"strings"
	"unicode"

	"github.com/yakisoba0728/PgBase/internal/schema"
)

// compactSQL folds the incidental differences between two renderings of the
// same expression: PostgreSQL adds its own parentheses and quoting when it
// deparses a stored CHECK or default, and the planner's source text does not
// carry them.
func compactSQL(value string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsSpace(character) || character == '"' || character == '(' || character == ')' {
			return -1
		}
		return unicode.ToLower(character)
	}, value)
}

// physicalRole names the findings one required object produces. Codes are the
// machine-readable half of `pgbase doctor --json`, so an object's role decides
// which established code an operator sees; the properties an object must have
// come from the schema planner's manifest and are never restated here.
//
// anyName exists because a collection's primary key, pagination index and field
// indexes have always been accepted by shape rather than by name: a database
// whose index was created by hand or by an older planner is healthy as long as
// PostgreSQL can use it the same way. Relation objects keep exact-name matching
// because the relation write path resolves them by their deterministic names.
type physicalRole struct {
	missing      string
	mismatch     string
	nullability  string
	defaultValue string
	anyName      bool
}

func (role physicalRole) nullabilityCode() string {
	if role.nullability != "" {
		return role.nullability
	}
	return role.mismatch
}

func (role physicalRole) defaultCode() string {
	if role.defaultValue != "" {
		return role.defaultValue
	}
	return role.mismatch
}

var (
	collectionTableRole  = physicalRole{missing: "collection_table_missing", mismatch: "collection_table_missing"}
	collectionColumnRole = physicalRole{
		missing:      "collection_column_missing",
		mismatch:     "collection_column_type_mismatch",
		nullability:  "collection_column_nullability_mismatch",
		defaultValue: "collection_column_default_mismatch",
	}
	collectionIndexRole = physicalRole{
		missing: "collection_index_missing", mismatch: "collection_index_missing", anyName: true,
	}
	collectionPageIndexRole = physicalRole{
		missing: "collection_page_index_missing", mismatch: "collection_page_index_missing", anyName: true,
	}
	collectionPrimaryKeyRole = physicalRole{
		missing: "collection_primary_key_missing", mismatch: "collection_primary_key_missing", anyName: true,
	}
)

func uniformRole(code string) physicalRole {
	return physicalRole{missing: code, mismatch: code}
}

// physicalRoles maps the objects a relation owns to the relation findings they
// have always produced. Every other required object falls back to its
// collection role, so a catalog without relations needs no entries at all.
func physicalRoles(relations relationChecks) physicalRoleIndex {
	roles := physicalRoleIndex{
		objects:       make(map[schema.PhysicalObjectRef]physicalRole),
		orphanColumns: make(map[schema.PhysicalObjectRef]string),
	}
	for _, relation := range relations {
		names := relation.physicalName
		switch relation.binding.cardinality {
		case schema.RelationSingle:
			roles.objects[columnRef("app", relation.sourceTable, relation.column)] = physicalRole{
				missing:  "relation_single_column_missing",
				mismatch: "relation_single_column_mismatch",
			}
			roles.objects[constraintRef("app", relation.sourceTable, names.SingleForeignKey)] =
				uniformRole("relation_single_foreign_key_mismatch")
			roles.objects[indexRef("app", relation.sourceTable, names.SingleReverseIndex)] =
				uniformRole("relation_single_index_mismatch")
		case schema.RelationMultiple:
			edge := names.MultiTable
			roles.objects[tableRef("pgbase_relation", edge)] = uniformRole("relation_multi_table_missing")
			for _, column := range []string{"source_id", "target_id", "position"} {
				roles.objects[columnRef("pgbase_relation", edge, column)] = uniformRole("relation_multi_columns_mismatch")
			}
			roles.orphanColumns[tableRef("pgbase_relation", edge)] = "relation_multi_columns_mismatch"
			assignRelationObject(roles, edge, names.MultiPrimaryKey, "relation_multi_primary_key_mismatch")
			assignRelationObject(roles, edge, names.MultiPositionUnique, "relation_multi_position_unique_mismatch")
			assignRelationObject(roles, edge, names.MultiPositionCheck, "relation_multi_position_check_mismatch")
			assignRelationObject(roles, edge, names.MultiSourceForeignKey, "relation_multi_source_foreign_key_mismatch")
			assignRelationObject(roles, edge, names.MultiTargetForeignKey, "relation_multi_target_foreign_key_mismatch")
			assignRelationObject(roles, edge, names.MultiReverseIndex, "relation_multi_reverse_index_mismatch")
			assignRelationObject(roles, edge, names.PositionTrigger, "relation_multi_position_trigger_mismatch")
			assignRelationObject(roles, edge, names.EdgeCardinalityTrigger, "relation_multi_edge_cardinality_trigger_mismatch")
			roles.objects[functionRef("pgbase_relation", names.PositionFunction)] =
				uniformRole("relation_multi_position_function_mismatch")
			roles.objects[functionRef("pgbase_relation", names.CardinalityFunction)] =
				uniformRole("relation_multi_cardinality_function_mismatch")
			for _, ref := range []schema.PhysicalObjectRef{
				constraintRef("app", relation.sourceTable, names.SourceCardinalityTrigger),
				triggerRef("app", relation.sourceTable, names.SourceCardinalityTrigger),
			} {
				roles.objects[ref] = uniformRole("relation_multi_source_cardinality_trigger_mismatch")
			}
		}
	}
	return roles
}

// assignRelationObject registers one deterministic name under every child kind
// it can appear as. A constraint trigger owns both a pg_trigger and a
// pg_constraint row under the same name, and a key constraint owns both a
// pg_constraint row and the index backing it.
func assignRelationObject(roles physicalRoleIndex, table, name, code string) {
	for _, ref := range []schema.PhysicalObjectRef{
		constraintRef("pgbase_relation", table, name),
		indexRef("pgbase_relation", table, name),
		triggerRef("pgbase_relation", table, name),
	} {
		roles.objects[ref] = uniformRole(code)
	}
}

// physicalRoleIndex separates the finding a required object produces from the
// finding an unexpected extra column on that object's table produces. A
// relation edge table reports both through its own relation code; a collection
// table reports the extra column as a catalog orphan.
type physicalRoleIndex struct {
	objects       map[schema.PhysicalObjectRef]physicalRole
	orphanColumns map[schema.PhysicalObjectRef]string
}

type physicalComparison struct {
	manifest schema.ExpectedPhysicalSchema
	snapshot physicalSnapshot
	roles    physicalRoleIndex
	report   *Report
	matched  map[schema.PhysicalObjectRef]struct{}
}

// comparePhysicalSchema reconciles the planner's compiled contract with one
// bulk observation. Each finding kind is emitted at most once: the codes carry
// no object names, so repeating one tells an operator nothing and only inflates
// the diagnostic payload of a badly drifted database.
func comparePhysicalSchema(
	manifest schema.ExpectedPhysicalSchema,
	snapshot physicalSnapshot,
	relations relationChecks,
	report *Report,
) {
	comparison := physicalComparison{
		manifest: manifest,
		snapshot: snapshot,
		roles:    physicalRoles(relations),
		report:   report,
		matched:  make(map[schema.PhysicalObjectRef]struct{}),
	}
	required := manifest.Required()
	comparison.compareTables(required.Tables)
	comparison.compareColumns(required.Columns)
	comparison.compareIndexes(required.Indexes)
	comparison.compareConstraints(required.Constraints)
	comparison.compareTriggers(required.Triggers)
	comparison.compareFunctions(required.Functions)
	comparison.reportOrphans()
}

func (c *physicalComparison) role(ref schema.PhysicalObjectRef, fallback physicalRole) physicalRole {
	if role, ok := c.roles.objects[ref]; ok {
		return role
	}
	return fallback
}

func (c *physicalComparison) append(code string) {
	appendUniqueFinding(c.report, code, physicalFindingMessages[code])
}

func (c *physicalComparison) compareTables(required []schema.TableSpec) {
	for _, want := range required {
		ref := tableRef(want.Namespace, want.Name)
		role := c.role(ref, collectionTableRole)
		got, ok := c.snapshot.tables[ref]
		if !ok {
			c.append(role.missing)
			continue
		}
		c.matched[ref] = struct{}{}
		if got.Kind != want.Kind || got.Persistence != want.Persistence {
			c.append(role.mismatch)
		}
	}
}

func (c *physicalComparison) compareColumns(required []schema.ColumnSpec) {
	for _, want := range required {
		ref := columnRef(want.Namespace, want.Table, want.Name)
		role := c.role(ref, collectionColumnRole)
		got, ok := c.snapshot.columns[ref]
		if !ok {
			// A table that is already reported missing would otherwise emit one
			// column finding per field, which says nothing the table finding
			// does not already say.
			if _, present := c.snapshot.tables[tableRef(want.Namespace, want.Table)]; present {
				c.append(role.missing)
			}
			continue
		}
		c.matched[ref] = struct{}{}
		if got.DataType != want.DataType {
			c.append(role.mismatch)
		}
		if got.NotNull != want.NotNull {
			c.append(role.nullabilityCode())
		}
		if !physicalDefaultsEquivalent(want.DataType, want.DefaultSQL, got.DefaultSQL) {
			c.append(role.defaultCode())
		}
	}
}

func (c *physicalComparison) compareIndexes(required []schema.IndexSpec) {
	for _, want := range required {
		ref := indexRef(want.Namespace, want.Table, want.Name)
		role := c.role(ref, defaultIndexRole(want))
		if got, ok := c.snapshot.indexes[ref]; ok {
			c.matched[ref] = struct{}{}
			if !indexSpecEqual(want, got) || got.ConstraintName != want.ConstraintName {
				c.append(role.mismatch)
			}
			continue
		}
		if !c.matchIndexByAlternateName(want) && !(role.anyName && c.matchIndexByShape(want)) {
			c.append(role.missing)
		}
	}
}

func (c *physicalComparison) matchIndexByAlternateName(want schema.IndexSpec) bool {
	for _, alternate := range want.CompatibleNames {
		ref := indexRef(want.Namespace, want.Table, alternate)
		got, ok := c.snapshot.indexes[ref]
		if !ok || !indexSpecEqual(want, got) {
			continue
		}
		c.matched[ref] = struct{}{}
		return true
	}
	return false
}

func (c *physicalComparison) matchIndexByShape(want schema.IndexSpec) bool {
	for ref, got := range c.snapshot.indexes {
		if ref.Namespace != want.Namespace || ref.Table != want.Table {
			continue
		}
		if _, taken := c.matched[ref]; taken || !indexSpecEqual(want, got) {
			continue
		}
		c.matched[ref] = struct{}{}
		return true
	}
	return false
}

// defaultIndexRole distinguishes the pagination index every collection carries
// from an ordinary field index by the keys the manifest requires, so the
// distinction lives in one place instead of in a name convention.
func defaultIndexRole(want schema.IndexSpec) physicalRole {
	if want.Primary {
		return collectionPrimaryKeyRole
	}
	if len(want.Keys) == 2 && want.Keys[0].Column == "created_at" && want.Keys[1].Column == "id" {
		return collectionPageIndexRole
	}
	return collectionIndexRole
}

func (c *physicalComparison) compareConstraints(required []schema.ConstraintSpec) {
	for _, want := range required {
		ref := constraintRef(want.Namespace, want.Table, want.Name)
		role := c.role(ref, defaultConstraintRole(want))
		if got, ok := c.snapshot.constraints[ref]; ok {
			c.matched[ref] = struct{}{}
			if !constraintSpecEqual(want, got) {
				c.append(role.mismatch)
			}
			continue
		}
		if role.anyName && c.matchConstraintByShape(want) {
			continue
		}
		c.append(role.missing)
	}
}

func defaultConstraintRole(want schema.ConstraintSpec) physicalRole {
	if want.Kind == "p" {
		return collectionPrimaryKeyRole
	}
	return collectionTableRole
}

func (c *physicalComparison) matchConstraintByShape(want schema.ConstraintSpec) bool {
	for observedRef, got := range c.snapshot.constraints {
		if observedRef.Namespace != want.Namespace || observedRef.Table != want.Table {
			continue
		}
		if _, taken := c.matched[observedRef]; taken || !constraintSpecEqual(want, got) {
			continue
		}
		c.matched[observedRef] = struct{}{}
		return true
	}
	return false
}

func (c *physicalComparison) compareTriggers(required []schema.TriggerSpec) {
	for _, want := range required {
		ref := triggerRef(want.Namespace, want.Table, want.Name)
		role := c.role(ref, collectionTableRole)
		got, ok := c.snapshot.triggers[ref]
		if !ok {
			c.append(role.missing)
			continue
		}
		c.matched[ref] = struct{}{}
		if !triggerSpecEqual(want, got) {
			c.append(role.mismatch)
		}
	}
}

func (c *physicalComparison) compareFunctions(required []schema.FunctionSpec) {
	for _, want := range required {
		ref := functionRef(want.Namespace, want.Name)
		role := c.role(ref, collectionTableRole)
		got, ok := c.snapshot.functions[ref]
		if !ok {
			c.append(role.missing)
			continue
		}
		c.matched[ref] = struct{}{}
		if functionDefinitionDigest(want) != functionDefinitionDigest(got) {
			c.append(role.mismatch)
		}
	}
}

// Tables and their columns are judged by namespace rather than by
// manifest ownership: both managed namespaces exist for PgBase alone, so an
// uncatalogued table there is drift even though the manifest's Owns rule
// deliberately tolerates an unrelated user index on a collection table.
func (c *physicalComparison) reportOrphans() {
	for ref, table := range c.snapshot.tables {
		if _, matched := c.matched[ref]; matched {
			continue
		}
		if table.Namespace == relationNamespace {
			c.append("relation_table_orphaned")
			continue
		}
		c.append("collection_table_orphaned")
	}
	for ref, column := range c.snapshot.columns {
		if _, matched := c.matched[ref]; matched {
			continue
		}
		table := tableRef(column.Namespace, column.Table)
		if _, required := c.matched[table]; !required {
			continue
		}
		if code, ok := c.roles.orphanColumns[table]; ok {
			c.append(code)
			continue
		}
		c.append("collection_column_orphaned")
	}
	for ref := range c.snapshot.functions {
		if _, matched := c.matched[ref]; matched || !c.manifest.Owns(ref) {
			continue
		}
		c.append("relation_function_orphaned")
	}
}

const relationNamespace = "pgbase_relation"

func indexSpecEqual(want, got schema.IndexSpec) bool {
	return want.Method == got.Method && slices.Equal(want.Keys, got.Keys) &&
		slices.Equal(want.IncludedColumns, got.IncludedColumns) &&
		want.Unique == got.Unique && want.NullsNotDistinct == got.NullsNotDistinct &&
		want.Primary == got.Primary && want.PredicateSQL == got.PredicateSQL &&
		want.ExpressionSQL == got.ExpressionSQL &&
		want.Valid == got.Valid && want.Ready == got.Ready && want.Live == got.Live
}

func constraintSpecEqual(want, got schema.ConstraintSpec) bool {
	return want.Kind == got.Kind && slices.Equal(want.Columns, got.Columns) &&
		want.ReferenceNamespace == got.ReferenceNamespace && want.ReferenceTable == got.ReferenceTable &&
		slices.Equal(want.ReferenceColumns, got.ReferenceColumns) &&
		want.Match == got.Match && want.OnUpdate == got.OnUpdate && want.OnDelete == got.OnDelete &&
		compactSQL(want.CheckSQL) == compactSQL(got.CheckSQL) &&
		want.Deferrable == got.Deferrable && want.InitiallyDeferred == got.InitiallyDeferred &&
		want.Validated == got.Validated && want.Enforced == got.Enforced && want.NoInherit == got.NoInherit
}

func triggerSpecEqual(want, got schema.TriggerSpec) bool {
	return want.FunctionNamespace == got.FunctionNamespace && want.FunctionName == got.FunctionName &&
		slices.Equal(want.FunctionArgumentTypes, got.FunctionArgumentTypes) &&
		want.TypeMask == got.TypeMask && slices.Equal(want.UpdateColumns, got.UpdateColumns) &&
		want.Enabled == got.Enabled && want.Constraint == got.Constraint &&
		want.Deferrable == got.Deferrable && want.InitiallyDeferred == got.InitiallyDeferred &&
		len(want.Arguments) == len(got.Arguments) && want.WhenSQL == got.WhenSQL &&
		want.OldTransitionTable == got.OldTransitionTable && want.NewTransitionTable == got.NewTransitionTable
}

// functionDefinitionDigest hashes the whole canonical definition, body included
// verbatim. Hashing the exact stored source is what makes a spoofed body fail:
// a replacement that keeps every fragment an older substring matcher looked for
// and hides the rest in comments still produces different bytes here. The
// encoding is length-delimited so no field boundary can be forged by choosing a
// value that contains the separator.
func functionDefinitionDigest(spec schema.FunctionSpec) string {
	digest := sha256.New()
	write := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		digest.Write(length[:])
		digest.Write([]byte(value))
	}
	write(spec.Namespace)
	write(spec.Name)
	write(booleanField(spec.ReturnsTrigger))
	write(spec.Language)
	write(spec.Volatility)
	write(spec.Kind)
	write(booleanField(spec.SecurityDefiner))
	write(booleanField(spec.Leakproof))
	write(booleanField(spec.Strict))
	write(spec.Parallel)
	write(spec.BodySQL)
	for _, argument := range spec.ArgumentTypes {
		write(argument)
	}
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(spec.ArgumentTypes)))
	digest.Write(count[:])
	return hex.EncodeToString(digest.Sum(nil))
}

func booleanField(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

// physicalFindingMessages pins the operator-facing text of every physical
// finding. A code with no entry here is a programming error made visible: the
// report would carry an empty message instead of silently inventing one.
var physicalFindingMessages = map[string]string{
	"collection_table_missing":                           "A catalog collection table is missing",
	"collection_table_orphaned":                          "A physical collection table is missing from the catalog",
	"collection_column_missing":                          "A catalog collection column is missing",
	"collection_column_orphaned":                         "A physical collection column is missing from the catalog",
	"collection_column_type_mismatch":                    "A catalog collection column has an unexpected type",
	"collection_column_nullability_mismatch":             "Catalog and physical column nullability do not match",
	"collection_column_default_mismatch":                 "A catalog field default does not match the physical column",
	"collection_primary_key_missing":                     "A catalog collection primary key is missing",
	"collection_page_index_missing":                      "A collection list pagination index on (created_at, id) is missing",
	"collection_index_missing":                           "A catalog field index is missing",
	"relation_single_column_missing":                     "A single relation column is missing",
	"relation_single_column_mismatch":                    "A single relation column has unexpected properties",
	"relation_single_foreign_key_mismatch":               "A single relation foreign key is missing or inconsistent",
	"relation_single_index_mismatch":                     "A single relation reverse index is missing or inconsistent",
	"relation_multi_table_missing":                       "A multiple relation table is missing",
	"relation_multi_columns_mismatch":                    "A multiple relation table has unexpected columns",
	"relation_multi_primary_key_mismatch":                "A multiple relation primary key is missing or inconsistent",
	"relation_multi_position_unique_mismatch":            "A multiple relation position constraint is missing or inconsistent",
	"relation_multi_position_check_mismatch":             "A multiple relation position check is missing or inconsistent",
	"relation_multi_source_foreign_key_mismatch":         "A multiple relation source foreign key is missing or inconsistent",
	"relation_multi_target_foreign_key_mismatch":         "A multiple relation target foreign key is missing or inconsistent",
	"relation_multi_reverse_index_mismatch":              "A multiple relation reverse index is missing or inconsistent",
	"relation_multi_position_function_mismatch":          "A multiple relation position function is missing or inconsistent",
	"relation_multi_cardinality_function_mismatch":       "A multiple relation cardinality function is missing or inconsistent",
	"relation_multi_position_trigger_mismatch":           "A multiple relation position trigger is missing or inconsistent",
	"relation_multi_source_cardinality_trigger_mismatch": "A multiple relation source cardinality trigger is missing or inconsistent",
	"relation_multi_edge_cardinality_trigger_mismatch":   "A multiple relation edge cardinality trigger is missing or inconsistent",
	"relation_table_orphaned":                            "A physical relation table is missing from relation metadata",
	"relation_function_orphaned":                         "A physical relation function is missing from relation metadata",
}
