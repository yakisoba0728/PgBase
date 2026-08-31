package schema

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
)

const (
	// RelationValueCodecVersion is included in relation schema digests so a
	// future JSON representation can be introduced without silently changing
	// existing record validators.
	RelationValueCodecVersion = 1

	maximumRelationSelect = 1000
)

type RelationCardinality string

const (
	RelationSingle   RelationCardinality = "single"
	RelationMultiple RelationCardinality = "multiple"
)

type RelationDeleteAction string

const (
	RelationRestrict RelationDeleteAction = "restrict"
	RelationCascade  RelationDeleteAction = "cascade"
	RelationSetNull  RelationDeleteAction = "set_null"
)

type RelationStorageKind string

const (
	RelationStorageInlineFKV1  RelationStorageKind = "inline_fk_v1"
	RelationStorageEdgeTableV1 RelationStorageKind = "edge_table_v1"
)

// RelationInput preserves optional integer members so normalization can
// distinguish an omitted bound from an explicitly supplied zero.
type RelationInput struct {
	TargetCollection string               `json:"targetCollection"`
	Cardinality      RelationCardinality  `json:"cardinality,omitempty"`
	MinSelect        *int                 `json:"minSelect,omitempty"`
	MaxSelect        *int                 `json:"maxSelect,omitempty"`
	OnDelete         RelationDeleteAction `json:"onDelete,omitempty"`
}

// PlannedRelation preserves the symbolic target name in a deterministic plan.
// TargetCollectionID is nil only for a create-time self relation; apply resolves
// that symbolic edge to the newly allocated source collection ID.
type PlannedRelation struct {
	TargetCollection   string               `json:"targetCollection"`
	TargetCollectionID *uuid.UUID           `json:"targetCollectionId,omitempty"`
	Cardinality        RelationCardinality  `json:"cardinality"`
	MinSelect          int                  `json:"minSelect"`
	MaxSelect          int                  `json:"maxSelect"`
	OnDelete           RelationDeleteAction `json:"onDelete"`
	ValueCodecVersion  int                  `json:"valueCodecVersion"`
	Storage            RelationStorage      `json:"storage"`
}

// RelationSpec is the resolved, normalized relation definition held by a
// catalog snapshot. It contains values only; catalog cloning replaces every
// pointer to a spec so published snapshots cannot alias caller state.
type RelationSpec struct {
	TargetCollectionID uuid.UUID            `json:"targetCollectionId"`
	Cardinality        RelationCardinality  `json:"cardinality"`
	MinSelect          int                  `json:"minSelect"`
	MaxSelect          int                  `json:"maxSelect"`
	OnDelete           RelationDeleteAction `json:"onDelete"`
	ValueCodecVersion  int                  `json:"valueCodecVersion"`
	Storage            RelationStorage      `json:"storage"`
}

type RelationStorage struct {
	Kind RelationStorageKind `json:"kind"`
	Name string              `json:"name"`
}

// RelationRef is the precomputed incoming edge stored under the target
// collection ID. Source IDs identify the owning field without duplicating
// mutable catalog names, while Spec carries the delete and storage semantics.
type RelationRef struct {
	SourceCollectionID uuid.UUID    `json:"sourceCollectionId"`
	SourceFieldID      uuid.UUID    `json:"sourceFieldId"`
	Spec               RelationSpec `json:"spec"`
}

var relationStorageIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// NormalizeRelationField validates the relation-specific members of a field,
// returns a caller-owned normalized input, and binds an immutable catalog spec.
//
// The caller resolves TargetCollection against its pinned catalog and supplies
// the resulting optional ID plus a deterministic physical storage name. A nil
// ID is accepted only when TargetCollection equals sourceCollection, preserving
// a create-time self reference symbolically. Scalar fields do not require any
// binding argument. Every returned ValidationError is sorted by field path and
// code, making the contract stable across runs.
func NormalizeRelationField(
	fieldPath string,
	sourceCollection string,
	field FieldInput,
	targetCollectionID *uuid.UUID,
	storageName string,
) (FieldInput, *PlannedRelation, error) {
	normalized := cloneFieldInput(field)
	if field.Type != TypeRelation {
		if field.Relation != nil {
			return FieldInput{}, nil, relationValidationError([]ValidationDetail{{
				Field: fieldPath + ".relation",
				Code:  "relation_config_forbidden",
			}})
		}
		return normalized, nil, nil
	}

	if field.Relation == nil {
		return FieldInput{}, nil, relationValidationError([]ValidationDetail{{
			Field: fieldPath + ".relation",
			Code:  "relation_config_required",
		}})
	}

	relation := cloneRelationInput(*field.Relation)
	if relation.Cardinality == "" {
		relation.Cardinality = RelationSingle
	}
	if relation.OnDelete == "" {
		relation.OnDelete = RelationRestrict
	}

	minSelect := 0
	if field.Required {
		minSelect = 1
	}
	minWasExplicit := relation.MinSelect != nil
	if minWasExplicit {
		minSelect = *relation.MinSelect
	}
	relation.MinSelect = intPointer(minSelect)

	maxSelect := 0
	maxWasExplicit := relation.MaxSelect != nil
	if maxWasExplicit {
		maxSelect = *relation.MaxSelect
	} else if relation.Cardinality == RelationSingle {
		maxSelect = 1
	}
	relation.MaxSelect = intPointer(maxSelect)

	details := make([]ValidationDetail, 0, 8)
	addDetail := func(member, code string) {
		details = append(details, ValidationDetail{Field: fieldPath + member, Code: code})
	}

	if strings.TrimSpace(relation.TargetCollection) == "" {
		addDetail(".relation.targetCollection", "relation_target_required")
	} else if targetCollectionID == nil && relation.TargetCollection != sourceCollection {
		addDetail(".relation.targetCollection", "relation_target_collection_not_found")
	} else if targetCollectionID != nil && *targetCollectionID == uuid.Nil {
		addDetail(".relation.targetCollection", "relation_target_collection_not_found")
	}

	switch relation.Cardinality {
	case RelationSingle:
		if minSelect < 0 || minSelect > 1 {
			addDetail(".relation.minSelect", "relation_min_select_invalid")
		}
		if maxSelect != 1 {
			addDetail(".relation.maxSelect", "relation_max_select_invalid")
		}
	case RelationMultiple:
		if !maxWasExplicit {
			addDetail(".relation.maxSelect", "relation_max_select_required")
		} else if maxSelect < 2 || maxSelect > maximumRelationSelect {
			addDetail(".relation.maxSelect", "relation_max_select_invalid")
		}
		if minSelect < 0 || (maxWasExplicit && minSelect > maxSelect) {
			addDetail(".relation.minSelect", "relation_min_select_invalid")
		}
	default:
		addDetail(".relation.cardinality", "relation_cardinality_invalid")
	}

	if minWasExplicit && field.Required != (minSelect > 0) {
		addDetail(".relation.minSelect", "relation_required_mismatch")
	}

	switch relation.OnDelete {
	case RelationRestrict, RelationCascade:
	case RelationSetNull:
		if minSelect != 0 {
			addDetail(".relation.onDelete", "relation_set_null_requires_optional")
		}
	default:
		addDetail(".relation.onDelete", "relation_delete_action_invalid")
	}

	if len(field.Default) != 0 {
		addDetail(".default", "relation_default_forbidden")
	}
	if field.Unique && relation.Cardinality != RelationSingle {
		addDetail(".unique", "relation_unique_requires_single")
	}
	if !relationStorageIdentifier.MatchString(storageName) {
		addDetail(".relation.storage", "relation_storage_invalid")
	}

	if len(details) != 0 {
		return FieldInput{}, nil, relationValidationError(details)
	}

	storageKind := RelationStorageInlineFKV1
	if relation.Cardinality == RelationMultiple {
		storageKind = RelationStorageEdgeTableV1
	}
	normalized.Relation = &relation
	// Every relation needs a reverse lookup index. Unique single relations use
	// the same physical index as their one-to-one constraint.
	normalized.Indexed = true
	planned := &PlannedRelation{
		TargetCollection:   relation.TargetCollection,
		TargetCollectionID: cloneUUIDPointer(targetCollectionID),
		Cardinality:        relation.Cardinality,
		MinSelect:          minSelect,
		MaxSelect:          maxSelect,
		OnDelete:           relation.OnDelete,
		ValueCodecVersion:  RelationValueCodecVersion,
		Storage: RelationStorage{
			Kind: storageKind,
			Name: storageName,
		},
	}
	return normalized, planned, nil
}

// ResolvePlannedRelation turns a planned relation into the immutable catalog
// spec persisted at apply time. A nil planned target is the symbolic self edge
// and resolves to sourceCollectionID; no synthetic UUID is introduced.
func ResolvePlannedRelation(fieldPath string, planned PlannedRelation, sourceCollectionID uuid.UUID) (*RelationSpec, error) {
	targetCollectionID := sourceCollectionID
	if planned.TargetCollectionID != nil {
		targetCollectionID = *planned.TargetCollectionID
	}
	if targetCollectionID == uuid.Nil {
		return nil, relationValidationError([]ValidationDetail{{
			Field: fieldPath + ".relation.targetCollection",
			Code:  "relation_target_collection_not_found",
		}})
	}
	spec := &RelationSpec{
		TargetCollectionID: targetCollectionID,
		Cardinality:        planned.Cardinality,
		MinSelect:          planned.MinSelect,
		MaxSelect:          planned.MaxSelect,
		OnDelete:           planned.OnDelete,
		ValueCodecVersion:  planned.ValueCodecVersion,
		Storage:            planned.Storage,
	}
	if err := ValidateRelationSpec(fieldPath, *spec); err != nil {
		return nil, err
	}
	return spec, nil
}

// PlannedRelationFromSpec reconstructs a deterministic update/delete plan
// value from a catalog spec. The target name comes from the same pinned catalog
// and the UUID pointer is newly allocated, so the plan cannot alias catalog
// state.
func PlannedRelationFromSpec(targetCollection string, spec RelationSpec) PlannedRelation {
	targetCollectionID := spec.TargetCollectionID
	return PlannedRelation{
		TargetCollection:   targetCollection,
		TargetCollectionID: &targetCollectionID,
		Cardinality:        spec.Cardinality,
		MinSelect:          spec.MinSelect,
		MaxSelect:          spec.MaxSelect,
		OnDelete:           spec.OnDelete,
		ValueCodecVersion:  spec.ValueCodecVersion,
		Storage:            spec.Storage,
	}
}

// ValidateRelationSpec checks catalog metadata read from durable storage before
// it is published to request readers. Field-level required/default/unique
// consistency remains the catalog loader's responsibility because those flags
// are not duplicated in RelationSpec.
func ValidateRelationSpec(fieldPath string, spec RelationSpec) error {
	details := make([]ValidationDetail, 0, 6)
	addDetail := func(member, code string) {
		details = append(details, ValidationDetail{Field: fieldPath + member, Code: code})
	}

	if spec.TargetCollectionID == uuid.Nil {
		addDetail(".relation.targetCollection", "relation_target_collection_not_found")
	}

	wantStorageKind := RelationStorageKind("")
	switch spec.Cardinality {
	case RelationSingle:
		wantStorageKind = RelationStorageInlineFKV1
		if spec.MinSelect < 0 || spec.MinSelect > 1 {
			addDetail(".relation.minSelect", "relation_min_select_invalid")
		}
		if spec.MaxSelect != 1 {
			addDetail(".relation.maxSelect", "relation_max_select_invalid")
		}
	case RelationMultiple:
		wantStorageKind = RelationStorageEdgeTableV1
		if spec.MaxSelect < 2 || spec.MaxSelect > maximumRelationSelect {
			addDetail(".relation.maxSelect", "relation_max_select_invalid")
		}
		if spec.MinSelect < 0 || spec.MinSelect > spec.MaxSelect {
			addDetail(".relation.minSelect", "relation_min_select_invalid")
		}
	default:
		addDetail(".relation.cardinality", "relation_cardinality_invalid")
	}

	switch spec.OnDelete {
	case RelationRestrict, RelationCascade:
	case RelationSetNull:
		if spec.MinSelect != 0 {
			addDetail(".relation.onDelete", "relation_set_null_requires_optional")
		}
	default:
		addDetail(".relation.onDelete", "relation_delete_action_invalid")
	}
	if spec.ValueCodecVersion != RelationValueCodecVersion {
		addDetail(".relation.valueCodecVersion", "relation_value_codec_unsupported")
	}
	if wantStorageKind == "" || spec.Storage.Kind != wantStorageKind {
		addDetail(".relation.storage.kind", "relation_storage_invalid")
	}
	if !relationStorageIdentifier.MatchString(spec.Storage.Name) {
		addDetail(".relation.storage.name", "relation_storage_invalid")
	}

	if len(details) != 0 {
		return relationValidationError(details)
	}
	return nil
}

func relationValidationError(details []ValidationDetail) error {
	sort.Slice(details, func(i, j int) bool {
		if details[i].Field != details[j].Field {
			return details[i].Field < details[j].Field
		}
		return details[i].Code < details[j].Code
	})
	return &ValidationError{Details: details}
}

func cloneFieldInput(field FieldInput) FieldInput {
	field.Default = append(json.RawMessage(nil), field.Default...)
	if field.Relation != nil {
		relation := cloneRelationInput(*field.Relation)
		field.Relation = &relation
	}
	return field
}

func cloneRelationInput(relation RelationInput) RelationInput {
	if relation.MinSelect != nil {
		relation.MinSelect = intPointer(*relation.MinSelect)
	}
	if relation.MaxSelect != nil {
		relation.MaxSelect = intPointer(*relation.MaxSelect)
	}
	return relation
}

func cloneRelationSpec(spec *RelationSpec) *RelationSpec {
	if spec == nil {
		return nil
	}
	clone := *spec
	return &clone
}

func cloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func intPointer(value int) *int { return &value }
