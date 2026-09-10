package doctor

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/schema"
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

// readRelationBindings reports whether the relation catalog is installed at
// all. A database still on a pre-relation migration has no relation metadata
// and no compilable catalog, so the caller uses the same probe to decide
// whether a physical comparison is possible.
func readRelationBindings(ctx context.Context, db database.DBTX) ([]relationBinding, bool, error) {
	var relationCatalogPresent bool
	if err := db.QueryRow(ctx, `select to_regclass('pgbase_meta.field_relations') is not null`).Scan(&relationCatalogPresent); err != nil {
		return nil, false, fmt.Errorf("check relation catalog: %w", err)
	}
	if !relationCatalogPresent {
		return []relationBinding{}, false, nil
	}
	rows, err := db.Query(ctx, `select field_id, source_collection_id, target_collection_id,
		cardinality, min_select, max_select, on_delete, value_codec_version,
		storage_kind, storage_name
		from pgbase_meta.field_relations
		order by field_id`)
	if err != nil {
		return nil, true, fmt.Errorf("read relation catalog: %w", err)
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
			return nil, true, fmt.Errorf("scan relation catalog: %w", err)
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, true, fmt.Errorf("iterate relation catalog: %w", err)
	}
	return bindings, true, nil
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
			appendUniqueFinding(report, "relation_metadata_orphaned", "Relation metadata is not bound to a catalog field")
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
				appendUniqueFinding(report, "relation_metadata_unexpected", "A scalar field has relation metadata")
			}
			continue
		}
		if len(fieldBindings) == 0 {
			appendUniqueFinding(report, "relation_metadata_missing", "A relation field is missing its relation metadata")
			continue
		}
		if len(fieldBindings) != 1 {
			appendUniqueFinding(report, "relation_metadata_mismatch", "Relation metadata is internally inconsistent")
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
			appendUniqueFinding(report, "relation_metadata_mismatch", "Relation metadata is internally inconsistent")
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
			appendUniqueFinding(report, "relation_metadata_mismatch", "Relation metadata is internally inconsistent")
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

// appendUniqueFinding keeps one code out of the report more than once. Finding
// codes name no collection, field or object, so a second copy tells an operator
// nothing and only grows the payload a badly drifted database produces.
func appendUniqueFinding(report *Report, code, message string) {
	for _, existing := range report.Findings {
		if existing.Code == code {
			return
		}
	}
	report.Findings = append(report.Findings, finding(code, message))
}
