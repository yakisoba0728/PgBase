package schema

import (
	"crypto/md5"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/google/uuid"
)

const (
	applicationPhysicalNamespace = "app"
	relationPhysicalNamespace    = "pgbase_relation"
	physicalTableOrdinary        = "r"
	physicalTablePermanent       = "p"
	physicalConstraintPrimaryKey = "p"
	physicalConstraintUnique     = "u"
	physicalConstraintCheck      = "c"
	physicalConstraintForeignKey = "f"
	physicalConstraintTrigger    = "t"
	physicalReferentialNoAction  = "a"
	physicalReferentialCascade   = "c"
	physicalReferentialUnused    = " "
	physicalMatchSimple          = "s"
	physicalFunctionVolatile     = "v"
	physicalFunctionOrdinary     = "f"
	physicalFunctionUnsafe       = "u"
	physicalTriggerEnabledOrigin = "O"
	physicalTriggerAfterRow      = 1
	physicalTriggerInsert        = 4
	physicalTriggerDelete        = 8
	physicalTriggerUpdate        = 16
	legacyPageIndexHashLength    = 16
)

// PhysicalObjectKind identifies one PostgreSQL catalog object represented by
// ExpectedPhysicalSchema. Child objects use PhysicalObjectRef.Table to name
// their owning relation.
type PhysicalObjectKind string

const (
	PhysicalObjectNamespace  PhysicalObjectKind = "namespace"
	PhysicalObjectTable      PhysicalObjectKind = "table"
	PhysicalObjectColumn     PhysicalObjectKind = "column"
	PhysicalObjectIndex      PhysicalObjectKind = "index"
	PhysicalObjectConstraint PhysicalObjectKind = "constraint"
	PhysicalObjectTrigger    PhysicalObjectKind = "trigger"
	PhysicalObjectFunction   PhysicalObjectKind = "function"
)

// PhysicalObjectRef is the stable identity used for ownership reconciliation.
// Namespace objects set only Namespace; tables and functions leave Table empty;
// columns, indexes, constraints, and triggers set all four members.
type PhysicalObjectRef struct {
	Kind      PhysicalObjectKind
	Namespace string
	Table     string
	Name      string
}

type TableSpec struct {
	Namespace   string
	Name        string
	Kind        string // pg_class.relkind
	Persistence string // pg_class.relpersistence
}

// ColumnSpec uses PostgreSQL's canonical type name. DefaultSQL is empty when
// the column has no default and otherwise contains the deterministic expression
// rendered by the schema planner.
type ColumnSpec struct {
	Namespace  string
	Table      string
	Name       string
	DataType   string
	NotNull    bool
	DefaultSQL string
}

// IndexPurpose separates indexes the catalog requires the moment a collection
// exists from companion indexes whose requirement is gated on a rollout
// capability. Doctor must not demand a companion before that gate is ready.
type IndexPurpose uint8

const (
	// IndexPurposeCore is an index the planner creates with its collection and
	// which is therefore required immediately.
	IndexPurposeCore IndexPurpose = iota
	// IndexPurposeNullableDesc backs a descending page over a nullable column.
	// The public API orders those NULLS LAST, which PostgreSQL's default btree
	// cannot serve in either scan direction, so the ordering needs its own index.
	IndexPurposeNullableDesc
)

// nullableDescIndexSuffix keys the companion index name. Field names are
// validated identifiers and cannot contain ":", so this cannot collide with the
// core index of a differently named field.
const nullableDescIndexSuffix = ":desc_nulls_last"

// IndexKeySpec describes one btree key. False direction flags are PostgreSQL's
// ASC NULLS LAST defaults. An empty OperatorClass means the type's default.
type IndexKeySpec struct {
	Column        string
	Descending    bool
	NullsFirst    bool
	OperatorClass string
}

// IndexSpec covers explicit planner indexes and indexes PostgreSQL creates to
// back primary-key or unique constraints. CompatibleNames are alternate names
// satisfying this one required object, not additional required indexes.
// ConstraintName is empty for an explicit index.
type IndexSpec struct {
	Namespace        string
	Table            string
	Name             string
	CompatibleNames  []string
	Method           string
	Keys             []IndexKeySpec
	IncludedColumns  []string
	Unique           bool
	NullsNotDistinct bool
	Primary          bool
	ConstraintName   string
	PredicateSQL     string
	ExpressionSQL    string
	Valid            bool
	Ready            bool
	Live             bool
	Purpose          IndexPurpose
}

// ConstraintSpec describes a standalone table constraint. PostgreSQL 18 also
// projects NOT NULL as contype "n" rows; snapshots must fold those rows into
// ColumnSpec.NotNull instead of reconciling them as independent constraints.
// Enforced is always true for current planner DDL; pre-18 snapshots normalize
// the catalog's absence of conenforced to true.
type ConstraintSpec struct {
	Namespace          string
	Table              string
	Name               string
	Kind               string // pg_constraint.contype
	Columns            []string
	ReferenceNamespace string
	ReferenceTable     string
	ReferenceColumns   []string
	Match              string // pg_constraint.confmatchtype
	OnUpdate           string // pg_constraint.confupdtype
	OnDelete           string // pg_constraint.confdeltype
	CheckSQL           string
	Deferrable         bool
	InitiallyDeferred  bool
	Validated          bool
	Enforced           bool
	NoInherit          bool
}

// FunctionSpec carries semantic catalog properties and the exact planner body
// PostgreSQL exposes through pg_proc.prosrc. It is not a canonical full CREATE
// definition; a future definition hash must normalize the installed function.
type FunctionSpec struct {
	Namespace       string
	Name            string
	ArgumentTypes   []string
	ReturnsTrigger  bool
	Language        string
	Volatility      string // pg_proc.provolatile
	Kind            string // pg_proc.prokind
	SecurityDefiner bool
	Leakproof       bool
	Strict          bool
	Parallel        string // pg_proc.proparallel
	BodySQL         string
}

// TriggerSpec mirrors the stable pg_trigger properties of a planner-created
// constraint trigger. TypeMask is pg_trigger.tgtype.
type TriggerSpec struct {
	Namespace             string
	Table                 string
	Name                  string
	FunctionNamespace     string
	FunctionName          string
	FunctionArgumentTypes []string
	TypeMask              int
	UpdateColumns         []string
	Enabled               string
	Constraint            bool
	Deferrable            bool
	InitiallyDeferred     bool
	Arguments             []string
	WhenSQL               string
	OldTransitionTable    string
	NewTransitionTable    string
}

type RequiredPhysicalObjects struct {
	Namespaces  []string
	Tables      []TableSpec
	Columns     []ColumnSpec
	Indexes     []IndexSpec
	Constraints []ConstraintSpec
	Triggers    []TriggerSpec
	Functions   []FunctionSpec
}

type physicalOwnership struct {
	exact          map[physicalObjectKey]struct{}
	requiredTables map[physicalTableKey]struct{}
	namespaceKinds map[string]map[PhysicalObjectKind]struct{}
	prefixes       []physicalOwnedPrefix
}

type physicalTableKey struct {
	namespace string
	name      string
}

type physicalOwnedPrefix struct {
	namespace string
	kind      PhysicalObjectKind
	prefix    string
}

type physicalObjectKey struct {
	kind      PhysicalObjectKind
	namespace string
	table     string
	name      string
}

// ExpectedPhysicalSchema is the immutable compiled physical contract for one
// validated Catalog. Its accessors return caller-owned slices, while Owns
// centralizes orphan classification so schema planning and doctor cannot drift.
type ExpectedPhysicalSchema struct {
	required  RequiredPhysicalObjects
	companion []IndexSpec
	ownership physicalOwnership
}

// Required returns a deep copy of the required object specifications. It holds
// only what a collection needs the instant it exists, so a binary that predates
// the companion rollout compiles the same required set as one that follows it.
func (manifest ExpectedPhysicalSchema) Required() RequiredPhysicalObjects {
	return cloneRequiredPhysicalObjects(manifest.required)
}

// CompanionIndexes returns a deep copy of the purpose-tagged companion indexes
// derived from this catalog. They are always owned, but a caller may treat them
// as a required physical contract only once the rollout capability is ready.
func (manifest ExpectedPhysicalSchema) CompanionIndexes() []IndexSpec {
	return cloneIndexSpecs(manifest.companion)
}

// ManagedNamespaces returns the namespaces a physical snapshot must inspect.
// Object ownership within a namespace remains kind- and prefix-aware; callers
// must use Owns instead of treating this list as blanket ownership.
func (manifest ExpectedPhysicalSchema) ManagedNamespaces() []string {
	return append([]string(nil), manifest.required.Namespaces...)
}

// Owns reports whether an observed object belongs to PgBase. Every required
// object is owned. The pgbase_relation namespace is exclusive; in app, PgBase
// owns only exact required tables/columns plus its deterministic name prefixes.
// Consequently, an unrelated user-created index on a collection table is not
// an orphan, while an unused idx_ or relation-prefix object is.
func (manifest ExpectedPhysicalSchema) Owns(ref PhysicalObjectRef) bool {
	if !validPhysicalObjectRef(ref) {
		return false
	}
	key := physicalObjectKey{kind: ref.Kind, namespace: ref.Namespace, table: ref.Table, name: ref.Name}
	if _, ok := manifest.ownership.exact[key]; ok {
		return true
	}
	if kinds := manifest.ownership.namespaceKinds[ref.Namespace]; kinds != nil {
		if _, ok := kinds[ref.Kind]; ok {
			return true
		}
	}
	for _, rule := range manifest.ownership.prefixes {
		if ref.Namespace != rule.namespace || ref.Kind != rule.kind || !strings.HasPrefix(ref.Name, rule.prefix) {
			continue
		}
		if ref.Namespace == applicationPhysicalNamespace {
			if _, ok := manifest.ownership.requiredTables[physicalTableKey{namespace: ref.Namespace, name: ref.Table}]; !ok {
				continue
			}
		}
		return true
	}
	return false
}

// ExpectedPhysicalSchemaForCatalog validates one catalog snapshot and compiles
// its physical contract. Doctor can use this seam inside the same database
// snapshot that produced its Catalog; compileCatalog uses the private
// post-validation compiler below so cached versions are not validated twice.
func ExpectedPhysicalSchemaForCatalog(catalog Catalog) (ExpectedPhysicalSchema, error) {
	if err := validateCatalogIntegrity(catalog); err != nil {
		return ExpectedPhysicalSchema{}, err
	}
	return compileExpectedPhysicalSchema(catalog)
}

// compileExpectedPhysicalSchema compiles a manifest from an integrity-checked
// Catalog. It deliberately does not validate again: compileCatalog calls this
// after the catalog's single validation pass for the version.
func compileExpectedPhysicalSchema(catalog Catalog) (ExpectedPhysicalSchema, error) {
	builder := physicalManifestBuilder{
		required: RequiredPhysicalObjects{
			Namespaces: []string{applicationPhysicalNamespace, relationPhysicalNamespace},
		},
	}

	collections := make([]Collection, 0, len(catalog.Collections))
	tableByCollectionID := make(map[uuid.UUID]string, len(catalog.Collections))
	for _, collection := range catalog.Collections {
		collections = append(collections, collection)
		tableByCollectionID[collection.ID] = collection.TableName
	}
	sort.Slice(collections, func(i, j int) bool {
		return collections[i].TableName < collections[j].TableName
	})

	for _, collection := range collections {
		if err := builder.addCollection(collection, tableByCollectionID); err != nil {
			return ExpectedPhysicalSchema{}, err
		}
	}
	builder.sort()

	manifest := ExpectedPhysicalSchema{required: builder.required, companion: builder.companion}
	manifest.ownership = buildPhysicalOwnership(manifest.required, manifest.companion)
	return manifest, nil
}

type physicalManifestBuilder struct {
	required  RequiredPhysicalObjects
	companion []IndexSpec
}

func (builder *physicalManifestBuilder) addCollection(collection Collection, tableByCollectionID map[uuid.UUID]string) error {
	builder.required.Tables = append(builder.required.Tables, TableSpec{
		Namespace:   applicationPhysicalNamespace,
		Name:        collection.TableName,
		Kind:        physicalTableOrdinary,
		Persistence: physicalTablePermanent,
	})
	builder.required.Columns = append(builder.required.Columns,
		ColumnSpec{Namespace: applicationPhysicalNamespace, Table: collection.TableName, Name: "id", DataType: "uuid", NotNull: true},
		ColumnSpec{Namespace: applicationPhysicalNamespace, Table: collection.TableName, Name: "created_at", DataType: "timestamp with time zone", NotNull: true},
		ColumnSpec{Namespace: applicationPhysicalNamespace, Table: collection.TableName, Name: "updated_at", DataType: "timestamp with time zone", NotNull: true},
	)

	primaryName := collection.TableName + "_pkey"
	builder.addKeyConstraint(applicationPhysicalNamespace, collection.TableName, primaryName, physicalConstraintPrimaryKey, []string{"id"})
	builder.addIndex(applicationPhysicalNamespace, collection.TableName, primaryName, []string{"id"}, true, true, primaryName)
	builder.addIndex(applicationPhysicalNamespace, collection.TableName, indexName(collection.Name, "created_at:id"), []string{"created_at", "id"}, false, false, "")
	builder.required.Indexes[len(builder.required.Indexes)-1].CompatibleNames = []string{legacyPageIndexName(collection.Name)}

	fields := append([]Field(nil), collection.Fields...)
	sort.Slice(fields, func(i, j int) bool {
		if fields[i].Ordinal != fields[j].Ordinal {
			return fields[i].Ordinal < fields[j].Ordinal
		}
		return fields[i].Name < fields[j].Name
	})
	for _, field := range fields {
		if field.Type != TypeRelation {
			defaultSQL := ""
			if len(field.Default) != 0 {
				_, rendered, err := normalizeStoredDefault(field.Type, field.Default)
				if err != nil {
					return ErrConflict
				}
				defaultSQL = rendered
			}
			builder.required.Columns = append(builder.required.Columns, ColumnSpec{
				Namespace:  applicationPhysicalNamespace,
				Table:      collection.TableName,
				Name:       field.ColumnName,
				DataType:   physicalColumnType(field.Type),
				NotNull:    field.Required,
				DefaultSQL: defaultSQL,
			})
			if field.Unique || field.Indexed {
				builder.addIndex(applicationPhysicalNamespace, collection.TableName, indexName(collection.Name, field.Name), []string{field.ColumnName}, field.Unique, false, "")
			}
			builder.addNullableDescCompanion(collection, field)
			continue
		}

		if field.Relation == nil {
			return ErrConflict
		}
		targetTable, ok := tableByCollectionID[field.Relation.TargetCollectionID]
		if !ok {
			return ErrConflict
		}
		names := NewRelationPhysicalNames(collection.TableName, field.Name)
		switch field.Relation.Cardinality {
		case RelationSingle:
			builder.required.Columns = append(builder.required.Columns, ColumnSpec{
				Namespace: applicationPhysicalNamespace,
				Table:     collection.TableName,
				Name:      field.ColumnName,
				DataType:  "uuid",
				NotNull:   field.Required,
			})
			builder.addForeignKey(
				applicationPhysicalNamespace, collection.TableName, names.SingleForeignKey, []string{field.ColumnName},
				applicationPhysicalNamespace, targetTable, []string{"id"}, physicalReferentialNoAction, true, true,
			)
			builder.addIndex(applicationPhysicalNamespace, collection.TableName, names.SingleReverseIndex, []string{field.ColumnName}, field.Unique, false, "")
			builder.addNullableDescCompanion(collection, field)
		case RelationMultiple:
			if err := builder.addMultiRelation(collection.TableName, targetTable, field, names); err != nil {
				return err
			}
		default:
			return ErrConflict
		}
	}
	return nil
}

// addNullableDescCompanion applies the exact predicate the read path uses to
// accept a sort key, so the companion set is a deterministic function of the
// catalog and never of a separate mutable row. A page over such a field orders
// by "<column> DESC NULLS LAST, id DESC". PostgreSQL's default btree on the
// column is ASC NULLS LAST, whose backward scan is DESC NULLS FIRST, so neither
// scan direction serves the ordering the public contract promises.
func (builder *physicalManifestBuilder) addNullableDescCompanion(collection Collection, field Field) {
	if !nullableDescSortable(field) {
		return
	}
	builder.companion = append(builder.companion, IndexSpec{
		Namespace: applicationPhysicalNamespace,
		Table:     collection.TableName,
		Name:      NullableDescCompanionIndexName(collection.Name, field.Name),
		Method:    "btree",
		Keys: []IndexKeySpec{
			{Column: field.ColumnName, Descending: true, NullsFirst: false},
			{Column: "id", Descending: true, NullsFirst: true},
		},
		Valid:   true,
		Ready:   true,
		Live:    true,
		Purpose: IndexPurposeNullableDesc,
	})
}

// nullableDescSortable reports whether a descending page over this field can
// observe a NULL. JSONB and multiple relations are rejected as sort keys, an
// unindexed field is rejected as unindexed, and a required column has no NULL
// to place, so none of those need a companion.
func nullableDescSortable(field Field) bool {
	if field.Required || field.Type == TypeJSONB {
		return false
	}
	if field.Type == TypeRelation && (field.Relation == nil || field.Relation.Cardinality != RelationSingle) {
		return false
	}
	return field.Indexed || field.Unique
}

// NullableDescCompanionIndexName is the deterministic companion index name for
// one catalog field. It shares the planner's owned "idx_" prefix so a binary
// that predates the rollout still classifies the object as PgBase-owned.
func NullableDescCompanionIndexName(collection, field string) string {
	return indexName(collection, field+nullableDescIndexSuffix)
}

func (builder *physicalManifestBuilder) addMultiRelation(sourceTable, targetTable string, field Field, names RelationPhysicalNames) error {
	table := names.MultiTable
	builder.required.Tables = append(builder.required.Tables, TableSpec{
		Namespace:   relationPhysicalNamespace,
		Name:        table,
		Kind:        physicalTableOrdinary,
		Persistence: physicalTablePermanent,
	})
	builder.required.Columns = append(builder.required.Columns,
		ColumnSpec{Namespace: relationPhysicalNamespace, Table: table, Name: "source_id", DataType: "uuid", NotNull: true},
		ColumnSpec{Namespace: relationPhysicalNamespace, Table: table, Name: "target_id", DataType: "uuid", NotNull: true},
		ColumnSpec{Namespace: relationPhysicalNamespace, Table: table, Name: "position", DataType: "integer", NotNull: true},
	)
	builder.addKeyConstraint(relationPhysicalNamespace, table, names.MultiPrimaryKey, physicalConstraintPrimaryKey, []string{"source_id", "target_id"})
	builder.addIndex(relationPhysicalNamespace, table, names.MultiPrimaryKey, []string{"source_id", "target_id"}, true, true, names.MultiPrimaryKey)
	builder.addKeyConstraint(relationPhysicalNamespace, table, names.MultiPositionUnique, physicalConstraintUnique, []string{"source_id", "position"})
	builder.addIndex(relationPhysicalNamespace, table, names.MultiPositionUnique, []string{"source_id", "position"}, true, false, names.MultiPositionUnique)
	builder.required.Constraints = append(builder.required.Constraints, ConstraintSpec{
		Namespace: relationPhysicalNamespace,
		Table:     table,
		Name:      names.MultiPositionCheck,
		Kind:      physicalConstraintCheck,
		Columns:   []string{"position"},
		Match:     physicalReferentialUnused,
		OnUpdate:  physicalReferentialUnused,
		OnDelete:  physicalReferentialUnused,
		CheckSQL:  "position >= 0",
		Validated: true,
		Enforced:  true,
	})
	builder.addForeignKey(
		relationPhysicalNamespace, table, names.MultiSourceForeignKey, []string{"source_id"},
		applicationPhysicalNamespace, sourceTable, []string{"id"}, physicalReferentialCascade, false, false,
	)
	builder.addForeignKey(
		relationPhysicalNamespace, table, names.MultiTargetForeignKey, []string{"target_id"},
		applicationPhysicalNamespace, targetTable, []string{"id"}, physicalReferentialNoAction, true, true,
	)
	builder.addIndex(relationPhysicalNamespace, table, names.MultiReverseIndex, []string{"target_id", "source_id"}, false, false, "")

	positionDefinition := positionFunctionStatement(sourceTable, field.Name)
	positionBody, ok := plannerFunctionBody(positionDefinition)
	if !ok {
		return ErrConflict
	}
	cardinalityDefinition := cardinalityFunctionStatement(sourceTable, field.Name, field.Relation.MinSelect, field.Relation.MaxSelect, false)
	cardinalityBody, ok := plannerFunctionBody(cardinalityDefinition)
	if !ok {
		return ErrConflict
	}
	builder.required.Functions = append(builder.required.Functions,
		newTriggerFunctionSpec(names.PositionFunction, positionBody),
		newTriggerFunctionSpec(names.CardinalityFunction, cardinalityBody),
	)
	builder.addConstraintTrigger(relationPhysicalNamespace, table, names.PositionTrigger, names.PositionFunction,
		physicalTriggerAfterRow|physicalTriggerInsert|physicalTriggerUpdate|physicalTriggerDelete)
	builder.addConstraintTrigger(applicationPhysicalNamespace, sourceTable, names.SourceCardinalityTrigger, names.CardinalityFunction,
		physicalTriggerAfterRow|physicalTriggerInsert)
	builder.addConstraintTrigger(relationPhysicalNamespace, table, names.EdgeCardinalityTrigger, names.CardinalityFunction,
		physicalTriggerAfterRow|physicalTriggerInsert|physicalTriggerUpdate|physicalTriggerDelete)
	return nil
}

func (builder *physicalManifestBuilder) addKeyConstraint(namespace, table, name, kind string, columns []string) {
	builder.required.Constraints = append(builder.required.Constraints, ConstraintSpec{
		Namespace: namespace,
		Table:     table,
		Name:      name,
		Kind:      kind,
		Columns:   append([]string(nil), columns...),
		Match:     physicalReferentialUnused,
		OnUpdate:  physicalReferentialUnused,
		OnDelete:  physicalReferentialUnused,
		Validated: true,
		Enforced:  true,
		NoInherit: true,
	})
}

func (builder *physicalManifestBuilder) addForeignKey(
	namespace, table, name string,
	columns []string,
	targetNamespace, targetTable string,
	targetColumns []string,
	deleteAction string,
	deferrable, initiallyDeferred bool,
) {
	builder.required.Constraints = append(builder.required.Constraints, ConstraintSpec{
		Namespace:          namespace,
		Table:              table,
		Name:               name,
		Kind:               physicalConstraintForeignKey,
		Columns:            append([]string(nil), columns...),
		ReferenceNamespace: targetNamespace,
		ReferenceTable:     targetTable,
		ReferenceColumns:   append([]string(nil), targetColumns...),
		Match:              physicalMatchSimple,
		OnUpdate:           physicalReferentialNoAction,
		OnDelete:           deleteAction,
		Deferrable:         deferrable,
		InitiallyDeferred:  initiallyDeferred,
		Validated:          true,
		Enforced:           true,
		NoInherit:          true,
	})
}

func (builder *physicalManifestBuilder) addIndex(namespace, table, name string, columns []string, unique, primary bool, constraintName string) {
	keys := make([]IndexKeySpec, len(columns))
	for index, column := range columns {
		keys[index] = IndexKeySpec{Column: column}
	}
	builder.required.Indexes = append(builder.required.Indexes, IndexSpec{
		Namespace:      namespace,
		Table:          table,
		Name:           name,
		Method:         "btree",
		Keys:           keys,
		Unique:         unique,
		Primary:        primary,
		ConstraintName: constraintName,
		Valid:          true,
		Ready:          true,
		Live:           true,
	})
}

func (builder *physicalManifestBuilder) sort() {
	sort.Strings(builder.required.Namespaces)
	sort.Slice(builder.required.Tables, func(i, j int) bool {
		left, right := builder.required.Tables[i], builder.required.Tables[j]
		return physicalLess(left.Namespace, "", left.Name, right.Namespace, "", right.Name)
	})
	sort.Slice(builder.required.Columns, func(i, j int) bool {
		left, right := builder.required.Columns[i], builder.required.Columns[j]
		if left.Namespace != right.Namespace {
			return left.Namespace < right.Namespace
		}
		if left.Table != right.Table {
			return left.Table < right.Table
		}
		return left.Name < right.Name
	})
	sort.Slice(builder.required.Indexes, func(i, j int) bool {
		left, right := builder.required.Indexes[i], builder.required.Indexes[j]
		return physicalLess(left.Namespace, left.Table, left.Name, right.Namespace, right.Table, right.Name)
	})
	sort.Slice(builder.companion, func(i, j int) bool {
		left, right := builder.companion[i], builder.companion[j]
		return physicalLess(left.Namespace, left.Table, left.Name, right.Namespace, right.Table, right.Name)
	})
	sort.Slice(builder.required.Constraints, func(i, j int) bool {
		left, right := builder.required.Constraints[i], builder.required.Constraints[j]
		return physicalLess(left.Namespace, left.Table, left.Name, right.Namespace, right.Table, right.Name)
	})
	sort.Slice(builder.required.Triggers, func(i, j int) bool {
		left, right := builder.required.Triggers[i], builder.required.Triggers[j]
		return physicalLess(left.Namespace, left.Table, left.Name, right.Namespace, right.Table, right.Name)
	})
	sort.Slice(builder.required.Functions, func(i, j int) bool {
		left, right := builder.required.Functions[i], builder.required.Functions[j]
		return physicalLess(left.Namespace, "", left.Name, right.Namespace, "", right.Name)
	})
}

func physicalLess(leftNamespace, leftTable, leftName, rightNamespace, rightTable, rightName string) bool {
	if leftNamespace != rightNamespace {
		return leftNamespace < rightNamespace
	}
	if leftTable != rightTable {
		return leftTable < rightTable
	}
	return leftName < rightName
}

func physicalColumnType(fieldType FieldType) string {
	if fieldType == TypeTimestampTZ {
		return "timestamp with time zone"
	}
	return string(fieldType)
}

// legacyPageIndexName preserves the accepted name emitted by system migration
// 000002 for collections that existed before the current planner name helper.
func legacyPageIndexName(collection string) string {
	digest := md5.Sum([]byte(collection + ":created_at:id")) // #nosec G401 -- compatibility identifier, not cryptography.
	return "idx_page_" + hex.EncodeToString(digest[:])[:legacyPageIndexHashLength]
}

func plannerFunctionBody(definition string) (string, bool) {
	const delimiter = "$pgbase_relation$"
	start := strings.Index(definition, delimiter)
	end := strings.LastIndex(definition, delimiter)
	if start < 0 || end <= start {
		return "", false
	}
	start += len(delimiter)
	return definition[start:end], true
}

func newTriggerFunctionSpec(name, body string) FunctionSpec {
	return FunctionSpec{
		Namespace:      relationPhysicalNamespace,
		Name:           name,
		ReturnsTrigger: true,
		Language:       "plpgsql",
		Volatility:     physicalFunctionVolatile,
		Kind:           physicalFunctionOrdinary,
		Parallel:       physicalFunctionUnsafe,
		BodySQL:        body,
	}
}

func newConstraintTriggerSpec(namespace, table, name, functionName string, typeMask int) TriggerSpec {
	return TriggerSpec{
		Namespace:         namespace,
		Table:             table,
		Name:              name,
		FunctionNamespace: relationPhysicalNamespace,
		FunctionName:      functionName,
		TypeMask:          typeMask,
		Enabled:           physicalTriggerEnabledOrigin,
		Constraint:        true,
		Deferrable:        true,
		InitiallyDeferred: true,
	}
}

func (builder *physicalManifestBuilder) addConstraintTrigger(namespace, table, name, functionName string, typeMask int) {
	builder.required.Constraints = append(builder.required.Constraints, ConstraintSpec{
		Namespace:         namespace,
		Table:             table,
		Name:              name,
		Kind:              physicalConstraintTrigger,
		Match:             physicalReferentialUnused,
		OnUpdate:          physicalReferentialUnused,
		OnDelete:          physicalReferentialUnused,
		Deferrable:        true,
		InitiallyDeferred: true,
		Validated:         true,
		Enforced:          true,
		NoInherit:         true,
	})
	builder.required.Triggers = append(builder.required.Triggers,
		newConstraintTriggerSpec(namespace, table, name, functionName, typeMask))
}

// buildPhysicalOwnership computes ownership over the union of required and
// companion objects. A companion is owned from the moment the catalog implies
// it, well before any capability promotes it to a required contract, so a
// rollback binary cannot read it as an orphan.
func buildPhysicalOwnership(required RequiredPhysicalObjects, companion []IndexSpec) physicalOwnership {
	ownership := physicalOwnership{
		exact:          make(map[physicalObjectKey]struct{}),
		requiredTables: make(map[physicalTableKey]struct{}, len(required.Tables)),
		namespaceKinds: map[string]map[PhysicalObjectKind]struct{}{
			relationPhysicalNamespace: {
				PhysicalObjectTable:      {},
				PhysicalObjectColumn:     {},
				PhysicalObjectIndex:      {},
				PhysicalObjectConstraint: {},
				PhysicalObjectTrigger:    {},
				PhysicalObjectFunction:   {},
			},
		},
		prefixes: []physicalOwnedPrefix{
			{namespace: applicationPhysicalNamespace, kind: PhysicalObjectIndex, prefix: "idx_"},
			{namespace: applicationPhysicalNamespace, kind: PhysicalObjectIndex, prefix: "rel_srev_"},
			{namespace: applicationPhysicalNamespace, kind: PhysicalObjectConstraint, prefix: "rel_sfk_"},
			{namespace: applicationPhysicalNamespace, kind: PhysicalObjectConstraint, prefix: "rel_card_src_trg_"},
			{namespace: applicationPhysicalNamespace, kind: PhysicalObjectTrigger, prefix: "rel_card_src_trg_"},
		},
	}
	for _, namespace := range required.Namespaces {
		ownership.addExact(PhysicalObjectRef{Kind: PhysicalObjectNamespace, Namespace: namespace})
	}
	for _, table := range required.Tables {
		ownership.requiredTables[physicalTableKey{namespace: table.Namespace, name: table.Name}] = struct{}{}
		ownership.addExact(PhysicalObjectRef{Kind: PhysicalObjectTable, Namespace: table.Namespace, Name: table.Name})
	}
	for _, column := range required.Columns {
		ownership.addExact(PhysicalObjectRef{Kind: PhysicalObjectColumn, Namespace: column.Namespace, Table: column.Table, Name: column.Name})
	}
	for _, index := range required.Indexes {
		ownership.addExact(PhysicalObjectRef{Kind: PhysicalObjectIndex, Namespace: index.Namespace, Table: index.Table, Name: index.Name})
		for _, compatibleName := range index.CompatibleNames {
			ownership.addExact(PhysicalObjectRef{Kind: PhysicalObjectIndex, Namespace: index.Namespace, Table: index.Table, Name: compatibleName})
		}
	}
	for _, constraint := range required.Constraints {
		ownership.addExact(PhysicalObjectRef{Kind: PhysicalObjectConstraint, Namespace: constraint.Namespace, Table: constraint.Table, Name: constraint.Name})
	}
	for _, trigger := range required.Triggers {
		ownership.addExact(PhysicalObjectRef{Kind: PhysicalObjectTrigger, Namespace: trigger.Namespace, Table: trigger.Table, Name: trigger.Name})
	}
	for _, function := range required.Functions {
		ownership.addExact(PhysicalObjectRef{Kind: PhysicalObjectFunction, Namespace: function.Namespace, Name: function.Name})
	}
	for _, index := range companion {
		ownership.addExact(PhysicalObjectRef{Kind: PhysicalObjectIndex, Namespace: index.Namespace, Table: index.Table, Name: index.Name})
	}
	return ownership
}

func (ownership *physicalOwnership) addExact(ref PhysicalObjectRef) {
	ownership.exact[physicalObjectKey{kind: ref.Kind, namespace: ref.Namespace, table: ref.Table, name: ref.Name}] = struct{}{}
}

func validPhysicalObjectRef(ref PhysicalObjectRef) bool {
	if ref.Namespace == "" {
		return false
	}
	switch ref.Kind {
	case PhysicalObjectNamespace:
		return ref.Table == "" && ref.Name == ""
	case PhysicalObjectTable, PhysicalObjectFunction:
		return ref.Table == "" && ref.Name != ""
	case PhysicalObjectColumn, PhysicalObjectIndex, PhysicalObjectConstraint, PhysicalObjectTrigger:
		return ref.Table != "" && ref.Name != ""
	default:
		return false
	}
}

func cloneIndexSpecs(source []IndexSpec) []IndexSpec {
	clone := append([]IndexSpec(nil), source...)
	for index := range clone {
		clone[index].CompatibleNames = append([]string(nil), source[index].CompatibleNames...)
		clone[index].Keys = append([]IndexKeySpec(nil), source[index].Keys...)
		clone[index].IncludedColumns = append([]string(nil), source[index].IncludedColumns...)
	}
	return clone
}

func cloneRequiredPhysicalObjects(source RequiredPhysicalObjects) RequiredPhysicalObjects {
	clone := source
	clone.Namespaces = append([]string(nil), source.Namespaces...)
	clone.Tables = append([]TableSpec(nil), source.Tables...)
	clone.Columns = append([]ColumnSpec(nil), source.Columns...)
	clone.Indexes = cloneIndexSpecs(source.Indexes)
	clone.Constraints = append([]ConstraintSpec(nil), source.Constraints...)
	for index := range clone.Constraints {
		clone.Constraints[index].Columns = append([]string(nil), source.Constraints[index].Columns...)
		clone.Constraints[index].ReferenceColumns = append([]string(nil), source.Constraints[index].ReferenceColumns...)
	}
	clone.Triggers = append([]TriggerSpec(nil), source.Triggers...)
	for index := range clone.Triggers {
		clone.Triggers[index].FunctionArgumentTypes = append([]string(nil), source.Triggers[index].FunctionArgumentTypes...)
		clone.Triggers[index].UpdateColumns = append([]string(nil), source.Triggers[index].UpdateColumns...)
		clone.Triggers[index].Arguments = append([]string(nil), source.Triggers[index].Arguments...)
	}
	clone.Functions = append([]FunctionSpec(nil), source.Functions...)
	for index := range clone.Functions {
		clone.Functions[index].ArgumentTypes = append([]string(nil), source.Functions[index].ArgumentTypes...)
	}
	return clone
}
