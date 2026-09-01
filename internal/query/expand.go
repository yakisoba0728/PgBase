package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/webisnoteasy/PgBase/internal/relation"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

type expansionUsage struct {
	limits     expandLimits
	rows       int
	querySteps int
	pageResult bool
}

func (u *expansionUsage) takeRows(count int) error {
	if count < 0 || u.rows > u.limits.maxRows-count {
		return expandBudgetError()
	}
	u.rows += count
	return nil
}

func (u *expansionUsage) takeQuerySteps(count int) error {
	if count < 0 || u.querySteps > u.limits.maxQuerySteps-count {
		return expandBudgetError()
	}
	u.querySteps += count
	return nil
}

func (s *Service) GetExpanded(ctx context.Context, request GetRequest) (ExpandedRecord, error) {
	if err := s.ready(); err != nil {
		return ExpandedRecord{}, err
	}
	if s.policy == nil || request.ID == uuid.Nil {
		return ExpandedRecord{}, errors.New("record expansion is unavailable")
	}
	var result ExpandedRecord
	err := s.inTxCatalog(ctx, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		usage := &expansionUsage{limits: s.expandLimits}
		plan, err := compileExpandPlan(catalog, collection, request.Expand, usage.limits)
		if err != nil {
			return err
		}
		if err := usage.takeQuerySteps(1); err != nil {
			return err
		}
		statement := CompileSelectByID(collection, request.ID)
		root, err := EncodeRecord(collection, tx.QueryRow(ctx, statement.SQL, statement.Args...))
		if err := recordQueryError(err); err != nil {
			return err
		}
		if err := usage.takeQuerySteps(multipleRelationFieldCount(collection)); err != nil {
			return err
		}
		if err := hydrateRelationRecordsBounded(ctx, tx, catalog, collection, []map[string]any{root}, usage); err != nil {
			return err
		}
		expanded, err := s.executeExpandPlan(ctx, tx, catalog, []map[string]any{root}, ExpandRequest{
			Principal: request.Principal,
			Expand:    request.Expand,
		}, plan, usage)
		if err != nil {
			return err
		}
		result = expanded[0]
		result.querySteps = usage.querySteps
		return validateExpandedRecordSize(result, s.expandLimits.maxBytes)
	}, request.Collection)
	return result, err
}

func (s *Service) ListExpanded(ctx context.Context, request ListRequest) (ExpandedRecordPage, error) {
	if err := s.ready(); err != nil {
		return ExpandedRecordPage{}, err
	}
	if s.policy == nil {
		return ExpandedRecordPage{}, errors.New("record expansion is unavailable")
	}
	filterNode, err := parseFilterRequest(request.Filter)
	if err != nil {
		return ExpandedRecordPage{}, err
	}
	requestedSort, err := parseSort(request.Sort)
	if err != nil {
		return ExpandedRecordPage{}, err
	}
	page := ExpandedRecordPage{Items: []ExpandedRecord{}}
	err = s.inTxCatalog(ctx, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		usage := &expansionUsage{limits: s.expandLimits, pageResult: true}
		plan, err := compileExpandPlan(catalog, collection, request.Expand, usage.limits)
		if err != nil {
			return err
		}
		relationPlan, err := compileRelationListPlan(
			ctx,
			catalog,
			collection,
			request.Principal,
			s.policy,
			filterNode,
			effectiveSortKeys(requestedSort),
		)
		if err != nil {
			return err
		}
		root, err := s.readRelationRootPageInTx(ctx, tx, catalog, collection, relationRootPageReadRequest{
			plan: relationPlan, cursorToken: request.Cursor, limit: request.Limit,
			filter: filterNode, requestedSort: requestedSort, operationTime: s.now().UTC(),
			charge: usage.takeQuerySteps,
			hydrate: func(items []map[string]any) error {
				if len(items) == 0 {
					return nil
				}
				if err := usage.takeQuerySteps(multipleRelationFieldCount(collection)); err != nil {
					return err
				}
				return hydrateRelationRecordsBounded(ctx, tx, catalog, collection, items, usage)
			},
		})
		if err != nil {
			return err
		}

		expanded, err := s.executeExpandPlan(ctx, tx, catalog, root.page.Items, ExpandRequest{
			Principal: request.Principal,
			Expand:    request.Expand,
		}, plan, usage)
		if err != nil {
			return err
		}
		page = ExpandedRecordPage{Items: expanded, NextCursor: root.page.NextCursor, querySteps: usage.querySteps}
		return s.finalizeExpandedPage(&page, collection.ID, root)
	}, request.Collection)
	return page, err
}

// finalizeExpandedPage measures the exact JSON object the API emits the way
// finalizeRecordPage does. Item encodings never change while the tail is
// trimmed, so they are measured once and the running total is decremented per
// dropped item; only the envelope is re-encoded per attempt, because its size
// follows the continuation cursor, which follows the anchor record.
// Re-marshalling the whole page on every attempt was quadratic in the page
// size for a bound that is exceeded by a few kilobytes.
func (s *Service) finalizeExpandedPage(
	page *ExpandedRecordPage,
	collectionID uuid.UUID,
	root rootPageReadResult,
) error {
	// A record too large to represent on its own takes the whole tail with it
	// before anything is measured, because ExpandedRecord.MarshalJSON refuses
	// to encode one. Trimming to the first such record leaves none behind, so
	// this runs at most once.
	for index := range page.Items {
		if !page.Items[index].oversized {
			continue
		}
		if index == 0 {
			return validation("response", "response_too_large")
		}
		if err := s.trimExpandedPage(page, index, collectionID, root); err != nil {
			return err
		}
		break
	}
	itemBytes := make([]int, len(page.Items))
	measured := 0
	for index := range page.Items {
		encoded, err := json.Marshal(page.Items[index])
		if err != nil {
			return fmt.Errorf("measure expanded record page response: %w", err)
		}
		itemBytes[index] = len(encoded)
		measured += len(encoded)
	}
	for {
		// An empty page encodes the exact envelope this page will emit; the
		// items add their own bytes plus one separating comma each.
		envelope, err := json.Marshal(ExpandedRecordPage{Items: []ExpandedRecord{}, NextCursor: page.NextCursor})
		if err != nil {
			return fmt.Errorf("measure expanded record page response: %w", err)
		}
		responseBytes := len(envelope) + measured
		if len(page.Items) > 0 {
			responseBytes += len(page.Items) - 1
		}
		if responseBytes+1 <= s.expandLimits.maxBytes {
			return nil
		}
		if len(page.Items) <= 1 {
			return validation("response", "response_too_large")
		}
		measured -= itemBytes[len(page.Items)-1]
		if err := s.trimExpandedPage(page, len(page.Items)-1, collectionID, root); err != nil {
			return err
		}
	}
}

func (s *Service) trimExpandedPage(
	page *ExpandedRecordPage,
	keep int,
	collectionID uuid.UUID,
	root rootPageReadResult,
) error {
	if keep <= 0 || keep > len(page.Items) {
		return validation("response", "response_too_large")
	}
	page.Items = page.Items[:keep]
	anchor := page.Items[len(page.Items)-1].Root
	anchorID, err := canonicalRecordID(anchor)
	if err != nil {
		return errors.New("encode page cursor: invalid record id")
	}
	values, ok := root.cursorValuesByRoot[anchorID.String()]
	if !ok {
		values, err = recordSortValues(root.sortKeys, anchor)
		if err != nil {
			return err
		}
	} else {
		values = cloneCursorValues(values)
	}
	page.NextCursor, err = s.cursors.Encode(PageCursor{
		Version: cursorVersion, CollectionID: collectionID,
		Sort: root.sortKeys, Values: values, AnchorID: anchorID,
		PrivateValues: root.privateCursor, ExpiresAt: root.expiresAt,
	})
	if err != nil {
		return err
	}
	return nil
}

// ExpandRecords is the narrow executor seam for callers such as recordops
// that already own a transaction, pinned catalog, root read, revision, and
// ETag lifecycle. Root maps are copied and are never mutated.
func (s *Service) ExpandRecords(
	ctx context.Context,
	tx pgx.Tx,
	catalog schema.Catalog,
	collection schema.Collection,
	roots []map[string]any,
	request ExpandRequest,
) ([]ExpandedRecord, error) {
	canonical, err := s.validateExpandExecutor(tx, catalog, collection)
	if err != nil {
		return nil, err
	}
	usage := &expansionUsage{limits: s.expandLimits}
	plan, err := compileExpandPlan(catalog, canonical, request.Expand, usage.limits)
	if err != nil {
		return nil, err
	}
	return s.finishExpandedRecords(ctx, tx, catalog, roots, request, plan, usage)
}

// ExpandBaseRecords is the recordops integration seam. Its inputs are base
// rows produced by EncodeRecord (multi relations are still [] placeholders).
// It accounts for the caller's root SELECT, performs bounded root hydration in
// the same pinned transaction, then expands and measures the flat response.
func (s *Service) ExpandBaseRecords(
	ctx context.Context,
	tx pgx.Tx,
	catalog schema.Catalog,
	collection schema.Collection,
	baseRoots []map[string]any,
	request ExpandRequest,
) ([]ExpandedRecord, error) {
	canonical, err := s.validateExpandExecutor(tx, catalog, collection)
	if err != nil {
		return nil, err
	}
	usage := &expansionUsage{limits: s.expandLimits}
	plan, err := compileExpandPlan(catalog, canonical, request.Expand, usage.limits)
	if err != nil {
		return nil, err
	}
	if err := usage.takeQuerySteps(1); err != nil {
		return nil, err
	}
	roots := make([]map[string]any, len(baseRoots))
	for index, root := range baseRoots {
		if _, err := canonicalRecordID(root); err != nil {
			return nil, schema.ErrConflict
		}
		roots[index] = cloneRecord(root)
	}
	if len(roots) != 0 {
		if err := usage.takeQuerySteps(multipleRelationFieldCount(canonical)); err != nil {
			return nil, err
		}
		if err := hydrateRelationRecordsBounded(ctx, tx, catalog, canonical, roots, usage); err != nil {
			return nil, err
		}
	}
	return s.finishExpandedRecords(ctx, tx, catalog, roots, request, plan, usage)
}

func (s *Service) validateExpandExecutor(
	tx pgx.Tx,
	catalog schema.Catalog,
	collection schema.Collection,
) (schema.Collection, error) {
	if s == nil || tx == nil || s.policy == nil {
		return schema.Collection{}, errors.New("record expansion is unavailable")
	}
	canonical, ok := catalog.Collections[collection.Name]
	if !ok || canonical.ID != collection.ID || canonical.TableName != collection.TableName {
		return schema.Collection{}, schema.ErrConflict
	}
	return canonical, nil
}

func (s *Service) finishExpandedRecords(
	ctx context.Context,
	tx pgx.Tx,
	catalog schema.Catalog,
	roots []map[string]any,
	request ExpandRequest,
	plan expandPlan,
	usage *expansionUsage,
) ([]ExpandedRecord, error) {
	for _, root := range roots {
		if _, err := canonicalRecordID(root); err != nil {
			return nil, schema.ErrConflict
		}
	}
	expanded, err := s.executeExpandPlan(ctx, tx, catalog, roots, request, plan, usage)
	if err != nil {
		return nil, err
	}
	for _, record := range expanded {
		if err := validateExpandedRecordSize(record, s.expandLimits.maxBytes); err != nil {
			return nil, err
		}
	}
	return expanded, nil
}

func validateExpandedRecordSize(record ExpandedRecord, maximumBytes int) error {
	if record.oversized {
		return validation("response", "response_too_large")
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("measure expanded record response: %w", err)
	}
	if len(encoded)+1 > maximumBytes {
		return validation("response", "response_too_large")
	}
	return nil
}

func (s *Service) executeExpandPlan(
	ctx context.Context,
	tx pgx.Tx,
	catalog schema.Catalog,
	roots []map[string]any,
	request ExpandRequest,
	plan expandPlan,
	usage *expansionUsage,
) ([]ExpandedRecord, error) {
	return s.executeMaterializedExpandPlan(ctx, tx, catalog, roots, request, plan, usage)
}

type relationReferences struct {
	present bool
	ids     []uuid.UUID
}

func relationIDs(field schema.Field, record map[string]any) (relationReferences, error) {
	value, present := record[field.Name]
	if !present {
		// ExpandRecords accepts fully materialized root records. Silently treating
		// an absent requested field as null would turn catalog/storage drift (or an
		// accidentally projected root) into partial, misleading output.
		return relationReferences{}, relationHydrationFailure(relation.ErrInvalidStoredValue)
	}
	if field.Relation == nil {
		return relationReferences{}, relationHydrationFailure(relation.ErrInvalidStoredValue)
	}
	if field.Relation.Cardinality == schema.RelationSingle {
		if value == nil {
			return relationReferences{present: true}, nil
		}
		text, ok := value.(string)
		if !ok {
			return relationReferences{}, relationHydrationFailure(relation.ErrInvalidStoredValue)
		}
		id, err := uuid.Parse(text)
		if err != nil || id == uuid.Nil || id.String() != text {
			return relationReferences{}, relationHydrationFailure(relation.ErrInvalidStoredValue)
		}
		return relationReferences{present: true, ids: []uuid.UUID{id}}, nil
	}
	if field.Relation.Cardinality != schema.RelationMultiple {
		return relationReferences{}, relationHydrationFailure(relation.ErrInvalidStoredValue)
	}
	var values []string
	switch typed := value.(type) {
	case []string:
		values = typed
	case []any:
		values = make([]string, len(typed))
		for index, member := range typed {
			text, ok := member.(string)
			if !ok {
				return relationReferences{}, relationHydrationFailure(relation.ErrInvalidStoredValue)
			}
			values[index] = text
		}
	default:
		return relationReferences{}, relationHydrationFailure(relation.ErrInvalidStoredValue)
	}
	ids := make([]uuid.UUID, len(values))
	seen := make(map[uuid.UUID]struct{}, len(values))
	for index, text := range values {
		id, err := uuid.Parse(text)
		if err != nil || id == uuid.Nil || id.String() != text {
			return relationReferences{}, relationHydrationFailure(relation.ErrInvalidStoredValue)
		}
		if _, duplicate := seen[id]; duplicate {
			return relationReferences{}, relationHydrationFailure(relation.ErrInvalidStoredValue)
		}
		seen[id] = struct{}{}
		ids[index] = id
	}
	return relationReferences{present: true, ids: ids}, nil
}

func (s *Service) fetchExpansionTargets(
	ctx context.Context,
	tx pgx.Tx,
	catalog schema.Catalog,
	collection schema.Collection,
	ids []uuid.UUID,
	predicate Predicate,
	fields FieldSet,
	usage *expansionUsage,
) (map[uuid.UUID]map[string]any, error) {
	targets := make(map[uuid.UUID]map[string]any, len(ids))
	if len(ids) == 0 {
		return targets, nil
	}
	projectedCollection, selectedMultiFields := expansionTargetProjection(collection, fields)
	hydrationSteps := len(selectedMultiFields)
	requiredSteps := 1 + hydrationSteps
	if err := usage.takeQuerySteps(requiredSteps); err != nil {
		return nil, err
	}
	statement, err := compileExpandTargetSelect(projectedCollection, ids, predicate)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, statement.SQL, statement.Args...)
	if err != nil {
		return nil, expansionTargetFailure(err)
	}
	records := make([]map[string]any, 0, len(ids))
	for rows.Next() {
		record, err := EncodeRecord(projectedCollection, rows)
		if err != nil {
			rows.Close()
			return nil, expansionTargetFailure(err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, expansionTargetFailure(err)
	}
	rows.Close()
	if hydrationSteps != 0 {
		bindings, err := relation.Bind(catalog, collection)
		if err != nil {
			return nil, relationHydrationFailure(err)
		}
		sourceIDs, recordsByID, err := indexRelationRecords(records)
		if err != nil {
			return nil, err
		}
		remaining := usage.limits.maxRows - usage.rows
		if remaining < 0 {
			return nil, expandBudgetError()
		}
		used, err := bindings.HydrateFieldsBounded(ctx, tx, sourceIDs, recordsByID, selectedMultiFields, remaining)
		if errors.Is(err, relation.ErrHydrationBudgetExceeded) {
			return nil, expandBudgetError()
		}
		if err != nil {
			return nil, relationHydrationFailure(err)
		}
		if err := usage.takeRows(used); err != nil {
			return nil, err
		}
	}
	for _, record := range records {
		id, err := canonicalRecordID(record)
		if err != nil {
			return nil, err
		}
		if _, duplicate := targets[id]; duplicate {
			return nil, errors.New("read expanded targets: duplicate record id")
		}
		targets[id] = projectRecord(collection, record, fields)
	}
	return targets, nil
}

func expansionTargetFailure(err error) error {
	if isCatalogDriftFailure(err) {
		return schema.ErrConflict
	}
	return fmt.Errorf("read expanded targets: %w", err)
}

func isCatalogDriftFailure(err error) bool {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "42P01", // undefined_table
			"42703", // undefined_column
			"42804": // datatype_mismatch
			return true
		}
	}
	var scanError *RecordScanError
	return errors.As(err, &scanError)
}

func compileExpandTargetSelect(collection schema.Collection, ids []uuid.UUID, predicate Predicate) (Statement, error) {
	args := []any{ids}
	condition := quoted("id") + " = any($1::uuid[])"
	switch predicate.kind {
	case predicateAllowAll:
	case predicateDenyAll:
		condition += " and false"
	case predicateRecordIDs:
		args = append(args, predicate.ids)
		condition += " and " + quoted("id") + " = any($2::uuid[])"
	default:
		return Statement{}, ErrProjectionDenied
	}
	return Statement{
		SQL: "SELECT " + selectedColumns(collection) + " FROM " + quotedTable(collection) +
			" WHERE " + condition + " ORDER BY " + quoted("id"),
		Args: args,
	}, nil
}

func projectRecord(collection schema.Collection, record map[string]any, fields FieldSet) map[string]any {
	if fields.all {
		return cloneRecord(record)
	}
	projected := make(map[string]any, len(fields.fields)+3)
	for _, name := range []string{systemFieldID, systemFieldCreatedAt, systemFieldUpdatedAt} {
		if value, present := record[name]; present {
			projected[name] = cloneJSONValue(value)
		}
	}
	for _, field := range collection.Fields {
		if _, selected := fields.fields[field.Name]; selected {
			projected[field.Name] = cloneJSONValue(record[field.Name])
		}
	}
	return projected
}

func multipleRelationFieldCount(collection schema.Collection) int {
	count := 0
	for _, field := range collection.Fields {
		if isMultipleRelation(field) {
			count++
		}
	}
	return count
}

// expansionTargetProjection limits both the base SELECT and junction
// hydration to fields authorized by ProjectionPolicy. The returned field
// names are in catalog ordinal order, matching HydrateFieldsBounded's contract.
func expansionTargetProjection(collection schema.Collection, fields FieldSet) (schema.Collection, []string) {
	projected := collection
	projected.Fields = make([]schema.Field, 0, len(collection.Fields))
	selectedMulti := make([]string, 0)
	for _, field := range fieldsByOrdinal(collection) {
		if !fieldSetAllows(fields, field.Name) {
			continue
		}
		projected.Fields = append(projected.Fields, field)
		if isMultipleRelation(field) {
			selectedMulti = append(selectedMulti, field.Name)
		}
	}
	return projected, selectedMulti
}

func visibleCandidateIDs(ids []uuid.UUID, predicate Predicate) []uuid.UUID {
	switch predicate.kind {
	case predicateAllowAll:
		return ids
	case predicateDenyAll:
		return nil
	case predicateRecordIDs:
		allowed := make(map[uuid.UUID]struct{}, len(predicate.ids))
		for _, id := range predicate.ids {
			allowed[id] = struct{}{}
		}
		visible := make([]uuid.UUID, 0, len(ids))
		for _, id := range ids {
			if _, ok := allowed[id]; ok {
				visible = append(visible, id)
			}
		}
		return visible
	default:
		return nil
	}
}

func uniqueSortedIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		seen[id] = struct{}{}
	}
	result := make([]uuid.UUID, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].String() < result[right].String() })
	return result
}

func canonicalRecordID(record map[string]any) (uuid.UUID, error) {
	text, ok := record[systemFieldID].(string)
	if !ok {
		return uuid.Nil, errors.New("read expanded targets: invalid record id")
	}
	id, err := uuid.Parse(text)
	if err != nil || id == uuid.Nil || id.String() != text {
		return uuid.Nil, errors.New("read expanded targets: invalid record id")
	}
	return id, nil
}

func cloneRecord(record map[string]any) map[string]any {
	if record == nil {
		return nil
	}
	cloned := make(map[string]any, len(record))
	for name, value := range record {
		cloned[name] = cloneJSONValue(value)
	}
	return cloned
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case json.RawMessage:
		return append(json.RawMessage(nil), typed...)
	case []string:
		return append([]string(nil), typed...)
	case []any:
		cloned := make([]any, len(typed))
		for index := range typed {
			cloned[index] = cloneJSONValue(typed[index])
		}
		return cloned
	case []map[string]any:
		cloned := make([]map[string]any, len(typed))
		for index := range typed {
			cloned[index] = cloneRecord(typed[index])
		}
		return cloned
	case map[string]any:
		return cloneRecord(typed)
	default:
		return typed
	}
}
