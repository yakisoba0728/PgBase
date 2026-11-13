package query

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/responseprojection"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"strings"
)

// PocketBasePage is the exact list envelope exposed by the explicit
// PocketBase compatibility adapter.
type PocketBasePage struct {
	Page       int              `json:"page"`
	PerPage    int              `json:"perPage"`
	TotalItems int64            `json:"totalItems"`
	TotalPages int64            `json:"totalPages"`
	Items      []map[string]any `json:"items"`
}

type PocketBaseListRequest struct {
	ListRequest
	Page      int
	PerPage   int
	SkipTotal bool
}

// ListPocketBase reads one offset page and its optional total in a single
// repeatable-read snapshot. It is deliberately separate from List so native
// callers keep the cursor wire contract.
func ListPocketBase(ctx context.Context, s *Service, request PocketBaseListRequest) (page PocketBasePage, err error) {
	if err := s.ready(); err != nil {
		return page, err
	}
	if !request.Principal.Valid() {
		return page, validation("principal", "invalid")
	}
	filterNode, err := parseFilterRequest(request.Filter)
	if err != nil {
		return page, err
	}
	filterNode = usePercentWildcards(filterNode)
	sortKeys, err := parseSort(request.Sort)
	if err != nil {
		return page, err
	}
	request.Page, request.PerPage = normalizePocketBasePage(request.Page, request.PerPage)
	if request.Page-1 > int(^uint(0)>>1)/request.PerPage {
		return page, validation("page", "invalid_page")
	}
	request.ListRequest.Limit = request.PerPage
	request.ListRequest.Offset = (request.Page - 1) * request.PerPage
	request.ListRequest.Cursor = ""
	request.ListRequest.PocketBaseCompatibility = true
	page = PocketBasePage{Page: request.Page, PerPage: request.PerPage, TotalItems: -1, TotalPages: -1, Items: []map[string]any{}}
	err = s.inTxCatalog(ctx, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		if !request.SkipTotal {
			total, countErr := s.countListInTx(ctx, tx, catalog, collection, request.ListRequest, filterNode, 0)
			if countErr != nil {
				return countErr
			}
			page.TotalItems = total
			page.TotalPages = (total + int64(request.PerPage) - 1) / int64(request.PerPage)
			if s.countPageHook != nil {
				s.countPageHook()
			}
		}
		result, _, readErr := s.readListInTx(ctx, tx, catalog, collection, request.ListRequest, filterNode, sortKeys)
		page.Items = result.page.Items
		return readErr
	}, request.Collection)
	return page, err
}

func normalizePocketBasePage(page, perPage int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 {
		perPage = 30
	} else if perPage > maximumPageLimit {
		perPage = maximumPageLimit
	}
	return page, perPage
}

func normalizePocketBaseExpand(catalog schema.Catalog, root schema.Collection, text string, limits expandLimits) (string, error) {
	if len(text) > maximumExpandBytes {
		return "", validation("expand", "expand_budget_exceeded")
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
				return "", err
			}
			continue
		}
		return "", err
	}
	if len(valid) == 0 {
		return "", nil
	}
	return strings.Join(valid, ","), nil
}

func parseListFilter(request ListRequest) (filterNode, error) {
	if !request.PocketBaseCompatibility {
		return parseFilterRequest(request.Filter)
	}
	node, err := parseFilterRequest(request.Filter)
	if err != nil {
		return nil, err
	}
	return usePercentWildcards(node), nil
}

func (s *Service) planRead(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, collection schema.Collection, request ListRequest, filter filterNode, sort []SortKey) (rootPlan, error) {
	if request.PocketBaseCompatibility {
		sort = pocketBaseSortKeys(collection, sort)
	}
	return s.planNativeRead(ctx, tx, catalog, collection, request, filter, sort)
}

func (s *Service) readListInTx(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, collection schema.Collection, request ListRequest, filter filterNode, sort []SortKey) (listResult, bool, error) {
	if !request.PocketBaseCompatibility {
		return s.readNativeListInTx(ctx, tx, catalog, collection, request, filter, sort)
	}
	fields := request.Fields
	expanded := request.Expand != ""
	var err error
	request.Expand, err = normalizePocketBaseExpand(catalog, collection, request.Expand, s.expandLimits)
	if err != nil {
		return listResult{}, false, err
	}
	request.Fields, request.PocketBaseCompatibility = "", false
	result, relation, err := s.readNativeListInTx(ctx, tx, catalog, collection, request, filter, pocketBaseSortKeys(collection, sort))
	if err != nil {
		return listResult{}, relation, err
	}
	if expanded && request.Expand == "" {
		result.expanded = make([]ExpandedRecord, len(result.page.Items))
		for i, item := range result.page.Items {
			result.expanded[i] = ExpandedRecord{Root: cloneRecord(item)}
		}
	}
	projection, err := compileRecordProjection(collection, request.Principal, fields)
	if err != nil {
		return listResult{}, relation, err
	}
	for i, item := range result.page.Items {
		result.page.Items[i] = responseprojection.PocketBaseRecord(item, collection.ID.String(), collection.Name)
	}
	result.page = projection.applyPage(result.page)
	return result, relation, nil
}

func (s *Service) readGetInTx(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, collection schema.Collection, request GetRequest, result *getResult) error {
	if !request.PocketBaseCompatibility {
		return s.readNativeGetInTx(ctx, tx, catalog, collection, request, result)
	}
	fields := request.Fields
	expanded := request.Expand != ""
	var err error
	request.Expand, err = normalizePocketBaseExpand(catalog, collection, request.Expand, s.expandLimits)
	if err != nil {
		return err
	}
	request.Fields, request.PocketBaseCompatibility = "", false
	if err := s.readNativeGetInTx(ctx, tx, catalog, collection, request, result); err != nil {
		return err
	}
	if expanded && request.Expand == "" {
		result.expanded = ExpandedRecord{Root: cloneRecord(result.record.Data)}
	}
	projection, err := compileRecordProjection(collection, request.Principal, fields)
	if err != nil {
		return err
	}
	result.record.Data = projection.apply(responseprojection.PocketBaseRecord(result.record.Data, collection.ID.String(), collection.Name))
	return nil
}

func usePercentWildcards(node filterNode) filterNode {
	switch value := node.(type) {
	case filterComparison:
		value.percentWildcard = true
		return value
	case filterAnd:
		for i := range value.children {
			value.children[i] = usePercentWildcards(value.children[i])
		}
		return value
	case filterOr:
		for i := range value.children {
			value.children[i] = usePercentWildcards(value.children[i])
		}
		return value
	default:
		return node
	}
}
