package query

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

// ListRelationAware executes scalar and relation-aware filters/sorts through
// one pinned catalog snapshot. Scalar-only requests deliberately delegate to
// the original root-page kernel so their SQL and cursor representation remain
// byte-for-byte compatible.
func (s *Service) ListRelationAware(ctx context.Context, request RelationListRequest) (RecordPage, error) {
	page, _, err := s.ListRelationAwareUsage(ctx, request)
	return page, err
}

// ListRelationAwareUsage behaves exactly like ListRelationAware and also
// reports whether the request addressed a relation field. Callers that record
// relation telemetry cannot derive that from the request text: a bare
// `sort=owner` names a root relation field and joins to its target, and only
// the pinned catalog separates it from a scalar column of the same name. The
// flag follows the parsed request rather than the compiled plan so a planning
// rejection -- an unsupported path, a static budget, a denied policy hop --
// still reports the relation work that produced it.
func (s *Service) ListRelationAwareUsage(ctx context.Context, request RelationListRequest) (RecordPage, bool, error) {
	if err := s.ready(); err != nil {
		return RecordPage{}, false, err
	}
	filter, err := parseFilterRequest(request.Filter)
	if err != nil {
		return RecordPage{}, false, err
	}
	requestedSort, err := parseSort(request.Sort)
	if err != nil {
		return RecordPage{}, false, err
	}

	page := RecordPage{Items: []map[string]any{}}
	usesRelation := false
	err = s.inTxCatalog(ctx, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		sortKeys := effectiveSortKeys(requestedSort)
		usesRelation = requestAddressesRelationField(collection, filter, sortKeys)
		plan, err := compileRelationListPlan(ctx, catalog, collection, request.Principal, s.policy, filter, sortKeys)
		if err != nil {
			return err
		}
		root, err := s.readRelationRootPageInTx(ctx, tx, catalog, collection, relationRootPageReadRequest{
			plan: plan, cursorToken: request.Cursor, limit: request.Limit,
			filter: filter, requestedSort: requestedSort, operationTime: s.now().UTC(),
			hydrate: func(items []map[string]any) error {
				return hydrateRelationRecords(ctx, tx, catalog, collection, items)
			},
		})
		if err != nil {
			return err
		}
		page = root.page
		return nil
	}, request.Collection)
	return page, usesRelation, err
}

// requestAddressesRelationField mirrors the operand classification that
// planRelationFilter and planRelationSort perform: an operand is scalar only
// when it names a known non-relation field without a dotted path. Everything
// else -- a dotted path, or the bare name of a root relation field -- is
// resolved against the catalog as a relation path, so it counts as relation
// work whether the plan then succeeds or is rejected. An unknown bare name is
// a plain validation error and stays scalar.
func requestAddressesRelationField(root schema.Collection, filter filterNode, sortKeys []SortKey) bool {
	for _, key := range sortKeys {
		if operandAddressesRelationField(root, key.Field) {
			return true
		}
	}
	return filterAddressesRelationField(root, filter)
}

func filterAddressesRelationField(root schema.Collection, node filterNode) bool {
	switch value := node.(type) {
	case filterAnd:
		return filterGroupAddressesRelationField(root, value.children)
	case filterOr:
		return filterGroupAddressesRelationField(root, value.children)
	case filterComparison:
		return operandAddressesRelationField(root, value.field)
	default:
		return false
	}
}

func filterGroupAddressesRelationField(root schema.Collection, children []filterNode) bool {
	for _, child := range children {
		if filterAddressesRelationField(root, child) {
			return true
		}
	}
	return false
}

func operandAddressesRelationField(root schema.Collection, name string) bool {
	if strings.Contains(name, ".") {
		return true
	}
	field, ok := filterField(root, name)
	return ok && field.Type == schema.TypeRelation
}

type relationRootPageReadRequest struct {
	plan          relationListPlan
	cursorToken   string
	limit         int
	filter        filterNode
	requestedSort []SortKey
	operationTime time.Time
	charge        func(int) error
	hydrate       func([]map[string]any) error
}

// readRelationRootPageInTx is the package-private root-page seam shared with
// expansion. Planning happens before this function, so malformed paths and
// static budgets fail before root record I/O.
func (s *Service) readRelationRootPageInTx(
	ctx context.Context,
	tx pgx.Tx,
	catalog schema.Catalog,
	collection schema.Collection,
	request relationRootPageReadRequest,
) (rootPageReadResult, error) {
	if !relationListPlanUsesRelation(request.plan) {
		return s.readRootPageInTx(ctx, tx, collection, rootPageReadRequest{
			cursorToken: request.cursorToken, limit: request.limit, filter: request.filter,
			requestedSort: request.requestedSort, operationTime: request.operationTime,
			charge: request.charge, hydrate: request.hydrate,
		})
	}
	pinned, ok := catalog.Collections[collection.Name]
	if !ok || pinned.ID != collection.ID || pinned.TableName != collection.TableName ||
		request.plan.root.ID != collection.ID || request.plan.root.Name != collection.Name {
		return rootPageReadResult{}, schema.ErrConflict
	}

	result := rootPageReadResult{
		page:               RecordPage{Items: []map[string]any{}},
		sortKeys:           relationPlanSortKeys(request.plan),
		expiresAt:          request.operationTime.Add(cursorTTL),
		privateCursor:      request.plan.privateCursor,
		cursorValuesByRoot: make(map[string][]json.RawMessage),
	}
	charge := request.charge
	if charge == nil {
		charge = func(int) error { return nil }
	}
	if request.plan.privateCursor {
		if err := charge(1); err != nil {
			return rootPageReadResult{}, err
		}
		if err := enforceRelationSortRootBudget(ctx, tx, collection); err != nil {
			return rootPageReadResult{}, err
		}
	}

	var after *PageCursor
	if request.cursorToken != "" {
		decoded, err := s.cursors.Decode(request.cursorToken, collection.ID, request.operationTime)
		if err != nil || !sameSortKeys(decoded.Sort, result.sortKeys) || (request.plan.privateCursor && !decoded.PrivateValues) {
			return rootPageReadResult{}, invalidCursor()
		}
		if len(decoded.Values) == 0 {
			if err := charge(1); err != nil {
				return rootPageReadResult{}, err
			}
			statement, err := request.plan.anchorStatement(decoded.AnchorID)
			if err != nil {
				return rootPageReadResult{}, invalidCursor()
			}
			values, err := scanPlannedSortValues(tx.QueryRow(ctx, statement.SQL, statement.Args...), request.plan.sort)
			if errors.Is(err, pgx.ErrNoRows) {
				return rootPageReadResult{}, invalidCursor()
			}
			if err != nil {
				return rootPageReadResult{}, err
			}
			digest, err := s.cursors.privateCursorValuesHash(values)
			if err != nil || !hmac.Equal(digest, decoded.AnchorHash) {
				return rootPageReadResult{}, invalidCursor()
			}
			decoded.Values = values
		}
		after = &decoded
	}

	if err := charge(1); err != nil {
		return rootPageReadResult{}, err
	}
	statement, err := request.plan.statement(after, request.limit)
	if err != nil {
		return rootPageReadResult{}, err
	}
	rows, err := tx.Query(ctx, statement.SQL, statement.Args...)
	if err != nil {
		return rootPageReadResult{}, err
	}
	defer rows.Close()

	pageLimit := normalizedFetchLimit(request.limit) - 1
	responseBytes := 0
	hasMore := false
	for rows.Next() {
		scanner, err := newRelationRecordScanner(rows, request.plan.sort)
		if err != nil {
			return rootPageReadResult{}, err
		}
		record, err := EncodeRecord(collection, scanner)
		if err != nil {
			return rootPageReadResult{}, err
		}
		values, err := scanner.cursorValues(record)
		if err != nil {
			return rootPageReadResult{}, err
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			return rootPageReadResult{}, fmt.Errorf("measure relation record page item: %w", err)
		}
		if len(result.page.Items) > 0 && responseBytes+len(encoded) > maximumRecordPageBytes {
			hasMore = true
			break
		}
		result.page.Items = append(result.page.Items, record)
		responseBytes += len(encoded)
		id, err := canonicalRecordID(record)
		if err != nil {
			return rootPageReadResult{}, schema.ErrConflict
		}
		result.cursorValuesByRoot[id.String()] = cloneCursorValues(values)
		if len(result.page.Items) > pageLimit {
			result.page.Items = result.page.Items[:pageLimit]
			hasMore = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return rootPageReadResult{}, err
	}
	rows.Close()

	if request.hydrate != nil {
		if err := request.hydrate(result.page.Items); err != nil {
			return rootPageReadResult{}, err
		}
	}
	var hydratedBytes int
	for index, record := range result.page.Items {
		encoded, err := json.Marshal(record)
		if err != nil {
			return rootPageReadResult{}, fmt.Errorf("measure hydrated relation record page item: %w", err)
		}
		if index > 0 && hydratedBytes+len(encoded) > maximumRecordPageBytes {
			result.page.Items = result.page.Items[:index]
			hasMore = true
			break
		}
		hydratedBytes += len(encoded)
	}
	if err := s.finalizeRelationRecordPage(&result, collection.ID, hasMore); err != nil {
		return rootPageReadResult{}, err
	}
	return result, nil
}

// enforceRelationSortRootBudget caps the work of the LEFT-JOIN sort plan.
// The probe reads at most limit+1 primary-key entries in the same repeatable-
// read snapshot; oversized collections fail before target joins or sorting.
func enforceRelationSortRootBudget(ctx context.Context, tx pgx.Tx, collection schema.Collection) error {
	statement := "SELECT EXISTS (SELECT 1 FROM " + quotedTable(collection) +
		" ORDER BY " + quoted(systemFieldID) + " OFFSET $1 LIMIT 1)"
	var exceeded bool
	if err := tx.QueryRow(ctx, statement, maximumRelationSortRootRows).Scan(&exceeded); err != nil {
		return err
	}
	if exceeded {
		return relationSortBudgetExceeded()
	}
	return nil
}

func relationListPlanUsesRelation(plan relationListPlan) bool {
	if plan.privateCursor {
		return true
	}
	var visit func(*plannedRelationFilter) bool
	visit = func(node *plannedRelationFilter) bool {
		if node == nil {
			return false
		}
		if node.comparison != nil && node.comparison.path != nil {
			return true
		}
		for _, child := range node.children {
			if visit(child) {
				return true
			}
		}
		return false
	}
	return visit(plan.filter)
}

func relationPlanSortKeys(plan relationListPlan) []SortKey {
	keys := make([]SortKey, len(plan.sort))
	for index, planned := range plan.sort {
		keys[index] = planned.key
	}
	return keys
}

type relationRecordScanner struct {
	row          RowScanner
	planned      []plannedRelationSort
	hiddenFields []schema.Field
	hidden       []any
}

func newRelationRecordScanner(row RowScanner, planned []plannedRelationSort) (*relationRecordScanner, error) {
	scanner := &relationRecordScanner{row: row, planned: planned}
	for _, sort := range planned {
		if sort.path == nil {
			continue
		}
		destination, err := scanDestination(sort.field)
		if err != nil {
			return nil, err
		}
		scanner.hiddenFields = append(scanner.hiddenFields, sort.field)
		scanner.hidden = append(scanner.hidden, destination)
	}
	return scanner, nil
}

func (s *relationRecordScanner) Scan(destinations ...any) error {
	return s.row.Scan(append(destinations, s.hidden...)...)
}

func (s *relationRecordScanner) cursorValues(record map[string]any) ([]json.RawMessage, error) {
	valuesByName := cloneRecord(record)
	hidden := 0
	for _, planned := range s.planned {
		if planned.path == nil {
			continue
		}
		value, err := encodeScannedValue(s.hiddenFields[hidden], s.hidden[hidden])
		if err != nil {
			return nil, err
		}
		valuesByName[planned.key.Field] = value
		hidden++
	}
	return recordSortValues(relationPlanSortKeys(relationListPlan{sort: s.planned}), valuesByName)
}

func scanPlannedSortValues(row RowScanner, planned []plannedRelationSort) ([]json.RawMessage, error) {
	destinations := make([]any, len(planned))
	for index := range planned {
		destination, err := scanDestination(planned[index].field)
		if err != nil {
			return nil, err
		}
		destinations[index] = destination
	}
	if err := row.Scan(destinations...); err != nil {
		return nil, err
	}
	values := make([]json.RawMessage, len(planned))
	for index := range planned {
		value, err := encodeScannedValue(planned[index].field, destinations[index])
		if err != nil {
			return nil, err
		}
		values[index], err = json.Marshal(value)
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}

// finalizeRelationRecordPage measures the exact JSON object the API emits the
// way finalizeRecordPage does. Item encodings never change while the tail is
// trimmed, so they are measured once and the running total is decremented per
// dropped item. Only the envelope is re-encoded per attempt, because its size
// follows the continuation cursor, which follows the anchor record.
// Re-marshalling the whole page on every attempt was quadratic in the page
// size for a bound that is exceeded by a few kilobytes.
func (s *Service) finalizeRelationRecordPage(result *rootPageReadResult, collectionID uuid.UUID, hasMore bool) error {
	itemBytes := make([]int, len(result.page.Items))
	measured := 0
	for index, item := range result.page.Items {
		encoded, err := json.Marshal(item)
		if err != nil {
			return fmt.Errorf("measure relation record page response: %w", err)
		}
		itemBytes[index] = len(encoded)
		measured += len(encoded)
	}
	for {
		result.page.NextCursor = ""
		if hasMore && len(result.page.Items) > 0 {
			anchor := result.page.Items[len(result.page.Items)-1]
			anchorID, err := canonicalRecordID(anchor)
			if err != nil {
				return errors.New("encode relation page cursor: invalid record id")
			}
			values, ok := result.cursorValuesByRoot[anchorID.String()]
			if !ok || len(values) != len(result.sortKeys) {
				return schema.ErrConflict
			}
			result.page.NextCursor, err = s.cursors.Encode(PageCursor{
				Version: cursorVersion, CollectionID: collectionID, Sort: result.sortKeys,
				Values: cloneCursorValues(values), AnchorID: anchorID, PrivateValues: result.privateCursor,
				ExpiresAt: result.expiresAt,
			})
			if err != nil {
				return err
			}
		}
		// An empty page encodes the exact envelope this page will emit; the
		// items add their own bytes plus one separating comma each.
		envelope, err := json.Marshal(RecordPage{Items: []map[string]any{}, NextCursor: result.page.NextCursor})
		if err != nil {
			return fmt.Errorf("measure relation record page response: %w", err)
		}
		responseBytes := len(envelope) + measured
		if len(result.page.Items) > 0 {
			responseBytes += len(result.page.Items) - 1
		}
		if responseBytes+1 <= maximumRecordPageBytes {
			return nil
		}
		if len(result.page.Items) <= 1 {
			return validation("response", "response_too_large")
		}
		measured -= itemBytes[len(result.page.Items)-1]
		result.page.Items = result.page.Items[:len(result.page.Items)-1]
		hasMore = true
	}
}

func cloneCursorValues(values []json.RawMessage) []json.RawMessage {
	cloned := make([]json.RawMessage, len(values))
	for index := range values {
		cloned[index] = append(json.RawMessage(nil), values[index]...)
	}
	return cloned
}
