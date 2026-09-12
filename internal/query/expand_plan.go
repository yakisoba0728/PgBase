package query

import (
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

const maximumExpandBytes = 4096

type expandPlan struct {
	nodes []expandNode
}

type expandNodeKind uint8

const (
	expandNodeForward expandNodeKind = iota
	expandNodeReverse
)

type expandNode struct {
	name     string
	kind     expandNodeKind
	field    schema.Field
	target   schema.Collection
	children []expandNode
}

type reverseExpandIndex map[uuid.UUID]map[string][]expandNode

func compileExpandPlan(catalog schema.Catalog, root schema.Collection, text string, limits expandLimits) (expandPlan, error) {
	if text == "" {
		return expandPlan{}, nil
	}
	if len(text) > maximumExpandBytes {
		return expandPlan{}, validation("expand", "expand_budget_exceeded")
	}
	if _, collision := collectionField(root, "expand"); collision {
		return expandPlan{}, validation("expand", "expand_invalid_path")
	}
	if limits.maxDepth <= 0 || limits.maxNodes <= 0 {
		return expandPlan{}, validation("expand", "expand_budget_exceeded")
	}
	reverseIndex, err := buildReverseExpandIndex(catalog)
	if err != nil {
		return expandPlan{}, err
	}

	rootNode := &expandNode{target: root}
	nodeCount := 0
	for _, rawPath := range strings.Split(text, ",") {
		path := strings.TrimSpace(rawPath)
		if path == "" {
			return expandPlan{}, validation("expand", "expand_invalid_path")
		}
		segments := strings.Split(path, ".")
		if len(segments) > limits.maxDepth {
			return expandPlan{}, validation("expand", "expand_budget_exceeded")
		}
		current := rootNode
		for _, segment := range segments {
			if segment == "" || !validExpandSegment(segment) {
				return expandPlan{}, validation("expand", "expand_invalid_path")
			}
			if _, collision := collectionField(current.target, "expand"); collision {
				return expandPlan{}, validation("expand", "expand_invalid_path")
			}
			existing := childExpandNode(current.children, segment)
			if existing != nil {
				current = existing
				continue
			}
			resolved, err := resolveExpandNode(catalog, reverseIndex, current.target, segment)
			if err != nil {
				return expandPlan{}, err
			}
			if resolved == nil {
				return expandPlan{}, validation("expand", "expand_invalid_path")
			}
			current.children = append(current.children, *resolved)
			nodeCount++
			if nodeCount > limits.maxNodes {
				return expandPlan{}, validation("expand", "expand_budget_exceeded")
			}
			current = &current.children[len(current.children)-1]
		}
	}

	sortExpandNodes(rootNode.children)
	return expandPlan{nodes: rootNode.children}, nil
}

func compilePocketBaseExpandPlan(catalog schema.Catalog, root schema.Collection, text string, limits expandLimits) (expandPlan, error) {
	if len(text) > maximumExpandBytes {
		return expandPlan{}, validation("expand", "expand_budget_exceeded")
	}
	valid := make([]string, 0)
	for _, raw := range strings.Split(text, ",") {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		plan, err := compileExpandPlan(catalog, root, path, limits)
		if err == nil && len(plan.nodes) != 0 {
			valid = append(valid, path)
			continue
		}
		var detail *ValidationError
		if errors.As(err, &detail) {
			if len(detail.Details) == 1 && detail.Details[0].Code == "expand_budget_exceeded" {
				return expandPlan{}, err
			}
			continue
		}
		return expandPlan{}, err
	}
	if len(valid) == 0 {
		return expandPlan{}, nil
	}
	return compileExpandPlan(catalog, root, strings.Join(valid, ","), limits)
}

func childExpandNode(nodes []expandNode, name string) *expandNode {
	for index := range nodes {
		if nodes[index].name == name {
			return &nodes[index]
		}
	}
	return nil
}

func sortExpandNodes(nodes []expandNode) {
	sort.Slice(nodes, func(left, right int) bool {
		if nodes[left].name != nodes[right].name {
			return nodes[left].name < nodes[right].name
		}
		if nodes[left].kind != nodes[right].kind {
			return nodes[left].kind < nodes[right].kind
		}
		if nodes[left].target.ID != nodes[right].target.ID {
			return nodes[left].target.ID.String() < nodes[right].target.ID.String()
		}
		return nodes[left].field.Ordinal < nodes[right].field.Ordinal
	})
	for index := range nodes {
		sortExpandNodes(nodes[index].children)
	}
}

func resolveExpandNode(
	catalog schema.Catalog,
	reverseIndex reverseExpandIndex,
	current schema.Collection,
	segment string,
) (*expandNode, error) {
	candidates := make([]expandNode, 0, 2)
	if field, ok := collectionField(current, segment); ok && field.Type == schema.TypeRelation && field.Relation != nil {
		target, ok := catalogCollectionByID(catalog, field.Relation.TargetCollectionID)
		if !ok {
			return nil, schema.ErrConflict
		}
		candidates = append(candidates, expandNode{
			name: field.Name, kind: expandNodeForward, field: field, target: target,
		})
	}
	candidates = append(candidates, reverseIndex[current.ID][segment]...)
	if len(candidates) == 0 {
		return nil, nil
	}
	if len(candidates) != 1 {
		return nil, validation("expand", "expand_invalid_path")
	}
	return &candidates[0], nil
}

func buildReverseExpandIndex(catalog schema.Catalog) (reverseExpandIndex, error) {
	type expectedReverse struct {
		targetID uuid.UUID
		node     expandNode
	}

	collectionsByID := make(map[uuid.UUID]schema.Collection, len(catalog.Collections))
	for key, collection := range catalog.Collections {
		if key != collection.Name || collection.ID == uuid.Nil || collection.TableName != collection.Name ||
			!validExpandCatalogName(collection.Name) {
			return nil, schema.ErrConflict
		}
		if _, duplicate := collectionsByID[collection.ID]; duplicate {
			return nil, schema.ErrConflict
		}
		collectionsByID[collection.ID] = collection
	}

	expected := make(map[schema.RelationRef]expectedReverse)
	fieldIDs := make(map[uuid.UUID]struct{})
	for _, source := range catalog.Collections {
		fieldNames := make(map[string]struct{}, len(source.Fields))
		for ordinal, field := range source.Fields {
			if field.ID == uuid.Nil || field.Ordinal != ordinal ||
				!validExpandCatalogName(field.Name) || !supportedExpandFieldType(field.Type) {
				return nil, schema.ErrConflict
			}
			if _, duplicate := fieldIDs[field.ID]; duplicate {
				return nil, schema.ErrConflict
			}
			fieldIDs[field.ID] = struct{}{}
			if _, duplicate := fieldNames[field.Name]; duplicate {
				return nil, schema.ErrConflict
			}
			fieldNames[field.Name] = struct{}{}
			if field.Type != schema.TypeRelation {
				if field.Relation != nil {
					return nil, schema.ErrConflict
				}
				continue
			}
			if field.Relation == nil {
				return nil, schema.ErrConflict
			}
			if schema.ValidateRelationSpec("field", *field.Relation) != nil ||
				field.Required != (field.Relation.MinSelect > 0) || !field.Indexed || len(field.Default) != 0 ||
				(field.Unique && field.Relation.Cardinality == schema.RelationMultiple) {
				return nil, schema.ErrConflict
			}
			switch field.Relation.Storage.Kind {
			case schema.RelationStorageInlineFKV1:
				if field.Relation.Storage.Name != field.ColumnName {
					return nil, schema.ErrConflict
				}
			case schema.RelationStorageEdgeTableV1:
				if field.Relation.Storage.Name != schema.NewRelationPhysicalNames(source.TableName, field.ColumnName).MultiTable {
					return nil, schema.ErrConflict
				}
			default:
				return nil, schema.ErrConflict
			}
			if _, ok := collectionsByID[field.Relation.TargetCollectionID]; !ok {
				return nil, schema.ErrConflict
			}
			name := source.Name + "_via_" + field.Name
			if !validExpandSegment(name) {
				return nil, schema.ErrConflict
			}
			ref := schema.RelationRef{
				SourceCollectionID: source.ID, SourceFieldID: field.ID, Spec: *field.Relation,
			}
			if _, duplicate := expected[ref]; duplicate {
				return nil, schema.ErrConflict
			}
			expected[ref] = expectedReverse{
				targetID: field.Relation.TargetCollectionID,
				node: expandNode{
					name: name, kind: expandNodeReverse, field: field, target: source,
				},
			}
		}
	}

	index := make(reverseExpandIndex)
	seen := make(map[schema.RelationRef]struct{}, len(expected))
	for targetID, incoming := range catalog.Incoming {
		if _, ok := collectionsByID[targetID]; !ok || len(incoming) == 0 {
			return nil, schema.ErrConflict
		}
		for _, ref := range incoming {
			entry, ok := expected[ref]
			if !ok || entry.targetID != targetID {
				return nil, schema.ErrConflict
			}
			if _, duplicate := seen[ref]; duplicate {
				return nil, schema.ErrConflict
			}
			seen[ref] = struct{}{}
			if index[targetID] == nil {
				index[targetID] = make(map[string][]expandNode)
			}
			name := entry.node.name
			index[targetID][name] = append(index[targetID][name], entry.node)
		}
	}
	if len(seen) != len(expected) {
		return nil, schema.ErrConflict
	}
	for _, byName := range index {
		for name := range byName {
			nodes := byName[name]
			sortExpandNodes(nodes)
			byName[name] = nodes
		}
	}
	return index, nil
}

func validExpandCatalogName(name string) bool {
	if len(name) == 0 || len(name) > 48 || !validExpandSegment(name) || strings.HasPrefix(name, "pgbase_") {
		return false
	}
	switch name {
	case systemFieldID, systemFieldCreatedAt, systemFieldUpdatedAt,
		"ctid", "xmin", "xmax", "cmin", "cmax", "tableoid", "true", "false", "null":
		return false
	default:
		return true
	}
}

func supportedExpandFieldType(fieldType schema.FieldType) bool {
	switch fieldType {
	case schema.TypeBoolean, schema.TypeInteger, schema.TypeBigInt, schema.TypeNumeric, schema.TypeText,
		schema.TypeEditor, schema.TypeGeoPoint, schema.TypeAutoDate, schema.TypePassword,
		schema.TypeTimestampTZ, schema.TypeUUID, schema.TypeJSONB, schema.TypeRelation, schema.TypeFile:
		return true
	default:
		return false
	}
}

func validExpandSegment(segment string) bool {
	if len(segment) == 0 || len(segment) > 101 {
		return false
	}
	for index := 0; index < len(segment); index++ {
		current := segment[index]
		switch {
		case current >= 'a' && current <= 'z':
		case current >= '0' && current <= '9':
			if index == 0 {
				return false
			}
		case current == '_':
		default:
			return false
		}
	}
	return true
}

func collectionField(collection schema.Collection, name string) (schema.Field, bool) {
	for _, field := range collection.Fields {
		if field.Name == name {
			return field, true
		}
	}
	return schema.Field{}, false
}

func catalogCollectionByID(catalog schema.Catalog, id uuid.UUID) (schema.Collection, bool) {
	if id == uuid.Nil {
		return schema.Collection{}, false
	}
	var found schema.Collection
	matches := 0
	for name, collection := range catalog.Collections {
		if name != collection.Name || collection.ID != id {
			continue
		}
		found = collection
		matches++
	}
	return found, matches == 1
}

func expandBudgetError() error { return validation("expand", "expand_budget_exceeded") }
