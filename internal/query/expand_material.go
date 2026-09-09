package query

import (
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

type materializedRecord struct {
	root   map[string]any
	expand map[string]materializedExpandValue
}

type materializedExpandValue struct {
	single *materializedRecord
	many   []*materializedRecord
	plural bool
}

type expandLevelJob struct {
	node    *expandNode
	parents []*materializedRecord
}

func (s *Service) executeMaterializedExpandPlan(
	ctx context.Context,
	tx pgx.Tx,
	catalog schema.Catalog,
	rootCollection schema.Collection,
	roots []map[string]any,
	plan expandPlan,
	principal Principal,
	usage *expansionUsage,
) ([]ExpandedRecord, error) {
	materials := make([]*materializedRecord, len(roots))
	for index, root := range roots {
		if err := usage.takeMaterializedRecord(root); err != nil {
			return nil, err
		}
		materials[index] = &materializedRecord{root: cloneRecord(root)}
	}
	if len(plan.nodes) != 0 && len(materials) != 0 {
		jobs := make([]expandLevelJob, 0, len(plan.nodes))
		for index := range plan.nodes {
			jobs = append(jobs, expandLevelJob{node: &plan.nodes[index], parents: materials})
		}
		for len(jobs) != 0 {
			next := make([]expandLevelJob, 0)
			for _, job := range jobs {
				var targets []*materializedRecord
				var err error
				switch job.node.kind {
				case expandNodeForward:
					targets, err = s.executeForwardExpandNode(ctx, tx, catalog, job, principal, usage)
				case expandNodeReverse:
					targets, err = s.executeReverseExpandNode(ctx, tx, catalog, job, principal, usage)
				default:
					err = schema.ErrConflict
				}
				if err != nil {
					return nil, err
				}
				if len(targets) == 0 {
					continue
				}
				for index := range job.node.children {
					next = append(next, expandLevelJob{node: &job.node.children[index], parents: targets})
				}
			}
			jobs = next
		}
	}
	hideMaterializedFields(rootCollection, plan.nodes, principal, materials)

	return materializedRecordsToExpanded(materials, usage)
}

func materializedRecordsToExpanded(materials []*materializedRecord, usage *expansionUsage) ([]ExpandedRecord, error) {
	measurement := newMaterializedMeasurement(usage.limits.maxBytes)
	expanded := make([]ExpandedRecord, len(materials))
	pageBytes := 0
	oversizedSuffix := false
	for index, material := range materials {
		expanded[index] = ExpandedRecord{Root: cloneRecord(material.root), querySteps: usage.querySteps}
		if oversizedSuffix {
			expanded[index].oversized = true
			continue
		}
		size, err := measurement.recordSize(material)
		if err != nil {
			return nil, err
		}
		maximumJSONBytes := usage.limits.maxBytes - 1
		tooLarge := maximumJSONBytes < 0 || size > maximumJSONBytes
		addition := size
		if usage.pageResult && index != 0 {
			addition++ // comma between array members
		}
		if usage.pageResult && (addition > maximumJSONBytes || pageBytes > maximumJSONBytes-addition) {
			tooLarge = true
		}
		if tooLarge {
			expanded[index].oversized = true
			if usage.pageResult {
				oversizedSuffix = true
			}
			continue
		}
		publicExpand, err := flattenMaterializedExpand(material.expand)
		if err != nil {
			return nil, err
		}
		expanded[index].Expand = publicExpand
		if usage.pageResult {
			pageBytes += addition
		}
	}
	return expanded, nil
}

func (s *Service) executeForwardExpandNode(
	ctx context.Context,
	tx pgx.Tx,
	catalog schema.Catalog,
	job expandLevelJob,
	principal Principal,
	usage *expansionUsage,
) ([]*materializedRecord, error) {
	if len(job.parents) == 0 {
		return nil, nil
	}

	references := make([]relationReferences, len(job.parents))
	allIDs := make([]uuid.UUID, 0)
	for index, parent := range job.parents {
		reference, err := relationIDs(job.node.field, parent.root)
		if err != nil {
			return nil, err
		}
		references[index] = reference
		allIDs = append(allIDs, reference.ids...)
	}
	ids := uniqueSortedIDs(allIDs)
	targetRecords, err := s.fetchExpansionTargets(ctx, tx, catalog, job.node.target, ids, principal, usage)
	if err != nil {
		return nil, err
	}
	if err := usage.takeRows(len(targetRecords)); err != nil {
		return nil, err
	}
	targets := make(map[uuid.UUID]*materializedRecord, len(targetRecords))
	for id, record := range targetRecords {
		targets[id] = &materializedRecord{root: record}
	}

	for index, reference := range references {
		parent := job.parents[index]
		if !reference.present {
			continue
		}
		if job.node.field.Relation.Cardinality == schema.RelationSingle {
			if len(reference.ids) == 1 {
				if target, visible := targets[reference.ids[0]]; visible {
					parent.attach(job.node.name, materializedExpandValue{single: target})
				}
			}
			continue
		}
		visible := make([]*materializedRecord, 0, len(reference.ids))
		for _, id := range reference.ids {
			if target, ok := targets[id]; ok {
				visible = append(visible, target)
			}
		}
		parent.attach(job.node.name, materializedExpandValue{many: visible, plural: true})
	}

	ordered := make([]uuid.UUID, 0, len(targets))
	for id := range targets {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].String() < ordered[right].String() })
	result := make([]*materializedRecord, len(ordered))
	for index, id := range ordered {
		result[index] = targets[id]
	}
	return result, nil
}

func (r *materializedRecord) attach(name string, value materializedExpandValue) {
	if r.expand == nil {
		r.expand = make(map[string]materializedExpandValue)
	}
	r.expand[name] = value
}

func flattenMaterializedExpand(values map[string]materializedExpandValue) (map[string]any, error) {
	if len(values) == 0 {
		return nil, nil
	}
	result := make(map[string]any, len(values))
	for name, value := range values {
		if value.plural {
			many := make([]map[string]any, len(value.many))
			for index, record := range value.many {
				flattened, err := flattenMaterializedRecord(record)
				if err != nil {
					return nil, err
				}
				many[index] = flattened
			}
			result[name] = many
			continue
		}
		if value.single != nil {
			flattened, err := flattenMaterializedRecord(value.single)
			if err != nil {
				return nil, err
			}
			result[name] = flattened
		}
	}
	return result, nil
}

func flattenMaterializedRecord(record *materializedRecord) (map[string]any, error) {
	if record == nil {
		return nil, errors.New("flatten expanded record: missing target")
	}
	flattened := cloneRecord(record.root)
	expand, err := flattenMaterializedExpand(record.expand)
	if err != nil {
		return nil, err
	}
	if len(expand) != 0 {
		if _, collision := flattened["expand"]; collision {
			return nil, errors.New("flatten expanded record: reserved field collision")
		}
		flattened["expand"] = expand
	}
	return flattened, nil
}
