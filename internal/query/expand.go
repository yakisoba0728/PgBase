package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/recordartifact"
	"github.com/yakisoba0728/PgBase/internal/relation"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type expansionUsage struct {
	limits                              expandLimits
	rows, querySteps, materializedBytes int
	pageResult                          bool
}

func (u *expansionUsage) takeMaterializedRecord(record map[string]any) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	count := len(encoded)
	if count < 0 || count > u.limits.maxBytes || u.materializedBytes > u.limits.maxBytes-count {
		return validation("response", "response_too_large")
	}
	u.materializedBytes += count
	return nil
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

type getResult struct {
	record   Record
	expanded ExpandedRecord
}

func (s *Service) Get(ctx context.Context, request GetRequest) (Record, error) {
	result, err := s.readGet(ctx, request)
	return result.record, err
}

func (s *Service) readGet(ctx context.Context, request GetRequest) (getResult, error) {
	if err := s.ready(); err != nil {
		return getResult{}, err
	}
	if !request.Principal.Valid() {
		return getResult{}, validation("principal", "invalid")
	}
	if request.ID == uuid.Nil {
		return getResult{}, validation("id", "invalid")
	}
	var result getResult
	err := s.inTxCatalog(ctx, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		return s.readGetInTx(ctx, tx, catalog, collection, request, &result)
	}, request.Collection)
	if err != nil {
		return getResult{}, err
	}
	return result, nil
}

func (s *Service) readGetInTx(
	ctx context.Context,
	tx pgx.Tx,
	catalog schema.Catalog,
	collection schema.Collection,
	request GetRequest,
	result *getResult,
) error {
	if err := authorizePrincipal(ctx, tx, collection, request.Principal, "view"); err != nil {
		return err
	}
	projection, err := compileRecordProjection(collection, request.Principal, request.Fields)
	if err != nil {
		return err
	}
	if err := validateSQLViewIdentity(ctx, tx, collection); err != nil {
		return err
	}
	statement := CompileSelectByID(collection, request.ID)
	if request.Principal.Kind == identity.User || request.Principal.Kind == identity.Anonymous {
		rules, _, err := recordaccess.LoadRules(ctx, tx, collection.AccessCollectionID())
		if err != nil {
			return err
		}
		policyTarget, err := sourcePolicyCollection(catalog, collection)
		if err != nil {
			return err
		}
		predicate, args, err := compilePolicyPredicate(rules.View, collection, policyTarget, collection.TableName, request.Principal, len(statement.Args))
		if err != nil {
			return err
		}
		statement.SQL += " AND (" + predicate + ")"
		statement.Args = append(statement.Args, args...)
	}
	root, err := EncodeRecord(collection, tx.QueryRow(ctx, statement.SQL, statement.Args...))
	if err := recordQueryError(err); err != nil {
		return err
	}

	var response map[string]any
	if request.Expand == "" {
		usage := &expansionUsage{limits: s.expandLimits}
		if err := usage.takeQuerySteps(1 + multipleRelationFieldCount(collection)); err != nil {
			return err
		}
		if err := hydrateRelationRecordsBounded(ctx, tx, catalog, collection, []map[string]any{root}, request.Principal, usage); err != nil {
			return err
		}
		if err := validateExpandedRecordSize(ExpandedRecord{Root: root}, usage.limits.maxBytes); err != nil {
			return err
		}
		response = cloneRecord(root)
		hideRecordFields(collection, request.Principal, response)
	} else {
		usage := &expansionUsage{limits: s.expandLimits}
		plan, err := compileExpandPlan(catalog, collection, request.Expand, usage.limits)
		if err != nil {
			return err
		}
		if err := rejectHiddenExpand(request.Principal, plan.nodes); err != nil {
			return err
		}
		if err := usage.takeQuerySteps(1); err != nil {
			return err
		}
		if err := usage.takeQuerySteps(multipleRelationFieldCount(collection)); err != nil {
			return err
		}
		if err := hydrateRelationRecordsBounded(ctx, tx, catalog, collection, []map[string]any{root}, request.Principal, usage); err != nil {
			return err
		}
		expanded, err := s.executeMaterializedExpandPlan(ctx, tx, catalog, collection, []map[string]any{root}, plan, request.Principal, usage)
		if err != nil {
			return err
		}
		result.expanded = expanded[0]
		result.expanded.querySteps = usage.querySteps
		if err := validateExpandedRecordSize(result.expanded, s.expandLimits.maxBytes); err != nil {
			return err
		}
		response, err = result.expanded.Flatten()
		if err != nil {
			return err
		}
	}

	digest, err := recordartifact.SchemaDigest(collection)
	if err != nil {
		return err
	}
	version, validator, present, err := recordartifact.ReadRevision(ctx, tx, collection.ID, request.ID)
	if err != nil {
		return err
	}
	etag, err := recordartifact.ETag(collection.ID, request.ID, digest, root, validator, present)
	if err != nil {
		return err
	}
	result.record = Record{Data: projection.apply(response), ETag: etag, Version: version}
	return nil
}

type listResult struct {
	page       RecordPage
	expanded   []ExpandedRecord
	querySteps int
}

func (s *Service) readList(ctx context.Context, request ListRequest) (result listResult, err error) {
	started := time.Now()
	relationOperation := requestTextAddressesRelation(request.Expand, request.Filter, request.Sort)
	defer s.finishReadOperation(
		ctx, readTelemetryOperation(request.Expand), started, &err, &relationOperation,
	)
	if err := s.ready(); err != nil {
		return listResult{}, err
	}
	if !request.Principal.Valid() {
		return listResult{}, validation("principal", "invalid")
	}
	filterNode, err := parseFilterRequest(request.Filter)
	if err != nil {
		return listResult{}, err
	}
	requestedSort, err := parseSort(request.Sort)
	if err != nil {
		return listResult{}, err
	}
	err = s.inTxCatalog(ctx, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		var operation bool
		var readErr error
		result, operation, readErr = s.readListInTx(ctx, tx, catalog, collection, request, filterNode, requestedSort)
		relationOperation = relationOperation || operation
		return readErr
	}, request.Collection)
	return result, err
}

func (s *Service) readListInTx(
	ctx context.Context,
	tx pgx.Tx,
	catalog schema.Catalog,
	collection schema.Collection,
	request ListRequest,
	filterNode filterNode,
	requestedSort []SortKey,
) (listResult, bool, error) {
	if err := rejectHiddenQueryFields(collection, request.Principal, filterNode, requestedSort); err != nil {
		return listResult{}, false, err
	}
	if err := authorizePrincipal(ctx, tx, collection, request.Principal, "list"); err != nil {
		return listResult{}, false, err
	}
	var err error
	projection, err := compileRecordProjection(collection, request.Principal, request.Fields)
	if err != nil {
		return listResult{}, false, err
	}
	if err := validateSQLViewIdentity(ctx, tx, collection); err != nil {
		return listResult{}, false, err
	}
	var expansion expandPlan
	var usage *expansionUsage
	if request.Expand != "" {
		usage = &expansionUsage{limits: s.expandLimits, pageResult: true}
		expansion, err = compileExpandPlan(catalog, collection, request.Expand, usage.limits)
		if err != nil {
			return listResult{}, false, err
		}
		if err := rejectHiddenExpand(request.Principal, expansion.nodes); err != nil {
			return listResult{}, false, err
		}
	}
	relationOperation := requestAddressesRelationField(collection, filterNode, effectiveSortKeys(requestedSort))
	relationPlan, err := compileRelationListPlan(catalog, collection, filterNode, effectiveSortKeys(requestedSort))
	if err != nil {
		if request.Cursor != "" && validationField(err, "sort") {
			return listResult{}, relationOperation, invalidCursor()
		}
		return listResult{}, relationOperation, err
	}
	if err := rejectHiddenRelationPlan(request.Principal, relationPlan); err != nil {
		return listResult{}, relationOperation, err
	}
	if relationListPlanUsesRelation(relationPlan) {
		if err := bindRelationPolicies(ctx, tx, &relationPlan, request.Principal); err != nil {
			return listResult{}, relationOperation, err
		}
	}
	rootPlan := plannedRootPlan(filterNode, relationPlan)
	rootPlan.catalogVersion, rootPlan.principal = catalog.Version, request.Principal
	rootPlan.policyCollection, err = sourcePolicyCollection(catalog, collection)
	if err != nil {
		return listResult{}, relationOperation, err
	}
	if request.Principal.Kind == identity.User || request.Principal.Kind == identity.Anonymous {
		rules, revision, err := recordaccess.LoadRules(ctx, tx, collection.AccessCollectionID())
		if err != nil {
			return listResult{}, false, err
		}
		if revision == 0 && request.Principal.Kind == identity.User {
			ownerID := request.Principal.ID
			rootPlan.ownerID = &ownerID
		} else {
			rootPlan.rule = &rules.List
			rootPlan.ruleRevision = revision
		}
		rootPlan.outputRuleScope, err = bindOutputRuleScope(ctx, tx, catalog, collection, expansion)
		if err != nil {
			return listResult{}, false, err
		}
	}
	traversal, err := s.resolveReadTraversal(ctx, tx, collection)
	if err != nil {
		return listResult{}, relationOperation, err
	}
	rootRequest := rootPageReadRequest{
		plan: rootPlan, cursorToken: request.Cursor, limit: request.Limit,
		operationTime: traversal.TransactionTime, traversal: traversal,
	}
	if usage == nil {
		if multipleRelationFieldCount(collection) != 0 {
			rootRequest.hydrator = newRootHydrator(tx, catalog, collection, request.Principal)
		} else if collectionHasRelation(collection) {
			rootRequest.hydrate = func(items []map[string]any) error {
				return filterRelationValues(ctx, tx, catalog, collection, items, request.Principal)
			}
		}
	} else {
		rootRequest.charge = usage.takeQuerySteps
		rootRequest.hydrate = func(items []map[string]any) error {
			if len(items) == 0 {
				return nil
			}
			if err := usage.takeQuerySteps(multipleRelationFieldCount(collection)); err != nil {
				return err
			}
			return hydrateRelationRecordsBounded(ctx, tx, catalog, collection, items, request.Principal, usage)
		}
	}
	root, err := s.readRootPageInTx(ctx, tx, rootRequest)
	if err != nil {
		return listResult{}, relationOperation, err
	}
	if usage == nil {
		for _, item := range root.page.Items {
			hideRecordFields(collection, request.Principal, item)
		}
		return listResult{page: projection.applyPage(root.page)}, relationOperation, nil
	}

	expanded, err := s.executeMaterializedExpandPlan(ctx, tx, catalog, collection, root.page.Items, expansion, request.Principal, usage)
	if err != nil {
		return listResult{}, relationOperation, err
	}
	candidates := make([]pageCandidate, len(expanded))
	for index := range expanded {
		candidates[index] = pageCandidate{
			id: root.candidates[index].id, root: root.candidates[index].root, wire: expanded[index],
			cursorValues: root.candidates[index].cursorValues, oversized: expanded[index].oversized,
		}
	}
	assembler := pageAssembler{
		cursors: s.cursors, order: root.order, expiresAt: root.expiresAt, maximumBytes: s.expandLimits.maxBytes,
		cursorV5: root.cursorV5,
	}
	keep, cursor, err := assembler.finish(candidates, root.page.NextCursor != "")
	if err != nil {
		return listResult{}, relationOperation, err
	}
	items := make([]map[string]any, keep)
	for index := range keep {
		items[index], err = expanded[index].Flatten()
		if err != nil {
			return listResult{}, relationOperation, err
		}
	}
	return listResult{
		page:     projection.applyPage(RecordPage{Items: items, NextCursor: cursor}),
		expanded: expanded[:keep], querySteps: usage.querySteps,
	}, relationOperation, nil
}

func collectionHasRelation(collection schema.Collection) bool {
	for _, field := range collection.Fields {
		if field.Type == schema.TypeRelation {
			return true
		}
	}
	return false
}

func authorizePrincipal(ctx context.Context, tx pgx.Tx, collection schema.Collection, principal Principal, action string) error {
	if principal.Kind == identity.Administrator {
		return nil
	}
	return recordaccess.AuthorizeSubject(ctx, tx, collection.AccessCollectionID(), principal, action)
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

type relationReferences struct {
	present bool
	ids     []uuid.UUID
}

func relationIDs(field schema.Field, record map[string]any) (relationReferences, error) {
	value, present := record[field.Name]
	if !present {
		// Treating a missing relation as null would hide catalog/storage drift.
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
	principal Principal,
	usage *expansionUsage,
) (map[uuid.UUID]map[string]any, error) {
	targets := make(map[uuid.UUID]map[string]any, len(ids))
	if len(ids) == 0 {
		return targets, nil
	}
	if err := recordaccess.AuthorizeSubject(ctx, tx, collection.AccessCollectionID(), principal, "view"); err != nil {
		return nil, err
	}
	selectedMultiFields := multipleRelationFieldNames(collection)
	hydrationSteps := len(selectedMultiFields)
	requiredSteps := 1 + hydrationSteps
	if err := usage.takeQuerySteps(requiredSteps); err != nil {
		return nil, err
	}
	statement := compileExpandTargetSelect(collection, ids)
	if principal.Kind == identity.User || principal.Kind == identity.Anonymous {
		rules, _, err := recordaccess.LoadRules(ctx, tx, collection.AccessCollectionID())
		if err != nil {
			return nil, err
		}
		policyTarget, err := sourcePolicyCollection(catalog, collection)
		if err != nil {
			return nil, err
		}
		predicate, args, err := compilePolicyPredicate(rules.View, collection, policyTarget, collection.TableName, principal, len(statement.Args))
		if err != nil {
			return nil, err
		}
		statement.SQL = injectBeforeOrder(statement.SQL, " AND ("+predicate+")")
		statement.Args = append(statement.Args, args...)
	}
	rows, err := tx.Query(ctx, statement.SQL, statement.Args...)
	if err != nil {
		return nil, expansionTargetFailure(err)
	}
	records := make([]map[string]any, 0, len(ids))
	for rows.Next() {
		record, err := EncodeRecord(collection, rows)
		if err != nil {
			rows.Close()
			return nil, expansionTargetFailure(err)
		}
		if err := usage.takeMaterializedRecord(record); err != nil {
			rows.Close()
			return nil, err
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
		targets[id] = record
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

func compileExpandTargetSelect(collection schema.Collection, ids []uuid.UUID) Statement {
	return Statement{
		SQL: "SELECT " + selectedColumns(collection) + " FROM " + quotedTable(collection) +
			" WHERE " + quoted("id") + " = any($1::uuid[]) ORDER BY " + quoted("id"),
		Args: []any{ids},
	}
}

func multipleRelationFieldNames(collection schema.Collection) []string {
	names := make([]string, 0)
	for _, field := range fieldsByOrdinal(collection) {
		if isMultipleRelation(field) {
			names = append(names, field.Name)
		}
	}
	return names
}

func multipleRelationFieldCount(collection schema.Collection) int {
	return len(multipleRelationFieldNames(collection))
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
