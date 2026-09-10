package schema

import (
	"crypto/md5"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/google/uuid"
)

type physicalManifestBuilder struct {
	required  RequiredPhysicalObjects
	companion []IndexSpec
}

func (builder *physicalManifestBuilder) addCollection(collection Collection, tableByCollectionID map[uuid.UUID]string) error {
	if collection.View != nil {
		builder.required.Tables = append(builder.required.Tables, TableSpec{Namespace: applicationPhysicalNamespace, Name: collection.TableName, Kind: physicalTableView, Persistence: physicalTablePermanent})
		builder.required.Columns = append(builder.required.Columns, ColumnSpec{Namespace: applicationPhysicalNamespace, Table: collection.TableName, Name: "id", DataType: "uuid"}, ColumnSpec{Namespace: applicationPhysicalNamespace, Table: collection.TableName, Name: "created_at", DataType: "timestamp with time zone"}, ColumnSpec{Namespace: applicationPhysicalNamespace, Table: collection.TableName, Name: "updated_at", DataType: "timestamp with time zone"})
		for _, field := range collection.Fields {
			builder.required.Columns = append(builder.required.Columns, ColumnSpec{Namespace: applicationPhysicalNamespace, Table: collection.TableName, Name: field.ColumnName, DataType: physicalColumnType(field.Type)})
		}
		return nil
	}
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
		if field.Type == TypeFile {
			continue
		}
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
				NotNull:    field.Required || field.Type == TypeEditor || field.Type == TypeGeoPoint,
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
		names := NewRelationPhysicalNames(collection.TableName, field.ColumnName)
		switch field.Relation.Cardinality {
		case RelationSingle:
			builder.required.Columns = append(builder.required.Columns, ColumnSpec{
				Namespace: applicationPhysicalNamespace,
				Table:     collection.TableName,
				Name:      field.ColumnName,
				DataType:  "uuid",
				NotNull:   field.Required || field.Type == TypeEditor || field.Type == TypeGeoPoint,
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
	fieldColumns := make(map[string]string, len(collection.Fields))
	for _, field := range collection.Fields {
		fieldColumns[field.Name] = field.ColumnName
	}
	for _, index := range collection.Indexes {
		spec, err := collectionIndexPhysicalSpec(collection, index, fieldColumns)
		if err != nil {
			return err
		}
		builder.required.Indexes = append(builder.required.Indexes, spec)
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
	if !nullableDescSortable(field.Required, field.Indexed || field.Unique,
		field.Relation != nil && field.Relation.Cardinality == RelationSingle, field.Type) {
		return
	}
	builder.companion = append(builder.companion,
		nullableDescCompanionSpec(collection.Name, collection.TableName, field.Name, field.ColumnName))
}

// nullableDescCompanionSpec is the single written-down shape of a companion
// index. The manifest compiles it from a catalog field and the create planner
// from a planned field, so the index a v2 create builds in its own apply
// transaction and the manifest that later verifies it cannot disagree.
func nullableDescCompanionSpec(collection, table, field, column string) IndexSpec {
	return IndexSpec{
		Namespace: applicationPhysicalNamespace,
		Table:     table,
		Name:      NullableDescCompanionIndexName(collection, field),
		Method:    "btree",
		Keys: []IndexKeySpec{
			{Column: column, Descending: true, NullsFirst: false},
			{Column: "id", Descending: true, NullsFirst: true},
		},
		Valid:   true,
		Ready:   true,
		Live:    true,
		Purpose: IndexPurposeNullableDesc,
	}
}

// nullableDescSortable reports whether a descending page over a field of this shape
// can observe a NULL. JSONB and multiple relations are rejected as sort keys, an
// unindexed field is rejected as unindexed, and a required column has no NULL to place,
// so none of those need a companion. It takes the shape rather than a Field because the
// create planner holds a PlannedField, and this rule may have exactly one owner.
func nullableDescSortable(required, indexed, singleRelation bool, fieldType FieldType) bool {
	if required || fieldType == TypeJSONB {
		return false
	}
	if fieldType == TypeRelation && !singleRelation {
		return false
	}
	return indexed
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

	positionDefinition := positionFunctionStatement(sourceTable, field.ColumnName)
	positionBody, ok := plannerFunctionBody(positionDefinition)
	if !ok {
		return ErrConflict
	}
	cardinalityDefinition := cardinalityFunctionStatement(sourceTable, field.ColumnName, field.Relation.MinSelect, field.Relation.MaxSelect, false)
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
	if fieldType == TypeTimestampTZ || fieldType == TypeAutoDate {
		return "timestamp with time zone"
	}
	return physicalFieldType(fieldType)
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
